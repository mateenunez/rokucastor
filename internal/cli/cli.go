// Package cli wires the command line to rokucastor's features.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/stupside/rokucastor/internal/config"
	"github.com/stupside/rokucastor/internal/tui"
	"github.com/stupside/rokucastor/internal/whisper"
)

// Options carries build information into the command line.
type Options struct {
	Version string
	Commit  string
}

// Run executes the command line and returns when it finishes or fails.
func Run(ctx context.Context, args []string, opts Options) error {
	return run(ctx, args, opts, os.Stdout, os.Stderr)
}

func run(ctx context.Context, args []string, opts Options, stdout, stderr io.Writer) error {
	paths, err := config.Paths()
	if err != nil {
		return err
	}
	closeLogs, err := config.InitLogging(paths.Logs)
	if err != nil {
		return err
	}
	defer closeLogs()

	app := &cli.Command{
		Name:      "rokucastor",
		Usage:     "Cast local video files to a Roku",
		Version:   opts.Version,
		Writer:    stdout,
		ErrWriter: stderr,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "device",
				Aliases: []string{"d"},
				Usage:   "Roku address; omit to find Rokus on the local network",
			},
			&cli.StringFlag{
				Name:    "password",
				Aliases: []string{"p"},
				Usage:   "Roku developer-mode password, to install/replace the channel if needed; saved in config.json",
			},
			&cli.IntFlag{
				Name:  "startup-timeout",
				Usage: "seconds to wait for the Roku to start playing; saved in config.json",
				Value: int(config.DefaultStartupTimeout / time.Second),
			},
			&cli.StringFlag{
				Name:  "app",
				Usage: "ID of an already installed channel to launch instead of sideloading rokucastor's",
			},
			&cli.StringFlag{
				Name:    "language",
				Aliases: []string{"l"},
				Usage:   "subtitle language, as ISO 639-1 (es) or ISO 639-2 (spa)",
				Value:   "es",
			},
			&cli.StringFlag{
				Name:  "model",
				Usage: "Whisper model for cached SRT files: small (default), base, tiny or tiny.en",
				Value: whisper.DefaultModel,
			},
			&cli.StringFlag{
				Name:  "stream-model",
				Usage: "Whisper model for live subtitles burned in during casting: tiny (default), tiny.en, base or small",
				Value: whisper.DefaultStreamModel,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			settings, err := rememberSettings(cmd)
			if err != nil {
				return err
			}
			return tui.Run(ctx, tui.Options{
				Device:         cmd.String("device"),
				Password:       settings.Password,
				StartupTimeout: settings.StartupTimeout(),
				AppID:          cmd.String("app"),
				Language:       cmd.String("language"),
				Model:          cmd.String("model"),
				StreamModel:    cmd.String("stream-model"),
			})
		},
		Commands: []*cli.Command{
			castCommand(),
			devicesCommand(),
			subtitlesCommand(),
			versionCommand(opts),
			pathsCommand(),
		},
	}
	err = app.Run(ctx, args)
	// Ctrl+C cancels the cast; that is how the user asked to stop, not a
	// failure to report.
	if err != nil && errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func versionCommand(opts Options) *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "Print the build version",
		Action: func(_ context.Context, cmd *cli.Command) error {
			fmt.Fprintf(cmd.Root().Writer, "rokucastor %s (%s)\n", opts.Version, opts.Commit)
			return nil
		},
	}
}

// pathsCommand exposes the per-user directories so support requests can find
// the cache, models and logs without reading the source.
func pathsCommand() *cli.Command {
	return &cli.Command{
		Name:  "paths",
		Usage: "Print the per-user directories rokucastor uses",
		Action: func(_ context.Context, cmd *cli.Command) error {
			paths, err := config.Paths()
			if err != nil {
				return err
			}
			out := cmd.Root().Writer
			fmt.Fprintf(out, "root:   %s\n", paths.Root)
			fmt.Fprintf(out, "config: %s\n", paths.Config)
			fmt.Fprintf(out, "bin:    %s\n", paths.Bin)
			fmt.Fprintf(out, "models: %s\n", paths.Models)
			fmt.Fprintf(out, "subs:   %s\n", paths.Subs)
			fmt.Fprintf(out, "logs:   %s\n", paths.Logs)
			return nil
		},
	}
}

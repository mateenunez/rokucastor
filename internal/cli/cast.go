package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/stupside/rokucastor/internal/cast"
	"github.com/stupside/rokucastor/internal/config"
	"github.com/stupside/rokucastor/internal/whisper"
)

func castCommand() *cli.Command {
	return &cli.Command{
		Name:      "cast",
		Usage:     "Play a local video on a Roku",
		ArgsUsage: "<video>",
		Description: "Serves the video from this computer and launches the rokucastor channel on the Roku.\n" +
			"Ctrl+C stops the cast and leaves the Roku on its home screen.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "device",
				Aliases:  []string{"d"},
				Usage:    "Roku address: a host, host:port or URL",
				Required: true,
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
				Name:  "at",
				Usage: "start at this position, such as 1h12m or 1:12 (1:00 is one hour, 1:12:30 is H:MM:SS)",
				Validator: func(s string) error {
					_, err := cast.ParseStart(s)
					return err
				},
			},
			&cli.StringFlag{
				Name:      "subtitle",
				Aliases:   []string{"s"},
				Usage:     "SRT subtitle file to side-load",
				TakesFile: true,
			},
			&cli.StringFlag{
				Name:    "language",
				Aliases: []string{"l"},
				Usage:   "subtitle language, as ISO 639-1 (es) or ISO 639-2 (spa)",
				Value:   "es",
			},
			&cli.BoolFlag{
				Name:  "generate-subtitles",
				Usage: "transcribe the audio live with Whisper and burn the subtitles into the video",
			},
			&cli.StringFlag{
				Name:  "stream-model",
				Usage: "Whisper model for live subtitles: tiny (default), tiny.en, base or small",
				Value: whisper.DefaultStreamModel,
			},
		},
		Action: runCast,
	}
}

func runCast(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 1 {
		return errors.New("cast takes exactly one video file")
	}
	video := cmd.Args().First()
	out := cmd.Root().Writer
	errOut := cmd.Root().ErrWriter

	settings, err := rememberSettings(cmd)
	if err != nil {
		return err
	}
	start, err := cast.ParseStart(cmd.String("at"))
	if err != nil {
		return err
	}
	generate := cmd.Bool("generate-subtitles")
	if generate && strings.TrimSpace(cmd.String("subtitle")) != "" {
		return errors.New("--subtitle and --generate-subtitles cannot be used together")
	}
	layout := config.Layout{}
	if generate {
		layout, err = config.Paths()
		if err != nil {
			return err
		}
	}

	fmt.Fprintf(out, "Connecting to Roku at %s...\n", cmd.String("device"))
	session, err := cast.Start(ctx, cast.Options{
		Address:           cmd.String("device"),
		Password:          settings.Password,
		StartupTimeout:    settings.StartupTimeout(),
		AppID:             cmd.String("app"),
		Video:             video,
		Start:             start,
		Subtitle:          cmd.String("subtitle"),
		Language:          cmd.String("language"),
		GenerateSubtitles: generate,
		StreamModel:       cmd.String("stream-model"),
		Layout:            layout,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := session.Close(); err != nil {
			fmt.Fprintln(errOut, "rokucastor:", err)
		}
	}()
	if start > 0 {
		fmt.Fprintf(out, "Playing %s on %s from %s\n", filepath.Base(video), session.Player.Name(), start.Truncate(time.Second))
	} else {
		fmt.Fprintf(out, "Playing %s on %s\n", filepath.Base(video), session.Player.Name())
	}

	err = session.AwaitEnd(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	if err == nil {
		fmt.Fprintln(out, "Playback finished.")
	}
	return nil
}

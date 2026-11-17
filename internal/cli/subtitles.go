package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/urfave/cli/v3"

	"github.com/stupside/rokucastor/internal/config"
	"github.com/stupside/rokucastor/internal/whisper"
)

func subtitlesCommand() *cli.Command {
	return &cli.Command{
		Name:      "subtitles",
		Aliases:   []string{"subs"},
		Usage:     "Generate SRT subtitles for a local video with Whisper",
		ArgsUsage: "<video>",
		Description: "Downloads whisper-cli and the selected model on first use, then writes an SRT\n" +
			"into the per-user cache under a hash-based name and prints its path. Re-running\n" +
			"reuses the transcript unless --force is given or the video changed.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "model",
				Usage: "Whisper model: small (default), base, tiny or tiny.en",
				Value: whisper.DefaultModel,
			},
			&cli.StringFlag{
				Name:    "language",
				Aliases: []string{"l"},
				Usage:   "spoken language, as ISO 639-1 (es) or ISO 639-2 (spa)",
				Value:   "es",
			},
			&cli.BoolFlag{
				Name:  "force",
				Usage: "regenerate even when a cached transcript matches",
			},
		},
		Action: runSubtitles,
	}
}

func runSubtitles(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 1 {
		return errors.New("subtitles takes exactly one video file")
	}
	paths, err := config.Paths()
	if err != nil {
		return err
	}

	out, errOut := cmd.Root().Writer, cmd.Root().ErrWriter
	video := cmd.Args().First()
	printer := &progressPrinter{w: errOut}

	srt, err := whisper.Video(ctx, whisper.Options{
		Layout:   paths,
		Video:    video,
		Model:    cmd.String("model"),
		Language: cmd.String("language"),
		Force:    cmd.Bool("force"),
		Progress: printer.update,
	})
	printer.done()
	if err != nil {
		return err
	}
	fmt.Fprintln(out, srt)
	return nil
}

// progressPrinter rewrites one line per stage so a long download still shows
// where it is without filling the terminal.
type progressPrinter struct {
	w     io.Writer
	stage whisper.Stage
	open  bool
	last  string
}

func (p *progressPrinter) update(progress whisper.Progress) {
	if p.stage != progress.Stage {
		if p.open {
			fmt.Fprintln(p.w)
		}
		p.stage, p.open = progress.Stage, true
	}
	line := progressText(progress)
	if progress.Percent >= 0 {
		line = fmt.Sprintf("%s %d%%", line, progress.Percent)
	}
	if line == p.last {
		return
	}
	p.last = line
	fmt.Fprintf(p.w, "\r%-64s", line)
}

func (p *progressPrinter) done() {
	if p.open {
		fmt.Fprintln(p.w)
	}
}

func progressText(progress whisper.Progress) string {
	switch progress.Stage {
	case whisper.StageTool:
		return "Downloading whisper-cli"
	case whisper.StageModel:
		return "Downloading the Whisper model"
	case whisper.StageAudio:
		return "Extracting the audio track"
	case whisper.StageSpeech:
		if progress.Detail == "cached" {
			return "Using cached subtitles"
		}
		return "Transcribing speech"
	}
	return "Preparing Whisper"
}

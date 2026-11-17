// Package whisper turns a local video into an SRT subtitle file with the
// whisper.cpp command-line tool, downloading the tool and the ggml model on
// first use and caching each transcript by video, model and language.
package whisper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/stupside/rokucastor/internal/config"
	"github.com/stupside/rokucastor/internal/media"
)

// Stage is one step of generating subtitles.
type Stage string

const (
	StageTool   Stage = "tool"
	StageModel  Stage = "model"
	StageAudio  Stage = "audio"
	StageSpeech Stage = "speech"
)

// Progress is how far the current stage has got. Percent is -1 while a stage
// cannot measure itself.
type Progress struct {
	Stage   Stage
	Percent int
	Detail  string
}

// Options selects what to transcribe and where the per-user files live.
type Options struct {
	// Layout is the per-user directory tree; Ensure may be called on it.
	Layout config.Layout
	// Video is the local file to transcribe.
	Video string
	// Model is "small" (default) or "base".
	Model string
	// Language is the spoken language, as ISO 639-1 or ISO 639-2.
	Language string
	// Force regenerates even when a cached SRT matches.
	Force bool
	// HTTP downloads the tool and model; nil uses http.DefaultClient.
	HTTP *http.Client
	// FFmpeg is the ffmpeg executable; empty uses "ffmpeg" on PATH.
	FFmpeg string
	// Progress receives stage updates; nil is silent.
	Progress func(Progress)
}

// Cached returns the SRT an earlier run generated for the video, model and
// language, without downloading the tool or transcribing anything.
func Cached(layout config.Layout, video, model, language string) (string, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		model = DefaultModel
	}
	if _, err := modelArtifact(model); err != nil {
		return "", false
	}
	trackLanguage, err := media.LanguageCode(language)
	if err != nil {
		return "", false
	}
	if !regular(video) {
		return "", false
	}
	key, err := cacheKey(video, model, trackLanguage)
	if err != nil {
		return "", false
	}
	path := filepath.Join(layout.Subs, key+".srt")
	if !regular(path) {
		return "", false
	}
	return path, true
}

// Video returns the path of a cached SRT for the video, generating it first
// when there is no matching transcript yet.
func Video(ctx context.Context, opts Options) (string, error) {
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = DefaultModel
	}
	if _, err := modelArtifact(model); err != nil {
		return "", err
	}

	whisperLanguage, err := media.WhisperCode(opts.Language)
	if err != nil {
		return "", err
	}
	// The track language is ISO 639-2/B; it also keeps two languages of the
	// same video from sharing a cached file.
	trackLanguage, err := media.LanguageCode(opts.Language)
	if err != nil {
		return "", err
	}

	if !regular(opts.Video) {
		return "", fmt.Errorf("%q is not a regular file", opts.Video)
	}

	key, err := cacheKey(opts.Video, model, trackLanguage)
	if err != nil {
		return "", err
	}
	if err := opts.Layout.Ensure(); err != nil {
		return "", err
	}
	cached := filepath.Join(opts.Layout.Subs, key+".srt")
	if !opts.Force && regular(cached) {
		report(opts.Progress, Progress{Stage: StageSpeech, Percent: 100, Detail: "cached"})
		return cached, nil
	}

	client := opts.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	ffmpeg := opts.FFmpeg
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	install := &installer{
		layout:   opts.Layout,
		http:     client,
		ffmpeg:   ffmpeg,
		progress: opts.Progress,
	}

	tool, err := install.tool(ctx)
	if err != nil {
		return "", err
	}
	modelPath, err := install.model(ctx, model)
	if err != nil {
		return "", err
	}

	work, err := os.MkdirTemp(opts.Layout.Root, "whisper-")
	if err != nil {
		return "", fmt.Errorf("creating a work directory: %w", err)
	}
	defer os.RemoveAll(work)

	generated, err := install.transcribe(ctx, tool, modelPath, opts.Video, whisperLanguage, work)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := keep(generated, cached); err != nil {
		return "", err
	}
	report(opts.Progress, Progress{Stage: StageSpeech, Percent: 100})
	return cached, nil
}

// keep moves the generated SRT into the cache, falling back to a copy when the
// work and cache directories are on different volumes.
func keep(generated, cached string) error {
	if err := os.Rename(generated, cached); err == nil {
		return nil
	}
	in, err := os.Open(generated)
	if err != nil {
		return fmt.Errorf("reading the generated subtitles: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(cached, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("saving the generated subtitles: %w", err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(cached)
		return fmt.Errorf("saving the generated subtitles: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(cached)
		return fmt.Errorf("saving the generated subtitles: %w", closeErr)
	}
	return nil
}

func report(report func(Progress), p Progress) {
	if report != nil {
		report(p)
	}
}

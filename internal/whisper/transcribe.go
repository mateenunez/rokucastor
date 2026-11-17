package whisper

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const (
	// The audio track is all whisper.cpp reads; 16 kHz mono PCM is what it
	// wants, so ffmpeg does the converting once.
	sampleRate = "16000"

	maxThreads = 8
	tailLimit  = 4096
)

// progressLine matches whisper.cpp's optional progress output.
var progressLine = regexp.MustCompile(`progress\s*=\s*(\d+)%`)

// transcribe extracts the audio, runs whisper-cli and returns the SRT it wrote.
// The file is named after prefix and lives in workDir.
func (i *installer) transcribe(ctx context.Context, tool, model, video, language, workDir string) (string, error) {
	audio := filepath.Join(workDir, "audio.wav")
	i.report(Progress{Stage: StageAudio, Percent: -1})
	if err := i.extractAudio(ctx, video, audio); err != nil {
		return "", err
	}

	prefix := filepath.Join(workDir, "subtitles")
	i.report(Progress{Stage: StageSpeech, Percent: 0})
	if err := i.runWhisper(ctx, tool, model, audio, language, prefix); err != nil {
		return "", err
	}

	srt := prefix + ".srt"
	info, err := os.Stat(srt)
	if err != nil || info.Size() == 0 {
		return "", ErrNoSubtitles
	}
	return srt, nil
}

func (i *installer) extractAudio(ctx context.Context, video, audio string) error {
	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", video,
		"-vn", "-ac", "1", "-ar", sampleRate, "-c:a", "pcm_s16le",
		audio,
	}
	cmd := exec.CommandContext(ctx, i.ffmpeg, args...)
	var stderr tailWriter
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("extracting the audio track with ffmpeg: %w%s", err, stderr.suffix())
	}
	return nil
}

func (i *installer) runWhisper(ctx context.Context, tool, model, audio, language, prefix string) error {
	cmd := exec.CommandContext(ctx, tool, whisperArgs(model, audio, language, prefix)...)
	var stderr tailWriter
	cmd.Stdout = io.Discard
	cmd.Stderr = &progressWriter{report: func(percent int) {
		i.report(Progress{Stage: StageSpeech, Percent: percent})
	}, tail: &stderr}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("running whisper-cli: %w%s", err, stderr.suffix())
	}
	return nil
}

// whisperArgs builds the command line: one SRT segment file, progress on
// stderr, and no banner or per-token chatter.
func whisperArgs(model, audio, language, prefix string) []string {
	return []string{
		"-m", model,
		"-f", audio,
		"-l", language,
		"-osrt",
		"-of", prefix,
		"-pp",
		"-np",
		"-t", strconv.Itoa(min(runtime.NumCPU(), maxThreads)),
	}
}

// progressWriter parses whisper's progress lines and keeps the tail of its
// output for an error message.
type progressWriter struct {
	report func(int)
	tail   *tailWriter
	carry  []byte
	last   int
	seen   bool
}

func (w *progressWriter) Write(p []byte) (int, error) {
	buf := append(w.carry, p...)
	for _, match := range progressLine.FindAllSubmatch(buf, -1) {
		percent, err := strconv.Atoi(string(match[1]))
		if err != nil || (w.seen && percent <= w.last) {
			continue
		}
		w.seen, w.last = true, percent
		w.report(percent)
	}
	// Keep enough of the end that a line split across writes is still seen.
	if len(buf) > 64 {
		buf = buf[len(buf)-64:]
	}
	w.carry = append(w.carry[:0], buf...)
	_, _ = w.tail.Write(p)
	return len(p), nil
}

// tailWriter keeps the last few lines of a child process's stderr.
type tailWriter struct{ buf []byte }

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailLimit {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-tailLimit:]...)
	}
	return len(p), nil
}

func (t *tailWriter) suffix() string {
	text := strings.TrimSpace(string(t.buf))
	if text == "" {
		return ""
	}
	return ": " + text
}

package subtitle

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// cueLeadBias compensates for encoder lookahead (Whisper ~±0.5s accurate).
	cueLeadBias = 1.0

	// cueWrapColumns: broadcast convention (two lines in safe area).
	cueWrapColumns = 42
)

// CueFile is the live subtitle text file the encoder reads, swapped as cues commit.
type CueFile struct {
	path string
	cues *Cues
}

// NewCueFile names the cue file a burn-in encode keeps inside dir. Nothing is
// written until Create.
func NewCueFile(dir string, cues *Cues) *CueFile {
	return &CueFile{path: filepath.Join(dir, "cue.txt"), cues: cues}
}

// Create file before the encoder starts: drawtext dies on a missing text file.
func (f *CueFile) Create() (string, error) {
	if err := os.WriteFile(f.path, nil, 0o644); err != nil {
		return "", fmt.Errorf("creating subtitle cue file: %w", err)
	}
	return f.path, nil
}

// Writer keeps the cue file updated with the line covering the frame the
// encoder is producing. ffmpeg reports that position through -progress.
func (f *CueFile) Writer(ctx context.Context) func(time.Duration) {
	tmpPath := f.path + ".tmp"
	last := ""
	wroteCue := false
	return func(position time.Duration) {
		seconds := position.Seconds()
		text := wrap(f.cues.cueAt(seconds+cueLeadBias), cueWrapColumns)
		f.cues.DiscardBefore(seconds)
		if text == last {
			return
		}
		if err := swapFile(tmpPath, f.path, []byte(text)); err != nil {
			slog.WarnContext(ctx, "writing subtitle cue", "error", err)
			return
		}
		if !wroteCue && text != "" {
			slog.InfoContext(ctx, "first subtitle cue rendered", "out_time", seconds, "text", text)
			wroteCue = true
		}
		slog.DebugContext(ctx, "subtitle cue swapped", "out_time", seconds, "text", text)
		last = text
	}
}

// swapFile writes text beside path and renames it into place. Windows refuses
// to replace a file ffmpeg has open for reading by a moment; retrying and then
// writing in place keeps the live cue updated without stopping the encode.
func swapFile(tmp, path string, text []byte) error {
	if err := os.WriteFile(tmp, text, 0o644); err != nil {
		return err
	}
	for attempt := 0; attempt < 5; attempt++ {
		if err := os.Rename(tmp, path); err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(path, text, 0o644); err != nil {
		return err
	}
	_ = os.Remove(tmp)
	return nil
}

func wrap(text string, width int) string {
	var b strings.Builder
	lineLen := 0
	for w := range strings.FieldsSeq(text) {
		n := utf8.RuneCountInString(w)
		switch {
		case lineLen == 0:
			b.WriteString(w)
			lineLen = n
		case lineLen+1+n <= width:
			b.WriteByte(' ')
			b.WriteString(w)
			lineLen += 1 + n
		default:
			b.WriteByte('\n')
			b.WriteString(w)
			lineLen = n
		}
	}
	return b.String()
}

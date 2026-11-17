package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubtitlesRequiresExactlyOneVideo(t *testing.T) {
	for _, args := range [][]string{
		{"rokucastor", "subtitles"},
		{"rokucastor", "subtitles", "a.mp4", "b.mp4"},
	} {
		var out, errOut bytes.Buffer
		err := run(t.Context(), args, Options{}, &out, &errOut)
		if err == nil || !strings.Contains(err.Error(), "exactly one video file") {
			t.Errorf("subtitles %v = %v, want the argument count refusal", args[2:], err)
		}
	}
}

func TestSubtitlesRejectsBadOptionsBeforeDownloading(t *testing.T) {
	video := filepath.Join(t.TempDir(), "movie.mp4")
	if err := os.WriteFile(video, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"rokucastor", "subtitles", video, "--language", "xx"},
		{"rokucastor", "subtitles", video, "--model", "huge"},
	} {
		var out, errOut bytes.Buffer
		err := run(t.Context(), args, Options{}, &out, &errOut)
		if err == nil {
			t.Errorf("subtitles %v accepted an invalid option", args[2:])
		}
		if err != nil && strings.Contains(err.Error(), "downloading") {
			t.Errorf("subtitles %v reached the network: %v", args[2:], err)
		}
	}
}

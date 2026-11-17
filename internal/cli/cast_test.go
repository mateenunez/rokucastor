package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCreatesDiagnosticLogAutomatically(t *testing.T) {
	localAppData := t.TempDir()
	t.Setenv("LOCALAPPDATA", localAppData)

	var out, errOut bytes.Buffer
	if err := run(t.Context(), []string{"rokucastor", "paths"}, Options{}, &out, &errOut); err != nil {
		t.Fatalf("run paths = %v", err)
	}

	logPath := filepath.Join(localAppData, "rokucastor", "logs", "rokucastor.log")
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading automatically created log %q: %v", logPath, err)
	}
	if !strings.Contains(string(log), "rokucastor session started") || !strings.Contains(string(log), "rokucastor session ended") {
		t.Errorf("log does not contain session boundaries: %q", log)
	}
}

func TestCastRejectsUnsupportedFormatsBeforeConnecting(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run(t.Context(), []string{"rokucastor", "cast", "movie.avi", "--device", "192.0.2.1"}, Options{}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "unsupported video format") {
		t.Fatalf("cast = %v, want the format refusal before any network use", err)
	}
}

func TestCastRequiresExactlyOneVideo(t *testing.T) {
	for _, args := range [][]string{
		{"rokucastor", "cast", "--device", "192.0.2.1"},
		{"rokucastor", "cast", "a.mp4", "b.mp4", "--device", "192.0.2.1"},
	} {
		var out, errOut bytes.Buffer
		err := run(t.Context(), args, Options{}, &out, &errOut)
		if err == nil || !strings.Contains(err.Error(), "exactly one video file") {
			t.Errorf("cast %v = %v, want the argument count refusal", args[2:], err)
		}
	}
}

func TestCastRejectsAnInvalidStartPosition(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run(t.Context(), []string{
		"rokucastor", "cast", "movie.mp4",
		"--device", "192.0.2.1",
		"--at", "1:75",
	}, Options{}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "not a position") {
		t.Fatalf("cast = %v, want the position refusal before any network use", err)
	}
}

func TestCastValidatesTheSubtitleLanguageBeforeConnecting(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run(t.Context(), []string{
		"rokucastor", "cast", "movie.mp4",
		"--device", "192.0.2.1",
		"--subtitle", "movie.srt",
		"--language", "xx",
	}, Options{}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "unknown subtitle language") {
		t.Fatalf("cast = %v, want the language refusal before any network use", err)
	}
}

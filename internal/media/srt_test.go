package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShiftSRTDropsEarlyCuesAndMovesTheRest(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "movie.srt")
	dest := filepath.Join(dir, "shifted.srt")
	body := "\ufeff1\r\n00:59:00,000 --> 00:59:02,000\r\nearly\r\n\r\n" +
		"2\r\n00:59:59,000 --> 01:00:01,500\r\nspanning\r\n\r\n" +
		"3\r\n01:00:05,000 --> 01:00:07,250\r\nlater\r\n"
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ShiftSRT(source, dest, time.Hour); err != nil {
		t.Fatal(err)
	}
	shifted, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	want := "1\n00:00:00,000 --> 00:00:01,500\nspanning\n\n" +
		"2\n00:00:05,000 --> 00:00:07,250\nlater\n"
	if string(shifted) != want {
		t.Fatalf("shifted subtitles = %q, want %q", shifted, want)
	}
}

func TestShiftSRTKeepsCueSettingsAndRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "movie.srt")
	dest := filepath.Join(dir, "shifted.srt")
	if err := os.WriteFile(source, []byte("1\n00:00:10,000 --> 00:00:12,000 X1:10 X2:20\nhello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ShiftSRT(source, dest, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	shifted, _ := os.ReadFile(dest)
	if !strings.Contains(string(shifted), "00:00:05,000 --> 00:00:07,000 X1:10 X2:20") {
		t.Fatalf("shifted subtitles = %q, want the cue settings preserved", shifted)
	}

	if err := os.WriteFile(source, []byte("not subtitles\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ShiftSRT(source, dest, time.Minute); err == nil {
		t.Fatal("ShiftSRT accepted a block without a timing line")
	}
	if err := ShiftSRT(source, dest, 0); err == nil {
		t.Fatal("ShiftSRT accepted a zero offset")
	}
}

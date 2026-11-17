package whisper

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestWhisperArgsRequestOneSRTWithProgress(t *testing.T) {
	args := whisperArgs(`C:\models\ggml-small.bin`, `C:\tmp\audio.wav`, "es", `C:\tmp\subtitles`)
	for _, want := range []string{"-m", "-f", "-l", "-osrt", "-of", "-pp", "-np", "-t"} {
		if !slices.Contains(args, want) {
			t.Errorf("args = %q, missing %q", args, want)
		}
	}
	if got := valueAfter(args, "-of"); got != `C:\tmp\subtitles` {
		t.Errorf("-of = %q, want the output prefix", got)
	}
	if got := valueAfter(args, "-l"); got != "es" {
		t.Errorf("-l = %q, want the Whisper language code", got)
	}
}

func valueAfter(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestTheProgressWriterFindsProgressSplitAcrossWrites(t *testing.T) {
	var percents []int
	tail := &tailWriter{}
	writer := &progressWriter{
		report: func(percent int) { percents = append(percents, percent) },
		tail:   tail,
	}
	for _, chunk := range []string{
		"whisper_print_progress_callback: pro",
		"gress = 12%\nwhisper_print_progress_callback: progress = 47%",
		"\nwhisper_print_progress_callback: progress = 99%\n",
	} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(percents, []int{12, 47, 99}) {
		t.Fatalf("percents = %v, want 12, 47 and 99", percents)
	}
	if !strings.Contains(string(tail.buf), "progress = 99%") {
		t.Errorf("tail = %q, want whisper's output kept for errors", string(tail.buf))
	}
}

func TestTheTailWriterKeepsTheLastLines(t *testing.T) {
	tail := &tailWriter{}
	for range 2000 {
		if _, err := tail.Write([]byte("a long line of whisper output\n")); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(tail.buf); got > tailLimit {
		t.Fatalf("tail kept %d bytes, want at most %d", got, tailLimit)
	}
	if !strings.HasSuffix(string(tail.buf), "\n") {
		t.Error("tail lost the newest line")
	}
}

func TestKeepMovesTheGeneratedSRT(t *testing.T) {
	dir := t.TempDir()
	generated := filepath.Join(dir, "subtitles.srt")
	cached := filepath.Join(dir, "cached.srt")
	if err := os.WriteFile(generated, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := keep(generated, cached); err != nil {
		t.Fatalf("keep = %v", err)
	}
	if regular(generated) {
		t.Error("keep left the work copy behind")
	}
	if got, err := os.ReadFile(cached); err != nil || string(got) != "1\n" {
		t.Fatalf("cached = %q, %v", got, err)
	}
}

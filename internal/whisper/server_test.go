package whisper

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stupside/rokucastor/internal/config"
)

func TestStreamRejectsAnUnknownModelBeforeDownloading(t *testing.T) {
	_, err := Stream(t.Context(), StreamOptions{
		Layout:   config.NewAt(t.TempDir()),
		Model:    "huge",
		Language: "es",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown streaming Whisper model") {
		t.Fatalf("Stream = %v, want the model refusal", err)
	}
}

func TestServerArgsLoadTheModelWithVAD(t *testing.T) {
	got := serverArgs(`C:\models\ggml-tiny.bin`, `C:\models\silero.bin`, "127.0.0.1", 9999)
	assertArgs(t, got,
		"-m", `C:\models\ggml-tiny.bin`,
		"--vad", "-vm", `C:\models\silero.bin`,
		"-nlp",
		"--host", "127.0.0.1",
		"--port", "9999",
	)
}

func TestParseWordsMovesTokensOntoTheCastTimeline(t *testing.T) {
	payload := []byte(`{"segments":[
		{"words":[{"word":" Hola","start":0.2,"end":0.6},{"word":" mundo","start":0.7,"end":1.1},{"word":" ","start":1.1,"end":1.2}]},
		{"words":[{"word":"Adiós","start":2.0,"end":2.4}]}
	]}`)
	words, err := parseWords(payload, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(words) != 3 {
		t.Fatalf("words = %+v, want the three non-blank tokens", words)
	}
	if words[0].Text != "Hola" || words[0].Start != 10.2 || words[0].End != 10.6 {
		t.Errorf("first word = %+v, want Hola at 10.2-10.6", words[0])
	}
	if words[2].Text != "Adiós" || words[2].Start != 12.0 {
		t.Errorf("last word = %+v, want Adiós at 12.0", words[2])
	}
}

func TestWAVIsMono16BitPCMAtTheRecognizerRate(t *testing.T) {
	data := wav([]float32{0, 1, -1})
	if len(data) != 44+3*2 {
		t.Fatalf("wav length = %d", len(data))
	}
	for offset, want := range map[int]string{0: "RIFF", 8: "WAVE", 12: "fmt ", 36: "data"} {
		if got := string(data[offset : offset+4]); got != want {
			t.Errorf("header[%d] = %q, want %q", offset, got, want)
		}
	}
	if rate := binary.LittleEndian.Uint32(data[24:28]); rate != 16000 {
		t.Errorf("sample rate = %d, want 16000", rate)
	}
	if channels := binary.LittleEndian.Uint16(data[22:24]); channels != 1 {
		t.Errorf("channels = %d, want mono", channels)
	}
	if got := int16(binary.LittleEndian.Uint16(data[46:48])); got != 32767 {
		t.Errorf("sample 1.0 = %d, want 32767", got)
	}
	if got := int16(binary.LittleEndian.Uint16(data[48:50])); got != -32767 {
		t.Errorf("sample -1.0 = %d, want -32767", got)
	}
}

func assertArgs(t *testing.T, got []string, want ...string) {
	t.Helper()
	for _, item := range want {
		found := false
		for _, candidate := range got {
			if candidate == item {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("arguments %q do not contain %q", got, item)
		}
	}
}

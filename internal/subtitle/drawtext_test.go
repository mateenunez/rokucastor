package subtitle

import (
	"runtime"
	"strings"
	"testing"
)

func TestDrawtextFilterReloadsTheLiveCueFile(t *testing.T) {
	filter := DrawtextFilter(`C:\Videos\a,b;c[d]\cue.txt`)
	for _, want := range []string{
		"drawtext=textfile='C\\:/Videos/a\\,b\\;c\\[d\\]/cue.txt'",
		"reload=1",
		"box=1",
		"x=(w-text_w)/2",
	} {
		if !strings.Contains(filter, want) {
			t.Errorf("filter %q does not contain %q", filter, want)
		}
	}
	if runtime.GOOS == "windows" && !strings.Contains(filter, "fontfile=") {
		t.Errorf("filter %q has no explicit Windows font", filter)
	}
}

func TestDrawtextPathQuotesTheOptionParserAndGraphParser(t *testing.T) {
	got := drawtextPath(`C:\a b\it's[1],;.txt`)
	want := `'C\:/a b/it\'s\[1\]\,\;.txt'`
	if got != want {
		t.Fatalf("drawtextPath = %q, want %q", got, want)
	}
}

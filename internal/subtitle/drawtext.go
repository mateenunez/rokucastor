package subtitle

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DrawtextFilter renders the cue file bottom-centered over the video. reload=1
// makes ffmpeg re-read it while encoding, so committed cues appear without
// restarting the encode.
func DrawtextFilter(textFile string) string {
	options := []string{
		"drawtext=textfile=" + drawtextPath(textFile),
		"reload=1",
		"fontsize=h/24",
		"fontcolor=white",
		"borderw=2",
		"bordercolor=black",
		"box=1",
		"boxcolor=black@0.45",
		"boxborderw=10",
		"text_align=center",
		"line_spacing=6",
		"x=(w-text_w)/2",
		"y=h-text_h-(h/20)",
	}
	if runtime.GOOS == "windows" {
		// Avoid Fontconfig's missing-config crash in common Windows FFmpeg builds.
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		options = append(options, "fontfile="+drawtextPath(filepath.Join(root, "Fonts", "arial.ttf")))
	}
	return strings.Join(options, ":")
}

// drawtextPath quotes a path for ffmpeg's filter parser. The option parser
// splits on ':' and the graph parser on ',', ';', '[' and ']', and both unescape
// backslashes and quotes, so every one of those needs escaping.
func drawtextPath(path string) string {
	path = filepath.ToSlash(path)
	path = strings.NewReplacer(
		`\`, `\\`,
		`'`, `\'`,
		`:`, `\:`,
		`,`, `\,`,
		`;`, `\;`,
		`[`, `\[`,
		`]`, `\]`,
	).Replace(path)
	return "'" + path + "'"
}

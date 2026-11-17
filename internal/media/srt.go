package media

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// srtTimestamp matches "00:01:02,500 --> 00:01:04,000" and returns the whole
// timestamp pair plus its bounds, so cue settings after it can be preserved.
var srtTimestamp = regexp.MustCompile(`^(\d{1,3}):(\d{2}):(\d{2})[,.](\d{1,3})\s*-->\s*(\d{1,3}):(\d{2}):(\d{2})[,.](\d{1,3})`)

// ShiftSRT writes source to dest with every cue moved earlier by offset: cues
// that end at or before offset are dropped, and one spanning it starts at zero.
// It keeps a progressive transcode started part-way through a video in sync
// with a subtitle timed from the beginning of the original file.
func ShiftSRT(source, dest string, offset time.Duration) error {
	if offset <= 0 {
		return fmt.Errorf("shifting subtitles needs a positive offset")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("reading subtitles %q: %w", source, err)
	}
	text := strings.TrimPrefix(string(raw), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	var cues []string
	for _, block := range strings.Split(text, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		cue, kept, err := shiftCue(block, offset)
		if err != nil {
			return fmt.Errorf("shifting subtitles %q: %w", source, err)
		}
		if !kept {
			continue
		}
		cues = append(cues, fmt.Sprintf("%d\n%s", len(cues)+1, cue))
	}
	out := strings.Join(cues, "\n\n")
	if out != "" {
		out += "\n"
	}
	if err := os.WriteFile(dest, []byte(out), 0o600); err != nil {
		return fmt.Errorf("writing shifted subtitles %q: %w", dest, err)
	}
	return nil
}

// shiftCue returns one renumbered cue with its timestamps moved by offset, or
// kept=false when the cue is entirely before the offset.
func shiftCue(block string, offset time.Duration) (string, bool, error) {
	lines := strings.Split(block, "\n")
	timing := -1
	for i, line := range lines {
		if strings.Contains(line, "-->") {
			timing = i
			break
		}
	}
	if timing < 0 {
		return "", false, fmt.Errorf("cue %q has no timing line", firstLine(block))
	}
	line := strings.TrimSpace(lines[timing])
	match := srtTimestamp.FindStringSubmatchIndex(line)
	if match == nil {
		return "", false, fmt.Errorf("cue timing %q is not an SRT timestamp", line)
	}
	start, err := srtTime(line, match[2:10])
	if err != nil {
		return "", false, err
	}
	end, err := srtTime(line, match[10:18])
	if err != nil {
		return "", false, err
	}
	if end <= offset {
		return "", false, nil
	}
	if start < offset {
		start = offset
	}
	start -= offset
	end -= offset
	settings := strings.TrimSpace(line[match[1]:])

	timingLine := srtClock(start) + " --> " + srtClock(end)
	if settings != "" {
		timingLine += " " + settings
	}
	body := strings.Join(lines[timing+1:], "\n")
	if strings.TrimSpace(body) == "" {
		return timingLine, true, nil
	}
	return timingLine + "\n" + body, true, nil
}

// srtTime reads one timestamp's groups from a submatch index slice.
func srtTime(line string, groups []int) (time.Duration, error) {
	read := func(i int) int {
		n, _ := strconv.Atoi(line[groups[2*i]:groups[2*i+1]])
		return n
	}
	hours, minutes, seconds, millis := read(0), read(1), read(2), read(3)
	if minutes > 59 || seconds > 59 {
		return 0, fmt.Errorf("timestamp %q has out-of-range minutes or seconds", line)
	}
	// An SRT millisecond field is three digits; tolerate shorter ones.
	switch len(line[groups[6]:groups[7]]) {
	case 1:
		millis *= 100
	case 2:
		millis *= 10
	}
	return time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second +
		time.Duration(millis)*time.Millisecond, nil
}

func srtClock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int64(d / time.Millisecond)
	hours := total / 3_600_000
	minutes := (total % 3_600_000) / 60_000
	seconds := (total % 60_000) / 1000
	millis := total % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, seconds, millis)
}

func firstLine(block string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(block), "\n")
	return first
}

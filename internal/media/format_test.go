package media

import (
	"strings"
	"testing"
)

func TestFormatFor(t *testing.T) {
	for name, want := range map[string]Format{
		"movie.mp4":  {Stream: "mp4", MIME: "video/mp4", Ext: ".mp4"},
		"movie.MP4":  {Stream: "mp4", MIME: "video/mp4", Ext: ".mp4"},
		"movie.m4v":  {Stream: "mp4", MIME: "video/mp4", Ext: ".mp4"},
		"movie.mov":  {Stream: "mp4", MIME: "video/mp4", Ext: ".mp4"},
		"movie.mkv":  {Stream: "mkv", MIME: "video/x-matroska", Ext: ".mkv"},
		"movie.MKV":  {Stream: "mkv", MIME: "video/x-matroska", Ext: ".mkv"},
		"no-ext":     {},
		"movie.webm": {},
		"movie.avi":  {},
	} {
		if strings.HasSuffix(name, ".webm") || strings.HasSuffix(name, ".avi") || name == "no-ext" {
			if _, err := FormatFor(name); err == nil {
				t.Errorf("FormatFor(%q) accepted an unsupported format", name)
			}
			continue
		}
		got, err := FormatFor(name)
		if err != nil {
			t.Errorf("FormatFor(%q) error = %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("FormatFor(%q) = %+v, want %+v", name, got, want)
		}
	}
}

func TestLanguageCodeReachesISO6392B(t *testing.T) {
	for tag, want := range map[string]string{
		"es":   "spa",
		"ES":   "spa",
		" en ": "eng",
		"spa":  "spa",
		"jpn":  "jpn",
		"deu":  "ger", // 639-2/T in, 639-2/B out
		"ger":  "ger",
		"fra":  "fre",
		"zho":  "chi",
		"pt":   "por",
	} {
		got, err := LanguageCode(tag)
		if err != nil || got != want {
			t.Errorf("LanguageCode(%q) = %q, %v; want %q", tag, got, err, want)
		}
	}
}

func TestLanguageCodeRejectsUnknownCodes(t *testing.T) {
	for _, tag := range []string{"", "xx", "xyz", "español", "e"} {
		if _, err := LanguageCode(tag); err == nil {
			t.Errorf("LanguageCode(%q) accepted an unknown language", tag)
		}
	}
}

func TestWhisperCodeReachesISO6391(t *testing.T) {
	for tag, want := range map[string]string{
		"es":  "es",
		"ES":  "es",
		"spa": "es",
		"ger": "de",
		"deu": "de",
		"jpn": "ja",
		"pt":  "pt",
		"en":  "en",
	} {
		got, err := WhisperCode(tag)
		if err != nil || got != want {
			t.Errorf("WhisperCode(%q) = %q, %v; want %q", tag, got, err, want)
		}
	}
}

func TestWhisperCodeRejectsUnknownCodes(t *testing.T) {
	for _, tag := range []string{"", "xx", "xyz", "español", "e"} {
		if _, err := WhisperCode(tag); err == nil {
			t.Errorf("WhisperCode(%q) accepted an unknown language", tag)
		}
	}
}

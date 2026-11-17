// Package media publishes local videos and their subtitles to a Roku on the
// local network.
package media

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Format is how a local file reaches the Roku.
type Format struct {
	// Stream is the Roku streamFormat value: "mp4" or "mkv".
	Stream string
	// MIME is the Content-Type the server answers with.
	MIME string
	// Ext is the extension used in the published URL.
	Ext string
}

// FormatFor maps a local video file to the container Roku plays it as.
func FormatFor(path string) (Format, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".m4v", ".mov":
		return Format{Stream: "mp4", MIME: "video/mp4", Ext: ".mp4"}, nil
	case ".mkv":
		return Format{Stream: "mkv", MIME: "video/x-matroska", Ext: ".mkv"}, nil
	}
	return Format{}, fmt.Errorf("unsupported video format %q: rokucastor plays MP4, M4V, MOV and MKV files", filepath.Ext(path))
}

// SubtitleMIME is the Content-Type of the SRT side-loaded tracks.
const SubtitleMIME = "application/x-subrip; charset=utf-8"

// languageCodes maps ISO 639-1 codes, which Whisper uses, to the ISO 639-2/B
// codes Roku expects in a subtitle track.
var languageCodes = map[string]string{
	"af": "afr", "am": "amh", "ar": "ara", "as": "asm", "az": "aze",
	"ba": "bak", "be": "bel", "bg": "bul", "bn": "ben", "bo": "tib",
	"br": "bre", "bs": "bos", "ca": "cat", "cs": "cze", "cy": "wel",
	"da": "dan", "de": "ger", "el": "gre", "en": "eng", "es": "spa",
	"et": "est", "eu": "baq", "fa": "per", "fi": "fin", "fo": "fao",
	"fr": "fre", "gl": "glg", "gu": "guj", "ha": "hau", "haw": "haw",
	"he": "heb", "hi": "hin", "hr": "hrv", "ht": "hat", "hu": "hun",
	"hy": "arm", "id": "ind", "is": "ice", "it": "ita", "ja": "jpn",
	"jw": "jav", "ka": "geo", "kk": "kaz", "km": "khm", "kn": "kan",
	"ko": "kor", "la": "lat", "lb": "ltz", "ln": "lin", "lo": "lao",
	"lt": "lit", "lv": "lav", "mg": "mlg", "mi": "mao", "mk": "mac",
	"ml": "mal", "mn": "mon", "mr": "mar", "ms": "may", "mt": "mlt",
	"my": "bur", "ne": "nep", "nl": "dut", "nn": "nno", "no": "nor",
	"oc": "oci", "pa": "pan", "pl": "pol", "ps": "pus", "pt": "por",
	"ro": "rum", "ru": "rus", "sa": "san", "sd": "snd", "si": "sin",
	"sk": "slo", "sl": "slv", "sn": "sna", "so": "som", "sq": "alb",
	"sr": "srp", "su": "sun", "sv": "swe", "sw": "swa", "ta": "tam",
	"te": "tel", "tg": "tgk", "th": "tha", "tk": "tuk", "tl": "tgl",
	"tr": "tur", "tt": "tat", "uk": "ukr", "ur": "urd", "uz": "uzb",
	"vi": "vie", "yi": "yid", "yo": "yor", "zh": "chi",
}

// languageTerminology maps the ISO 639-2/T variants, which some tools emit, to
// the 639-2/B code Roku documents.
var languageTerminology = map[string]string{
	"bod": "tib", "ces": "cze", "cym": "wel", "deu": "ger", "ell": "gre",
	"eus": "baq", "fas": "per", "fra": "fre", "hye": "arm", "isl": "ice",
	"kat": "geo", "mkd": "mac", "mri": "mao", "msa": "may", "mya": "bur",
	"nld": "dut", "ron": "rum", "slk": "slo", "sqi": "alb", "zho": "chi",
}

// LanguageCode reduces a two- or three-letter language tag to the ISO 639-2/B
// code a Roku subtitle track needs.
func LanguageCode(tag string) (string, error) {
	code := strings.ToLower(strings.TrimSpace(tag))
	if len(code) == 3 {
		if b, ok := languageTerminology[code]; ok {
			return b, nil
		}
		if _, ok := languageB[code]; ok {
			return code, nil
		}
	}
	if b, ok := languageCodes[code]; ok {
		return b, nil
	}
	return "", fmt.Errorf("unknown subtitle language %q: use an ISO 639-1 code such as es, or an ISO 639-2 code such as spa", tag)
}

// languageB is every 639-2/B code languageCodes can produce.
var languageB = func() map[string]struct{} {
	codes := make(map[string]struct{}, len(languageCodes)+len(languageTerminology))
	for _, code := range languageCodes {
		codes[code] = struct{}{}
	}
	for _, code := range languageTerminology {
		codes[code] = struct{}{}
	}
	return codes
}()

// whisperLanguages maps every accepted 639-2/B or 639-2/T code back to the ISO
// 639-1 code whisper.cpp knows a language by.
var whisperLanguages = func() map[string]string {
	codes := make(map[string]string, len(languageCodes)+len(languageTerminology))
	for one, b := range languageCodes {
		codes[b] = one
	}
	for t, b := range languageTerminology {
		if one, ok := codes[b]; ok {
			codes[t] = one
		}
	}
	return codes
}()

// WhisperCode reduces a two- or three-letter language tag to the ISO 639-1 code
// whisper.cpp takes for -l.
func WhisperCode(tag string) (string, error) {
	code := strings.ToLower(strings.TrimSpace(tag))
	if _, ok := languageCodes[code]; ok {
		return code, nil
	}
	if b, err := LanguageCode(code); err == nil {
		if one, ok := whisperLanguages[b]; ok {
			return one, nil
		}
	}
	return "", fmt.Errorf("unknown subtitle language %q: use an ISO 639-1 code such as es, or an ISO 639-2 code such as spa", tag)
}

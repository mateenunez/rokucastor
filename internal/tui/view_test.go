package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"

	"github.com/stupside/rokucastor/internal/cast"
	"github.com/stupside/rokucastor/internal/device"
)

func rendered(t *testing.T, m model) string {
	t.Helper()
	m.w, m.h = 80, 24
	m.resize()
	return m.content()
}

func TestEveryScreenRenders(t *testing.T) {
	m := testModel(t, Options{})
	m.scr = screenDevices

	m.discovering = true
	if got := rendered(t, m); !strings.Contains(got, "Searching") {
		t.Errorf("discovery screen = %q, want the search message", got)
	}

	m.discovering = false
	m.devices.SetItems([]list.Item{deviceItem{device.Info{Name: "Living Room", Address: "http://192.0.2.1:8060"}}})
	m.devices.Select(0)
	if got := rendered(t, m); !strings.Contains(got, "Living Room") {
		t.Errorf("device screen = %q, want the device name", got)
	}

	m.scr = screenFolder
	if got := rendered(t, m); !strings.Contains(got, "Enter the folder") {
		t.Errorf("folder screen = %q, want the folder prompt", got)
	}

	m.scr = screenFiles
	m.currentDir = `C:\Videos`
	m.files.SetItems([]list.Item{entry{name: "Movie.mp4", path: `C:\Videos\Movie.mp4`, size: 2 << 20}})
	if got := rendered(t, m); !strings.Contains(got, "Movie.mp4") {
		t.Errorf("file screen = %q, want the video", got)
	}

	m.scr = screenSubtitles
	m.video = `C:\Videos\Movie.mp4`
	m.subs.SetItems([]list.Item{subtitleChoice{label: "No subtitles"}})
	if got := rendered(t, m); !strings.Contains(got, "Subtitles") {
		t.Errorf("subtitle screen = %q, want the prompt", got)
	}

	m.scr = screenCasting
	m.cast = castState{
		phase:    castPlaying,
		video:    `C:\Videos\Movie.mp4`,
		target:   "Living Room",
		language: "es",
		playhead: device.Playhead{At: 30 * time.Second, Duration: 2 * time.Minute},
	}
	got := rendered(t, m)
	for _, want := range []string{"playing", "0:30 / 2:00", "25%"} {
		if !strings.Contains(got, want) {
			t.Errorf("casting screen = %q, want %q", got, want)
		}
	}

	m.cast.transcode = &cast.TranscodeInfo{
		Encoder:         "h264_amf",
		VideoCodec:      "h264",
		VideoWidth:      1920,
		VideoHeight:     802,
		AudioCodec:      "aac",
		AudioChannels:   2,
		AudioSampleRate: 48000,
	}
	got = rendered(t, m)
	for _, want := range []string{"Transcoding:", "H264 1920x802", "AAC 2ch 48kHz", "h264_amf", "0:30"} {
		if !strings.Contains(got, want) {
			t.Errorf("transcoded casting screen = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "25%") || strings.Contains(got, "0:30 / 2:00") {
		t.Errorf("transcoded casting screen includes unstable playback progress: %q", got)
	}

	m.cast.generate = true
	if got := rendered(t, m); !strings.Contains(got, "generated live by Whisper") {
		t.Errorf("generated-subtitles screen = %q, want the live caption line", got)
	}
	m.cast.generate = false

	m.cast.phase = castFailed
	m.cast.err = errPlayback
	if got := rendered(t, m); !strings.Contains(got, "Playback failed") {
		t.Errorf("failed screen = %q, want the failure", got)
	}

	m.scr = screenFinished
	m.cast.phase = castFinished
	if got := rendered(t, m); !strings.Contains(got, "Finished on Living Room") {
		t.Errorf("finished screen = %q, want the summary", got)
	}
}

var errPlayback = &staticError{"decoder refused"}

type staticError struct{ message string }

func (e *staticError) Error() string { return e.message }

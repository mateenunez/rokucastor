package roku

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/rokucastor/internal/device"
)

func TestTheChannelIsLaunchedWithTheFormatOfWhatItPlays(t *testing.T) {
	launched := make(chan url.Values, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/launch/dev" {
			launched <- r.URL.Query()
		}
	}))
	defer ts.Close()
	ecp, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	dev := &Session{ecp: ecp, appID: "dev", name: "Bedroom Roku", hc: ts.Client()}
	stream := &url.URL{Scheme: "http", Host: "192.0.2.1:8080", Path: "/stream"}
	for _, format := range []string{"mp4", "mkv", "hls"} {
		if err := dev.Play(t.Context(), device.Playback{URL: stream, Format: format}); err != nil {
			t.Fatal(err)
		}
		if got := <-launched; got.Get("format") != format || got.Get("url") != stream.String() {
			t.Errorf("playing %q launched the channel with %v, want the stream and its format", format, got)
		}
	}
}

func TestTheChannelIsLaunchedWithItsSideLoadedSubtitle(t *testing.T) {
	launched := make(chan url.Values, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/launch/dev" {
			launched <- r.URL.Query()
		}
	}))
	defer ts.Close()
	ecp, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	dev := &Session{ecp: ecp, appID: "dev", name: "Bedroom Roku", hc: ts.Client()}
	subtitle := &url.URL{Scheme: "http", Host: "192.0.2.1:8080", Path: "/s/token.srt"}

	err = dev.Play(t.Context(), device.Playback{
		URL:                 &url.URL{Scheme: "http", Host: "192.0.2.1:8080", Path: "/v/token.mp4"},
		Format:              "mp4",
		SubtitleURL:         subtitle,
		SubtitleLanguage:    "spa",
		SubtitleDescription: "Subtítulos (es)",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := <-launched
	if got.Get("subtitle") != subtitle.String() || got.Get("subtitleLang") != "spa" || got.Get("subtitleDescription") != "Subtítulos (es)" {
		t.Errorf("launch parameters = %v, want the subtitle track and its language", got)
	}
}

func TestTheChannelIsLaunchedAtTheRequestedPosition(t *testing.T) {
	launched := make(chan url.Values, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/launch/dev" {
			launched <- r.URL.Query()
		}
	}))
	defer ts.Close()
	ecp, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	dev := &Session{ecp: ecp, appID: "dev", name: "Bedroom Roku", hc: ts.Client()}
	stream := &url.URL{Scheme: "http", Host: "192.0.2.1:8080", Path: "/stream"}
	if err := dev.Play(t.Context(), device.Playback{URL: stream, Format: "mp4", Start: time.Hour + 12*time.Minute + 30*time.Second}); err != nil {
		t.Fatal(err)
	}
	if got := <-launched; got.Get("start") != "4350.000" {
		t.Errorf("launch parameters = %v, want a start at 1:12:30", got)
	}

	if err := dev.Play(t.Context(), device.Playback{URL: stream, Format: "mp4"}); err != nil {
		t.Fatal(err)
	}
	if got := <-launched; got.Has("start") {
		t.Errorf("launch parameters = %v, want no start from the beginning", got)
	}
}

func TestUnsupportedFormatsDoNotLaunchTheRokuChannel(t *testing.T) {
	// No client is needed: an unsupported format must fail before ECP.
	dev := &Session{}
	for _, format := range []string{"", "ts", "avi"} {
		if err := dev.Play(t.Context(), device.Playback{URL: &url.URL{Scheme: "http", Host: "media.test"}, Format: format}); err == nil {
			t.Errorf("Play accepted unsupported format %q", format)
		}
	}
}

func TestPlayRequiresAURL(t *testing.T) {
	if err := (&Session{}).Play(t.Context(), device.Playback{Format: "mp4"}); err == nil {
		t.Fatal("Play accepted a playback without a URL")
	}
}

// refusingAfter answers the media-player query as playing its first polls, then refuses every one, as a Roku unplugged would.
type refusingAfter struct{ answers, polls atomic.Int32 }

func (e *refusingAfter) RoundTrip(*http.Request) (*http.Response, error) {
	if e.polls.Add(1) > e.answers.Load() {
		return nil, errors.New("connect: connection refused")
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`<player error="false" state="play"/>`))}, nil
}

func TestARokuThatStopsAnsweringIsNamedGone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ecp := &refusingAfter{}
		ecp.answers.Store(2)
		dev := &Session{ecp: &url.URL{Scheme: "http", Host: "192.0.2.10:8060"}, name: "Bedroom Roku", hc: &http.Client{Transport: ecp}}
		away, gone := errors.AsType[*device.Gone](dev.AwaitEnd(t.Context()))
		if !gone {
			t.Fatal("AwaitEnd did not name the Roku gone once it stopped answering")
		}
		if away.Device != "Bedroom Roku" || away.Err == nil {
			t.Errorf("gone = %+v, want the device named and the last failure carried", away)
		}
	})
}

// scriptedECP answers each media-player query with the next state, "state@channel" naming the channel holding the player and a leading "!" a failed one, the last one repeating, and records the keys pressed.
type scriptedECP struct {
	states []string
	polls  int
	keys   []string
}

func (e *scriptedECP) RoundTrip(r *http.Request) (*http.Response, error) {
	if key, ok := strings.CutPrefix(r.URL.Path, "/keypress/"); ok {
		e.keys = append(e.keys, key)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	}
	state, channel, held := strings.Cut(e.states[min(e.polls, len(e.states)-1)], "@")
	state, failed := strings.CutPrefix(state, "!")
	e.polls++
	plugin := ""
	if held {
		plugin = `<plugin id="` + channel + `"/>`
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`<player error="` + strconv.FormatBool(failed) + `" state="` + state + `">` + plugin + `</player>`))}, nil
}

func scriptedSession(ecp *scriptedECP) *Session {
	return &Session{ecp: &url.URL{Scheme: "http", Host: "192.0.2.10:8060"}, appID: "dev", name: "Bedroom Roku", hc: &http.Client{Transport: ecp}}
}

func TestRokuEndsWhenThePlayerClosesAfterPlaying(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ecp := &scriptedECP{states: []string{"none", "startup", "buffer", "play", "pause", "play", "close"}}
		if err := scriptedSession(ecp).AwaitEnd(t.Context()); err != nil {
			t.Fatalf("AwaitEnd = %v, want a clean end", err)
		}
		if ecp.polls != len(ecp.states) {
			t.Errorf("polled %d times, want %d: a player that has not played yet is not over", ecp.polls, len(ecp.states))
		}
	})
}

func TestRokuEndsCleanlyWhenAnotherChannelTakesThePlayer(t *testing.T) {
	for _, tt := range []struct {
		name   string
		states []string
	}{
		{"playing", []string{"play@dev", "play@dev", "play@12"}},
		{"failing", []string{"play@dev", "!stop@12"}},
		{"still held by the previous channel at startup", []string{"play@12", "startup@dev", "play@dev", "close@dev"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ecp := &scriptedECP{states: tt.states}
				if err := scriptedSession(ecp).AwaitEnd(t.Context()); err != nil {
					t.Fatalf("AwaitEnd = %v, want a clean end", err)
				}
				if ecp.polls != len(ecp.states) {
					t.Errorf("polled %d times, want %d: the cast is over once another channel holds the player after ours played", ecp.polls, len(ecp.states))
				}
			})
		})
	}
}

func TestRokuPauseAndResumePressPlayOnlyWhenTheStateCallsForIt(t *testing.T) {
	for _, tt := range []struct {
		name  string
		state string
		do    func(*Session) error
		keys  int
		fails bool
	}{
		{"pausing a playing player", "play", func(s *Session) error { return s.Pause(context.Background()) }, 1, false},
		{"pausing a paused player", "pause", func(s *Session) error { return s.Pause(context.Background()) }, 0, false},
		{"resuming a paused player", "pause", func(s *Session) error { return s.Resume(context.Background()) }, 1, false},
		{"resuming a playing player", "play", func(s *Session) error { return s.Resume(context.Background()) }, 0, false},
		{"pausing a closed player", "close", func(s *Session) error { return s.Pause(context.Background()) }, 0, true},
		{"resuming another channel's paused player", "pause@12", func(s *Session) error { return s.Resume(context.Background()) }, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ecp := &scriptedECP{states: []string{tt.state}}
			err := tt.do(scriptedSession(ecp))
			if (err != nil) != tt.fails || len(ecp.keys) != tt.keys {
				t.Errorf("err = %v, keys = %v, want %d key(s) and failure %v", err, ecp.keys, tt.keys, tt.fails)
			}
		})
	}
}

func TestRokuStopLeavesForHome(t *testing.T) {
	ecp := &scriptedECP{states: []string{"play"}}
	if err := scriptedSession(ecp).Stop(t.Context()); err != nil || len(ecp.keys) != 1 || ecp.keys[0] != "Home" {
		t.Errorf("Stop = %v with keys %v, want Home", err, ecp.keys)
	}
}

func TestRokuCloseLeavesForHome(t *testing.T) {
	ecp := &scriptedECP{states: []string{"play"}}
	if err := scriptedSession(ecp).Close(); err != nil || len(ecp.keys) != 1 || ecp.keys[0] != "Home" {
		t.Errorf("Close = %v with keys %v, want Home", err, ecp.keys)
	}
}

func TestRokuStartupHasDeadline(t *testing.T) {
	for _, state := range []string{"none", "startup", "buffer"} {
		t.Run(state, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ecp := &scriptedECP{states: []string{state}}
				start := time.Now()
				err := scriptedSession(ecp).AwaitEnd(t.Context())
				if _, ok := errors.AsType[*device.PlaybackError](err); !ok {
					t.Fatalf("AwaitEnd=%v", err)
				}
				if time.Since(start) > DefaultStartupTimeout+device.PollInterval {
					t.Fatal("startup exceeded deadline")
				}
			})
		})
	}
}

func TestRokuStartupTimeoutIsConfigurable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ecp := &scriptedECP{states: []string{"startup"}}
		dev := scriptedSession(ecp)
		dev.startup = 6 * time.Second

		start := time.Now()
		err := dev.AwaitEnd(t.Context())
		if _, ok := errors.AsType[*device.PlaybackError](err); !ok {
			t.Fatalf("AwaitEnd=%v", err)
		}
		elapsed := time.Since(start)
		if elapsed < dev.startup || elapsed > dev.startup+device.PollInterval {
			t.Fatalf("startup waited %s, want about %s", elapsed, dev.startup)
		}
		if !strings.Contains(err.Error(), "6s") {
			t.Errorf("error %q does not name the configured timeout", err)
		}
	})
}

type decoderErrorECP struct{}

func (decoderErrorECP) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`<player error="true" state="stop"><error-code>42</error-code><error-message>unsupported stream</error-message></player>`))}, nil
}

func TestRokuDecoderErrorIsTerminal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dev := &Session{ecp: &url.URL{Scheme: "http", Host: "roku.test"}, hc: &http.Client{Transport: decoderErrorECP{}}}
		start := time.Now()
		err := dev.AwaitEnd(t.Context())
		if err == nil || !strings.Contains(err.Error(), "unsupported stream") || !strings.Contains(err.Error(), "42") {
			t.Fatalf("AwaitEnd=%v", err)
		}
		if time.Since(start) != device.PollInterval {
			t.Fatal("decoder refusal retried as network failure")
		}
	})
}

type answeringECP string

func (body answeringECP) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}

func TestRokuPositionIsThePlayersWhileItHoldsTheMedia(t *testing.T) {
	for _, tt := range []struct {
		name, player string
		want         device.Playhead
		err          error
	}{
		{"playing", `<player error="false" state="play"><plugin id="dev"/><position>12345 ms</position><duration>600000 ms</duration></player>`, device.Playhead{At: 12345 * time.Millisecond, Duration: 10 * time.Minute}, nil},
		{"paused", `<player error="false" state="pause"><position>5000 ms</position></player>`, device.Playhead{At: 5 * time.Second, Paused: true}, nil},
		{"no position", `<player error="false" state="play"/>`, device.Playhead{}, device.ErrNoPosition},
		{"an unreadable position", `<player error="false" state="play"><position>soon</position></player>`, device.Playhead{}, device.ErrNoPosition},
		{"an unreadable duration", `<player error="false" state="play"><position>5000 ms</position><duration>soon</duration></player>`, device.Playhead{At: 5 * time.Second}, nil},
		{"closed", `<player error="false" state="close"><position>5000 ms</position></player>`, device.Playhead{}, device.ErrNoPosition},
		{"failed", `<player error="true" state="stop"><position>5000 ms</position></player>`, device.Playhead{}, device.ErrNoPosition},
		{"another channel's", `<player error="false" state="play"><plugin id="12"/><position>5000 ms</position></player>`, device.Playhead{}, device.ErrNoPosition},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dev := &Session{ecp: &url.URL{Scheme: "http", Host: "roku.test"}, appID: "dev", hc: &http.Client{Transport: answeringECP(tt.player)}}
			if got, err := dev.Position(t.Context()); got != tt.want || !errors.Is(err, tt.err) {
				t.Errorf("Position = %v, %v; want %v, %v", got, err, tt.want, tt.err)
			}
		})
	}
}

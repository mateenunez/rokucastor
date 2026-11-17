package roku

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/stupside/rokucastor/internal/device"
)

// Session is a Roku driven over ECP, playing through the channel it launches.
type Session struct {
	ecp     *url.URL
	appID   string
	name    string
	hc      *http.Client
	startup time.Duration
}

var _ device.Device = (*Session)(nil)

// Name is the owner-set name read at connect time.
func (s *Session) Name() string { return s.name }

// get reads at most limit bytes of an ECP answer, refusing any status but 200.
func get(ctx context.Context, hc *http.Client, u *url.URL, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", u.Path, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// Play launches the channel with the media URL, format and optional subtitle
// track. Roku reads launch parameters from the channel's args.
func (s *Session) Play(ctx context.Context, playback device.Playback) error {
	if playback.URL == nil {
		return errors.New("no media URL to play")
	}
	switch playback.Format {
	case "mp4", "mkv", "hls":
	default:
		return fmt.Errorf("roku does not support stream format %q", playback.Format)
	}

	q := url.Values{}
	q.Set(paramURL, playback.URL.String())
	q.Set(paramFormat, playback.Format)
	if playback.Start > 0 {
		q.Set(paramStart, strconv.FormatFloat(playback.Start.Seconds(), 'f', 3, 64))
	}
	if playback.SubtitleURL != nil {
		q.Set(paramSubtitle, playback.SubtitleURL.String())
		q.Set(paramSubtitleLang, playback.SubtitleLanguage)
		q.Set(paramSubtitleDescription, playback.SubtitleDescription)
	}
	u := s.ecp.JoinPath("launch", s.appID)
	u.RawQuery = q.Encode()
	slog.InfoContext(ctx, "launching Roku playback",
		"device", s.name,
		"app_id", s.appID,
		"format", playback.Format,
		"start", playback.Start,
		"subtitles", playback.SubtitleURL != nil,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		// The launch URL contains the tokenized media URL, which may be
		// included in the underlying transport error.
		slog.ErrorContext(ctx, "Roku launch request failed", "device", s.name, "error_type", fmt.Sprintf("%T", err))
		return fmt.Errorf("launching roku channel: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		slog.ErrorContext(ctx, "Roku rejected playback launch", "device", s.name, "status", resp.Status)
		return fmt.Errorf("roku launch: %s (channel %q installed?)", resp.Status, s.appID)
	}
	slog.InfoContext(ctx, "Roku playback launch accepted", "device", s.name, "status", resp.Status)
	return nil
}

const statePause = "pause"

func playing(state string) bool { return state == "play" || state == "buffer" || state == "startup" }

func over(state string) bool { return state == "close" || state == "stop" || state == "none" }

// Pause holds the player paused; ECP has one Play key for both, so it presses
// it only when the player is playing.
func (s *Session) Pause(ctx context.Context) error { return s.hold(ctx, true) }

// Resume releases a paused player.
func (s *Session) Resume(ctx context.Context) error { return s.hold(ctx, false) }

func (s *Session) hold(ctx context.Context, paused bool) error {
	player, err := s.player(ctx)
	if err != nil {
		return err
	}
	state := player.State
	switch {
	case paused && state == statePause, !paused && playing(state):
		return nil
	case paused && playing(state), !paused && state == statePause:
		return s.keypress(ctx, "Play")
	}
	return fmt.Errorf("roku player is %q, with nothing to pause or resume", state)
}

func (s *Session) keypress(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.ecp.JoinPath("keypress", key).String(), nil)
	if err != nil {
		return err
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("roku keypress %s: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("roku keypress %s: %s", key, resp.Status)
	}
	return nil
}

// Stop leaves the channel for the home screen, which ends its playback.
func (s *Session) Stop(ctx context.Context) error { return s.keypress(ctx, "Home") }

// DefaultStartupTimeout is how long a Roku may stay in startup or buffering
// before the cast is considered failed. A large file or a slow network needs
// longer than the default.
const DefaultStartupTimeout = 30 * time.Second

// startupTimeout is the configured wait, or the default when none was set.
func (s *Session) startupTimeout() time.Duration {
	if s.startup <= 0 {
		return DefaultStartupTimeout
	}
	return s.startup
}

const mediaPlayerQuery = "/query/media-player"

type mediaPlayer struct {
	State        string `xml:"state,attr"`
	Error        bool   `xml:"error,attr"`
	ErrorMessage string `xml:"error-message"`
	ErrorCode    string `xml:"error-code"`
	Position     string `xml:"position"`
	Duration     string `xml:"duration"`
	Plugin       struct {
		ID string `xml:"id,attr"`
	} `xml:"plugin"`
}

// player is the Roku media player as reported, a playback error once it
// reports one.
func (s *Session) player(ctx context.Context) (mediaPlayer, error) {
	var player mediaPlayer
	body, err := get(ctx, s.hc, s.ecp.JoinPath(mediaPlayerQuery), 1<<16)
	if err != nil {
		return player, err
	}
	if err := xml.Unmarshal(body, &player); err != nil {
		return player, fmt.Errorf("decoding roku player state: %w", err)
	}
	// Another channel holding the player means ours is closed, whatever that
	// channel's own playback reports.
	if player.Plugin.ID != "" && player.Plugin.ID != s.appID {
		return mediaPlayer{State: "close"}, nil
	}
	if player.Error {
		return player, &device.PlaybackError{Err: fmt.Errorf("roku playback failed (code %s): %s", player.ErrorCode, player.ErrorMessage)}
	}
	return player, nil
}

// Position is the player's, stated in milliseconds, while it holds our media.
func (s *Session) Position(ctx context.Context) (device.Playhead, error) {
	player, err := s.player(ctx)
	if _, failed := errors.AsType[*device.PlaybackError](err); failed {
		return device.Playhead{}, device.ErrNoPosition
	}
	if err != nil {
		return device.Playhead{}, fmt.Errorf("reading the playback position: %w", err)
	}
	at, ok := parseMillis(player.Position)
	if !ok || over(player.State) {
		return device.Playhead{}, device.ErrNoPosition
	}
	// A player without a reported length still tells where it is; the caller
	// shows elapsed time instead of a bar.
	duration, _ := parseMillis(player.Duration)
	return device.Playhead{At: at, Duration: duration, Paused: player.State == statePause}, nil
}

// parseMillis reads ECP's "<n> ms" durations and positions.
func parseMillis(value string) (time.Duration, bool) {
	ms, unit := strings.CutSuffix(strings.TrimSpace(value), " ms")
	if !unit {
		return 0, false
	}
	n, err := strconv.ParseUint(ms, 10, 63)
	if err != nil {
		return 0, false
	}
	return time.Duration(n) * time.Millisecond, true
}

// AwaitEnd polls the player until it closes or stops after having played.
func (s *Session) AwaitEnd(ctx context.Context) error {
	var active bool
	started := time.Now()
	lastState := ""
	lastStatus := time.Time{}
	slog.InfoContext(ctx, "waiting for Roku playback", "device", s.name, "startup_timeout", s.startupTimeout())
	return device.AwaitPolledEnd(ctx, s.name, mediaPlayerQuery, func(ctx context.Context) (bool, error) {
		player, err := s.player(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "reading Roku playback state failed", "device", s.name, "error", err)
			return false, err
		}
		state := player.State
		now := time.Now()
		if state != lastState || lastStatus.IsZero() || now.Sub(lastStatus) >= 15*time.Second {
			message := "Roku playback status"
			if state != lastState {
				message = "Roku playback state changed"
			}
			slog.InfoContext(ctx, message,
				"device", s.name,
				"from", lastState,
				"to", state,
				"position", player.Position,
				"duration", player.Duration,
			)
			lastState = state
			lastStatus = now
		}
		active = active || state == "play" || state == statePause
		if timeout := s.startupTimeout(); !active && time.Since(started) >= timeout {
			err := fmt.Errorf("roku did not start playback within %s (state %q); raise --startup-timeout if the file is large or the network is slow", timeout, state)
			slog.ErrorContext(ctx, "Roku startup timed out", "device", s.name, "state", state, "elapsed", time.Since(started))
			return false, &device.PlaybackError{Err: err}
		}
		return active && over(state), nil
	})
}

const stopTimeout = 5 * time.Second

// Close sends the Roku home, ending playback. A Roku already gone reports the
// failure, but nothing here retries: the command is finishing either way.
func (s *Session) Close() error {
	stopping, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	return s.Stop(stopping)
}

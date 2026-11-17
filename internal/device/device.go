// Package device defines what a connected player must do, independent of the
// protocol that reaches it.
package device

import (
	"context"
	"errors"
	"net/url"
	"time"
)

// Info names one discovered device and where it answers.
type Info struct {
	// ID is the device's own identity, unique within its family.
	ID string
	// Name is the owner-set name, or the address when the device has none.
	Name string
	// Address is the URL a family connects to.
	Address string
}

// Playback is what to play, already reachable by the device.
type Playback struct {
	URL    *url.URL
	Format string // Roku streamFormat: "mp4", "mkv" or "hls"
	// Start is where playback begins within the media's own timeline. Media
	// whose timeline already starts at zero (a progressive transcode) leaves
	// it zero.
	Start time.Duration

	// SubtitleURL is a side-loaded SRT the device must fetch itself.
	SubtitleURL *url.URL
	// SubtitleLanguage is the ISO 639-2/B code Roku expects, such as "spa".
	SubtitleLanguage string
	// SubtitleDescription is what the Roku caption menu shows.
	SubtitleDescription string
}

// Device is a connected player; whoever connected it closes it.
type Device interface {
	Name() string

	// Play points the device at the requested media.
	Play(ctx context.Context, playback Playback) error

	// AwaitEnd polls until playback ends, fails, or ctx is canceled.
	AwaitEnd(ctx context.Context) error

	// Pause and Resume hold and release the picture.
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error

	// Stop ends what the device plays.
	Stop(ctx context.Context) error

	// Position is where the device says it has played to, ErrNoPosition
	// where it cannot say.
	Position(ctx context.Context) (Playhead, error)

	Close() error
}

// ErrNoPosition says the device cannot report a playhead right now.
var ErrNoPosition = errors.New("the device does not report its position")

// Playhead is a playback position, how long the media runs, and whether
// playback is paused there.
type Playhead struct {
	At time.Duration
	// Duration is the media's full length, or zero when the player does not
	// report one.
	Duration time.Duration
	Paused   bool
}

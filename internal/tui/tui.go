// Package tui is rokucastor's terminal interface: it finds Rokus, browses local
// video files, and shows playback while a cast runs.
package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stupside/rokucastor/internal/cast"
	"github.com/stupside/rokucastor/internal/config"
	"github.com/stupside/rokucastor/internal/whisper"
)

// Options configures one TUI run.
type Options struct {
	// Device pins a Roku address, skipping discovery; empty searches.
	Device string
	// Password enables installing or updating rokucastor's dev channel.
	Password string
	// StartupTimeout is how long the Roku may stay in startup or buffering
	// before the cast fails; zero uses roku.DefaultStartupTimeout.
	StartupTimeout time.Duration
	// AppID launches an already installed channel instead of sideloading.
	AppID string
	// Language is the subtitle language tag, as ISO 639-1 or ISO 639-2.
	Language string
	// Model is the Whisper model used to generate cached SRT files; empty
	// uses the package default.
	Model string
	// StreamModel is the Whisper model used for live burn-in captions; empty
	// uses the package default.
	StreamModel string
	// Layout is the per-user directory tree; empty resolves the current
	// user's directories.
	Layout config.Layout
}

// Run starts the interface and blocks until the operator leaves.
func Run(ctx context.Context, opts Options) error {
	if opts.Layout.Root == "" {
		layout, err := config.Paths()
		if err != nil {
			return err
		}
		opts.Layout = layout
	}
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = whisper.DefaultModel
	}
	if !whisper.ValidModel(opts.Model) {
		return fmt.Errorf("unknown Whisper model %q: use %q, %q, %q or %q", opts.Model, whisper.ModelTiny, whisper.ModelTinyEN, whisper.ModelBase, whisper.ModelSmall)
	}
	if strings.TrimSpace(opts.StreamModel) == "" {
		opts.StreamModel = whisper.DefaultStreamModel
	}
	if !whisper.ValidModel(opts.StreamModel) {
		return fmt.Errorf("unknown streaming Whisper model %q: use %q, %q, %q or %q", opts.StreamModel, whisper.ModelTiny, whisper.ModelTinyEN, whisper.ModelBase, whisper.ModelSmall)
	}
	res := &resources{}
	program := tea.NewProgram(newModel(ctx, opts, res), tea.WithContext(ctx))
	_, err := program.Run()
	// The model may have been mid-cast when the operator quit; leave the Roku
	// on its home screen and stop serving in every case.
	res.closeAll()
	return err
}

// resources tracks every cast's player and server so a quit in any screen can
// still release them. Each cast gets its own resource: a finished cast can be
// cleaned up while the next one prepares.
type resources struct {
	mu   sync.Mutex
	open []*resource
}

func (r *resources) new() *resource {
	res := &resource{}
	r.mu.Lock()
	r.open = append(r.open, res)
	r.mu.Unlock()
	return res
}

func (r *resources) closeAll() {
	r.mu.Lock()
	open := slices.Clone(r.open)
	r.mu.Unlock()
	for _, res := range open {
		_ = res.close()
	}
}

// resource holds one cast's session, adopted once the session is ready.
type resource struct {
	mu      sync.Mutex
	session *cast.Session
	closed  bool
}

func (r *resource) adopt(session *cast.Session) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.session = session
	return true
}

// close releases the session; it is safe to call more than once.
func (r *resource) close() error {
	r.mu.Lock()
	r.closed = true
	session := r.session
	r.session = nil
	r.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.Close()
}

package tui

import (
	"context"
	"net/url"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stupside/rokucastor/internal/cast"
	"github.com/stupside/rokucastor/internal/device"
	"github.com/stupside/rokucastor/internal/roku"
)

// deviceItem is one Roku on the discovery screen.
type deviceItem struct{ device.Info }

func (d deviceItem) Title() string       { return d.Name }
func (d deviceItem) Description() string { return displayAddress(d.Address) }
func (d deviceItem) FilterValue() string { return d.Name }

func displayAddress(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" {
		return address
	}
	return u.Host
}

type devicesFoundMsg struct{ devices []device.Info }

func discoverCmd(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		return devicesFoundMsg{devices: roku.Family{}.Discover(ctx)}
	}
}

// castPhase is how far a cast has got.
type castPhase int

const (
	castPreparing castPhase = iota
	castPlaying
	castStopping
	castFailed
	castFinished
)

// castState is one cast's UI state. The resource and cancel function outlive
// the screen so a stop can release the Roku even while the spinner shows.
type castState struct {
	id       int
	phase    castPhase
	video    string
	subtitle string
	generate bool
	language string
	start    time.Duration
	target   string

	ctx     context.Context
	cancel  context.CancelCauseFunc
	res     *resource
	session *cast.Session
	player  device.Device

	playhead  device.Playhead
	transcode *cast.TranscodeInfo
	err       error
	notice    error
	pausing   bool
}

type preparedMsg struct {
	id      int
	session *cast.Session
	err     error
}

type playbackEndedMsg struct {
	id  int
	err error
}

type positionTickMsg struct{ id int }

type positionMsg struct {
	id       int
	playhead device.Playhead
	err      error
}

type pausedMsg struct {
	id  int
	err error
}

type stoppedMsg struct {
	id            int
	err           error
	returnToFiles bool
}

type resourceClosedMsg struct{ id int }

func prepareCastCmd(ctx context.Context, id int, res *resource, opts cast.Options) tea.Cmd {
	return func() tea.Msg {
		session, err := cast.Start(ctx, opts)
		if err != nil {
			return preparedMsg{id: id, err: err}
		}
		if !res.adopt(session) {
			// The operator left while the session was starting.
			_ = session.Close()
			return preparedMsg{id: id, err: context.Canceled}
		}
		return preparedMsg{id: id, session: session}
	}
}

func awaitEndCmd(ctx context.Context, id int, session *cast.Session) tea.Cmd {
	return func() tea.Msg {
		return playbackEndedMsg{id: id, err: session.AwaitEnd(ctx)}
	}
}

func pollPositionCmd(ctx context.Context, id int, player device.Device) tea.Cmd {
	return func() tea.Msg {
		playhead, err := player.Position(ctx)
		return positionMsg{id: id, playhead: playhead, err: err}
	}
}

func togglePauseCmd(ctx context.Context, id int, player device.Device, paused bool) tea.Cmd {
	return func() tea.Msg {
		if paused {
			return pausedMsg{id: id, err: player.Resume(ctx)}
		}
		return pausedMsg{id: id, err: player.Pause(ctx)}
	}
}

func closeResourceCmd(res *resource, id int) tea.Cmd {
	return func() tea.Msg {
		_ = res.close()
		return resourceClosedMsg{id: id}
	}
}

func closeSessionCmd(session *cast.Session) tea.Cmd {
	return func() tea.Msg {
		_ = session.Close()
		return resourceClosedMsg{}
	}
}

func stopCastCmd(res *resource, id int, returnToFiles bool) tea.Cmd {
	return func() tea.Msg {
		return stoppedMsg{id: id, err: res.close(), returnToFiles: returnToFiles}
	}
}

func nextPositionTick(id int) tea.Cmd {
	return tea.Tick(device.PollInterval, func(time.Time) tea.Msg {
		return positionTickMsg{id: id}
	})
}

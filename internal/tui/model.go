package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/stupside/rokucastor/internal/cast"
	"github.com/stupside/rokucastor/internal/palette"
	"github.com/stupside/rokucastor/internal/whisper"
)

const spInline = 2

type screen int

const (
	screenDevices screen = iota
	screenFolder
	screenFiles
	screenSubtitles
	screenStart
	screenCasting
	screenFinished
)

type model struct {
	ctx  context.Context
	opts Options
	res  *resources

	pal    palette.Palette
	styles styles
	keys   keyMap
	help   help.Model
	spin   spinner.Model

	w, h int
	scr  screen

	deviceStep bool
	address    string
	targetName string

	devices     list.Model
	discovering bool

	folder     textinput.Model
	files      list.Model
	currentDir string
	loadTok    int
	loading    bool
	loadErr    error

	video   string
	sibling string
	subs    list.Model

	// subtitle is the SRT chosen for the next cast; generate asks for live
	// Whisper captions burned into the HLS transcode. The start prompt carries
	// both until the operator confirms a position.
	subtitle string
	generate bool
	startAt  time.Duration
	startIn  textinput.Model
	startErr error

	cast   castState
	castID int
}

func newModel(ctx context.Context, opts Options, res *resources) model {
	if strings.TrimSpace(opts.Language) == "" {
		opts.Language = "es"
	}
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = whisper.DefaultModel
	}
	if strings.TrimSpace(opts.StreamModel) == "" {
		opts.StreamModel = whisper.DefaultStreamModel
	}

	folder := textinput.New()
	folder.Prompt = "❯ "
	folder.Placeholder = `C:\Users\You\Videos`
	folder.CharLimit = 4096
	if wd, err := os.Getwd(); err == nil {
		folder.SetValue(wd)
	}

	startIn := textinput.New()
	startIn.Prompt = "❯ "
	startIn.Placeholder = "0:00"
	startIn.CharLimit = 16

	m := model{
		ctx:     ctx,
		opts:    opts,
		res:     res,
		keys:    defaultKeys(),
		help:    help.New(),
		spin:    spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		scr:     screenDevices,
		devices: newList(),
		files:   newList(),
		subs:    newList(),
		folder:  folder,
		startIn: startIn,
		address: strings.TrimSpace(opts.Device),
		castID:  0,
	}
	if m.address != "" {
		m.scr = screenFolder
		m.folder.Focus()
	} else {
		m.deviceStep = true
	}
	m.restyle(true)
	return m
}

func newList() list.Model {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	// The screens own quitting, so a list binding never leaves by accident.
	l.DisableQuitKeybindings()
	return l
}

// restyle repaints for the terminal background; dark until the terminal reports it.
func (m *model) restyle(dark bool) {
	m.pal = palette.New(dark)
	m.styles = newStyles(m.pal)
	m.help.Styles = newHelpStyles(m.pal)
	m.spin.Style = lipgloss.NewStyle().Foreground(m.pal.Accent)
	m.folder.SetStyles(newInputStyles(m.pal))
	m.startIn.SetStyles(newInputStyles(m.pal))
	for _, l := range []*list.Model{&m.devices, &m.files, &m.subs} {
		l.SetDelegate(m.pal.Delegate())
		m.pal.StyleList(l)
		l.Styles.NoItems = lipgloss.NewStyle().Foreground(m.pal.FgMuted).Padding(0, 2)
	}
}

func (m model) Init() tea.Cmd {
	if m.scr == screenFolder {
		return tea.Batch(tea.RequestBackgroundColor, textinput.Blink)
	}
	return tea.Batch(tea.RequestBackgroundColor, m.spin.Tick, discoverCmd(m.ctx))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.restyle(msg.IsDark())
		return m, nil

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		m.resize()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		if !m.spinning() {
			return m, nil // restarted by whatever loads next
		}
		return m, cmd

	case devicesFoundMsg:
		m.discovering = false
		items := make([]list.Item, len(msg.devices))
		for i, info := range msg.devices {
			items[i] = deviceItem{info}
		}
		m.devices.SetItems(items)
		m.devices.Select(0)
		return m, nil

	case folderLoadedMsg:
		if msg.tok != m.loadTok {
			return m, nil
		}
		m.loading = false
		m.loadErr = msg.err
		if msg.err != nil {
			return m, nil
		}
		m.currentDir = msg.path
		m.folder.SetValue(msg.path)
		// The operator may have gone back to the device list while a slow
		// folder was being read; keep the answer for the list screen but do
		// not pull them away from where they are.
		if m.scr != screenFolder && m.scr != screenFiles {
			return m, nil
		}
		m.files.SetItems(msg.items)
		m.files.Select(0)
		m.scr = screenFiles
		return m, nil

	case preparedMsg:
		return m.onPrepared(msg)

	case positionTickMsg:
		if msg.id != m.cast.id || m.cast.phase != castPlaying {
			return m, nil
		}
		return m, pollPositionCmd(m.cast.ctx, m.cast.id, m.cast.player)

	case positionMsg:
		if msg.id != m.cast.id || m.cast.phase != castPlaying {
			return m, nil
		}
		if msg.err == nil {
			m.cast.playhead = msg.playhead
		}
		// A player that cannot report a position is not a failed cast;
		// AwaitEnd still decides when playback is over.
		return m, nextPositionTick(m.cast.id)

	case pausedMsg:
		if msg.id != m.cast.id {
			return m, nil
		}
		m.cast.pausing = false
		m.cast.notice = msg.err
		return m, nil

	case playbackEndedMsg:
		return m.onPlaybackEnded(msg)

	case stoppedMsg:
		if msg.id != m.cast.id {
			return m, nil
		}
		if msg.returnToFiles {
			m.resetCast()
			m.scr = screenFiles
			return m, nil
		}
		return m, tea.Quit

	case resourceClosedMsg:
		return m, nil

	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}

	return m.forward(msg)
}

// forward sends cursor-blink and similar messages to the focused control.
func (m model) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.scr {
	case screenFolder:
		m.folder, cmd = m.folder.Update(msg)
	case screenStart:
		m.startIn, cmd = m.startIn.Update(msg)
	case screenDevices:
		m.devices, cmd = m.devices.Update(msg)
	case screenFiles:
		m.files, cmd = m.files.Update(msg)
	case screenSubtitles:
		m.subs, cmd = m.subs.Update(msg)
	}
	return m, cmd
}

func (m model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.interrupt) {
		return m.quit()
	}
	// q is a filename character on the folder prompt and a position on the
	// start prompt, so it only quits away from those text inputs.
	if m.scr != screenFolder && m.scr != screenStart && key.Matches(msg, m.keys.quit) {
		return m.quit()
	}
	if key.Matches(msg, m.keys.help) && m.scr != screenFolder && m.scr != screenStart && m.scr != screenCasting {
		m.help.ShowAll = !m.help.ShowAll
		return m, nil
	}

	switch m.scr {
	case screenDevices:
		return m.updateDevices(msg)
	case screenFolder:
		return m.updateFolder(msg)
	case screenFiles:
		return m.updateFiles(msg)
	case screenSubtitles:
		return m.updateSubtitles(msg)
	case screenStart:
		return m.updateStart(msg)
	case screenCasting:
		return m.updateCasting(msg)
	case screenFinished:
		return m.updateFinished(msg)
	}
	return m, nil
}

func (m model) updateDevices(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.refresh):
		m.discovering = true
		return m, tea.Batch(m.spin.Tick, discoverCmd(m.ctx))
	case key.Matches(msg, m.keys.enter):
		item, ok := m.devices.SelectedItem().(deviceItem)
		if !ok {
			return m, nil
		}
		m.address = item.Address
		m.targetName = item.Name
		m.folder.Focus()
		m.scr = screenFolder
		return m, textinput.Blink
	default:
		var cmd tea.Cmd
		m.devices, cmd = m.devices.Update(msg)
		return m, cmd
	}
}

func (m model) updateFolder(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.enter):
		m.loadErr = nil
		m.loading = true
		m.loadTok++
		return m, tea.Batch(m.spin.Tick, loadFolderCmd(m.ctx, m.folder.Value(), m.loadTok))
	case key.Matches(msg, m.keys.back):
		if m.deviceStep {
			m.folder.Blur()
			m.scr = screenDevices
			return m, nil
		}
		return m, tea.Quit
	default:
		m.loadErr = nil
		var cmd tea.Cmd
		m.folder, cmd = m.folder.Update(msg)
		return m, cmd
	}
}

func (m model) updateFiles(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.back):
		m.folder.SetValue(m.currentDir)
		m.folder.Focus()
		m.scr = screenFolder
		return m, textinput.Blink
	case key.Matches(msg, m.keys.enter):
		item, ok := m.files.SelectedItem().(entry)
		if !ok {
			return m, nil
		}
		if item.dir {
			m.loadErr = nil
			m.loading = true
			m.loadTok++
			return m, tea.Batch(m.spin.Tick, loadFolderCmd(m.ctx, item.path, m.loadTok))
		}
		return m.chooseVideo(item.path)
	default:
		var cmd tea.Cmd
		m.files, cmd = m.files.Update(msg)
		return m, cmd
	}
}

func (m model) chooseVideo(path string) (tea.Model, tea.Cmd) {
	m.video = path
	m.sibling = siblingSubtitle(path)
	cached, hasCached := whisper.Cached(m.opts.Layout, path, m.opts.Model, m.opts.Language)

	choices := make([]list.Item, 0, 4)
	if m.sibling != "" {
		choices = append(choices, subtitleChoice{
			label: "Use " + filepath.Base(m.sibling),
			path:  m.sibling,
		})
	}
	if hasCached && (m.sibling == "" || !strings.EqualFold(cached, m.sibling)) {
		choices = append(choices, subtitleChoice{
			label:       "Use cached subtitles (" + m.opts.Language + ")",
			description: filepath.Base(cached),
			path:        cached,
		})
	}
	choices = append(choices,
		subtitleChoice{
			label:       "Generate subtitles with Whisper",
			description: whisperHint(m.opts.StreamModel),
			generate:    true,
		},
		subtitleChoice{label: "No subtitles"},
	)
	m.subs.SetItems(choices)
	m.subs.Select(0)
	m.scr = screenSubtitles
	return m, nil
}

func whisperHint(model string) string {
	size := "78 MB"
	switch model {
	case whisper.ModelSmall:
		size = "488 MB"
	case whisper.ModelBase:
		size = "148 MB"
	}
	return model + " model · live captions · one-time " + size + " download"
}

func (m model) updateSubtitles(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.back):
		m.scr = screenFiles
		return m, nil
	case key.Matches(msg, m.keys.enter):
		choice, ok := m.subs.SelectedItem().(subtitleChoice)
		if !ok {
			return m, nil
		}
		return m.askStart(choice.path, choice.generate)
	default:
		var cmd tea.Cmd
		m.subs, cmd = m.subs.Update(msg)
		return m, cmd
	}
}

// askStart asks where the chosen video should begin; an empty answer plays it
// from the start. A generate choice is carried into the cast, where Whisper
// burns its cues in during the transcode.
func (m model) askStart(subtitle string, generate bool) (tea.Model, tea.Cmd) {
	m.subtitle = subtitle
	m.generate = generate
	m.startAt = 0
	m.startErr = nil
	m.startIn.Reset()
	m.startIn.Focus()
	m.scr = screenStart
	m.help.ShowAll = false
	return m, textinput.Blink
}

func (m model) updateStart(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.back):
		m.startIn.Blur()
		m.scr = screenSubtitles
		return m, nil
	case key.Matches(msg, m.keys.enter):
		start, err := cast.ParseStart(m.startIn.Value())
		if err != nil {
			m.startErr = err
			return m, nil
		}
		m.startErr = nil
		m.startAt = start
		m.startIn.Blur()
		return m.startCast(m.subtitle)
	default:
		m.startErr = nil
		var cmd tea.Cmd
		m.startIn, cmd = m.startIn.Update(msg)
		return m, cmd
	}
}

func (m model) updateCasting(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.cast.phase {
	case castPlaying:
		switch {
		case key.Matches(msg, m.keys.pause):
			if m.cast.pausing {
				return m, nil
			}
			m.cast.pausing = true
			m.cast.notice = nil
			return m, togglePauseCmd(m.cast.ctx, m.cast.id, m.cast.player, m.cast.playhead.Paused)
		case key.Matches(msg, m.keys.back):
			return m.stopToFiles()
		}
	case castPreparing, castFailed:
		if key.Matches(msg, m.keys.back) {
			return m.stopToFiles()
		}
	}
	return m, nil
}

func (m model) updateFinished(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.enter) || key.Matches(msg, m.keys.back) {
		m.resetCast()
		m.scr = screenFiles
		return m, nil
	}
	return m, nil
}

func (m model) onPrepared(msg preparedMsg) (tea.Model, tea.Cmd) {
	if msg.id != m.cast.id || m.cast.phase != castPreparing {
		if msg.session != nil {
			return m, closeSessionCmd(msg.session)
		}
		return m, nil
	}
	if msg.err != nil {
		m.cast.phase = castFailed
		m.cast.err = msg.err
		return m, nil
	}
	m.cast.session = msg.session
	m.cast.player = msg.session.Player
	m.cast.target = msg.session.Player.Name()
	m.cast.transcode = msg.session.TranscodeInfo
	m.cast.phase = castPlaying
	return m, tea.Batch(
		awaitEndCmd(m.cast.ctx, m.cast.id, m.cast.session),
		nextPositionTick(m.cast.id),
	)
}

func (m model) onPlaybackEnded(msg playbackEndedMsg) (tea.Model, tea.Cmd) {
	if msg.id != m.cast.id {
		return m, nil
	}
	if m.cast.phase == castStopping {
		return m, nil // the stop path owns the next screen
	}
	switch {
	case msg.err == nil:
		m.cast.phase = castFinished
		m.scr = screenFinished
		return m, closeResourceCmd(m.cast.res, m.cast.id)
	case errors.Is(msg.err, context.Canceled):
		return m, nil
	default:
		m.cast.phase = castFailed
		m.cast.err = msg.err
		return m, closeResourceCmd(m.cast.res, m.cast.id)
	}
}

func (m model) startCast(subtitle string) (tea.Model, tea.Cmd) {
	m.castID++
	castCtx, cancel := context.WithCancelCause(m.ctx)
	target := m.targetName
	if target == "" {
		target = m.address
	}
	m.cast = castState{
		id:       m.castID,
		phase:    castPreparing,
		video:    m.video,
		subtitle: subtitle,
		generate: m.generate,
		language: m.opts.Language,
		start:    m.startAt,
		target:   target,
		ctx:      castCtx,
		cancel:   cancel,
		res:      m.res.new(),
	}
	m.scr = screenCasting
	m.help.ShowAll = false
	m.loadErr = nil
	return m, tea.Batch(m.spin.Tick, prepareCastCmd(castCtx, m.cast.id, m.cast.res, cast.Options{
		Address:           m.address,
		Password:          m.opts.Password,
		AppID:             m.opts.AppID,
		StartupTimeout:    m.opts.StartupTimeout,
		Video:             m.video,
		Start:             m.startAt,
		Subtitle:          subtitle,
		Language:          m.opts.Language,
		GenerateSubtitles: m.generate,
		StreamModel:       m.opts.StreamModel,
		Layout:            m.opts.Layout,
	}))
}

func (m model) quit() (tea.Model, tea.Cmd) {
	if m.scr == screenCasting && m.cast.phase != castStopping {
		switch m.cast.phase {
		case castPreparing, castPlaying:
			m.cast.cancel(context.Canceled)
			m.cast.phase = castStopping
			return m, tea.Batch(m.spin.Tick, stopCastCmd(m.cast.res, m.cast.id, false))
		}
	}
	return m, tea.Quit
}

func (m model) stopToFiles() (tea.Model, tea.Cmd) {
	switch m.cast.phase {
	case castPreparing, castPlaying:
		m.cast.cancel(context.Canceled)
		m.cast.phase = castStopping
		return m, tea.Batch(m.spin.Tick, stopCastCmd(m.cast.res, m.cast.id, true))
	case castFailed, castFinished:
		m.resetCast()
		m.scr = screenFiles
		return m, nil
	}
	return m, nil
}

// resetCast forgets the finished cast; its resource is already closed.
func (m *model) resetCast() {
	if m.cast.cancel != nil {
		m.cast.cancel(context.Canceled)
	}
	m.cast = castState{}
}

func (m model) spinning() bool {
	switch {
	case m.loading:
		return true
	case m.scr == screenDevices && m.discovering:
		return true
	case m.scr == screenCasting && (m.cast.phase == castPreparing || m.cast.phase == castStopping):
		return true
	}
	return false
}

func (m *model) resize() {
	if m.w <= 0 || m.h <= 0 {
		return
	}
	m.files.SetSize(max(m.w-4, 20), max(m.h-10, 5))
	m.devices.SetSize(max(m.w-4, 20), max(m.h-10, 5))
	m.subs.SetSize(max(m.w-4, 20), max(m.h-10, 5))
	m.folder.SetWidth(max(m.w-8, 20))
}

// subtitleChoice is one row of the subtitle prompt.
type subtitleChoice struct {
	label       string
	description string
	path        string
	generate    bool
}

func (s subtitleChoice) Title() string       { return s.label }
func (s subtitleChoice) Description() string { return s.description }
func (s subtitleChoice) FilterValue() string { return s.label }

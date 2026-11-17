package tui

import "charm.land/bubbles/v2/key"

// keyMap holds the TUI bindings. Text input screens own printable keys, so
// quit and back are the only global shortcuts there.
type keyMap struct {
	up, down         key.Binding
	pageUp, pageDown key.Binding
	enter, back      key.Binding
	refresh          key.Binding
	pause            key.Binding
	help             key.Binding
	quit             key.Binding
	interrupt        key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		up:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		pageUp:    key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
		pageDown:  key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down")),
		enter:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "select")),
		back:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		refresh:   key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "search again")),
		pause:     key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "pause/resume")),
		help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		quit:      key.NewBinding(key.WithKeys("ctrl+c", "q"), key.WithHelp("^C/q", "quit")),
		interrupt: key.NewBinding(key.WithKeys("ctrl+c")),
	}
}

// screenHelp adapts keyMap to bubbles' help.KeyMap for the current screen.
type screenHelp struct {
	k       keyMap
	screen  screen
	playing bool
}

func (s screenHelp) ShortHelp() []key.Binding {
	switch s.screen {
	case screenDevices:
		return []key.Binding{s.k.up, s.k.down, s.k.enter, s.k.refresh, s.k.quit}
	case screenFolder:
		return []key.Binding{s.k.enter, s.k.back, s.k.quit}
	case screenFiles:
		return []key.Binding{s.k.up, s.k.down, s.k.enter, s.k.back, s.k.quit}
	case screenSubtitles:
		return []key.Binding{s.k.up, s.k.down, s.k.enter, s.k.back, s.k.quit}
	case screenStart:
		return []key.Binding{s.k.enter, s.k.back, s.k.quit}
	case screenCasting:
		if s.playing {
			return []key.Binding{s.k.pause, s.k.back, s.k.quit}
		}
		return []key.Binding{s.k.back, s.k.quit}
	case screenFinished:
		return []key.Binding{s.k.enter, s.k.back, s.k.quit}
	}
	return nil
}

func (s screenHelp) FullHelp() [][]key.Binding {
	nav := []key.Binding{s.k.up, s.k.down, s.k.pageUp, s.k.pageDown}
	switch s.screen {
	case screenDevices:
		return [][]key.Binding{nav, {s.k.enter, s.k.refresh}, {s.k.quit}}
	case screenFolder:
		return [][]key.Binding{{s.k.enter, s.k.back}, {s.k.quit}}
	case screenFiles, screenSubtitles:
		return [][]key.Binding{nav, {s.k.enter, s.k.back}, {s.k.quit}}
	case screenStart:
		return [][]key.Binding{{s.k.enter, s.k.back}, {s.k.quit}}
	case screenCasting:
		if s.playing {
			return [][]key.Binding{{s.k.pause}, {s.k.back}, {s.k.quit}}
		}
		return [][]key.Binding{{s.k.back}, {s.k.quit}}
	case screenFinished:
		return [][]key.Binding{{s.k.enter, s.k.back}, {s.k.quit}}
	}
	return nil
}

package tui

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"

	"github.com/stupside/rokucastor/internal/palette"
)

type styles struct {
	title     lipgloss.Style
	meta      lipgloss.Style
	accent    lipgloss.Style
	muted     lipgloss.Style
	err       lipgloss.Style
	danger    lipgloss.Style
	bar       lipgloss.Style
	barAccent lipgloss.Style
}

func newStyles(p palette.Palette) styles {
	titleText := lipgloss.NewStyle().Bold(true).Foreground(p.Accent)
	return styles{
		title:     titleText.Padding(0, spInline),
		meta:      lipgloss.NewStyle().Foreground(p.FgSecondary),
		accent:    lipgloss.NewStyle().Foreground(p.Accent),
		muted:     lipgloss.NewStyle().Foreground(p.FgMuted),
		err:       lipgloss.NewStyle().Foreground(p.Error).Bold(true),
		danger:    lipgloss.NewStyle().Foreground(p.Error),
		bar:       lipgloss.NewStyle().Background(p.Bar),
		barAccent: lipgloss.NewStyle().Background(p.Bar).Foreground(p.Accent).Bold(true),
	}
}

func newHelpStyles(p palette.Palette) help.Styles {
	s := help.DefaultStyles(p.Dark)
	s.ShortKey = s.ShortKey.Foreground(p.Accent).Bold(true)
	s.ShortDesc = s.ShortDesc.Foreground(p.FgMuted)
	s.ShortSeparator = s.ShortSeparator.Foreground(p.Rule)
	s.FullKey = s.FullKey.Foreground(p.FgSecondary)
	s.FullDesc = s.FullDesc.Foreground(p.FgMuted)
	s.FullSeparator = s.FullSeparator.Foreground(p.FgMuted)
	s.Ellipsis = s.Ellipsis.Foreground(p.FgMuted)
	return s
}

func newInputStyles(p palette.Palette) textinput.Styles {
	s := textinput.DefaultStyles(p.Dark)
	s.Focused.Prompt = lipgloss.NewStyle().Foreground(p.Accent)
	s.Focused.Placeholder = lipgloss.NewStyle().Foreground(p.FgMuted)
	s.Focused.Text = lipgloss.NewStyle().Foreground(p.FgPrimary)
	s.Cursor.Color = lipgloss.NoColor{} // terminal reverse video, not v2's grey tint
	return s
}

package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/stupside/rokucastor/internal/cast"
)

func (m model) View() tea.View {
	v := tea.NewView(m.content())
	v.AltScreen = true
	return v
}

func (m model) content() string {
	rows := []string{m.header(m.section()), ""}
	rows = append(rows, m.body()...)
	rows = append(rows, "", m.footer())
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m model) section() string {
	switch m.scr {
	case screenDevices:
		return "Select a Roku"
	case screenFolder, screenFiles:
		return "Local video"
	case screenSubtitles:
		return "Subtitles"
	case screenStart:
		return "Start at"
	case screenCasting:
		return "Casting"
	case screenFinished:
		return "Finished"
	}
	return ""
}

func (m model) header(section string) string {
	left := m.styles.barAccent.Render("  rokucastor  ·  " + section)
	right := ""
	if m.scr == screenCasting || m.scr == screenFinished {
		right = m.cast.target
	} else {
		right = m.targetName
	}
	rightStyle := lipgloss.NewStyle().Background(m.pal.Bar).Foreground(m.pal.FgMuted)
	if right != "" {
		right = "  " + right
	}
	right = rightStyle.Render(right + "  ")
	if m.w <= 0 {
		return left + right
	}
	gap := max(m.w-lipgloss.Width(left)-lipgloss.Width(right), 0)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, m.styles.bar.Render(strings.Repeat(" ", gap)), right)
}

func (m model) body() []string {
	switch m.scr {
	case screenDevices:
		return m.viewDevices()
	case screenFolder:
		return m.viewFolder()
	case screenFiles:
		return m.viewFiles()
	case screenSubtitles:
		return m.viewSubtitles()
	case screenStart:
		return m.viewStart()
	case screenCasting:
		return m.viewCasting()
	case screenFinished:
		return m.viewFinished()
	}
	return nil
}

func (m model) viewDevices() []string {
	if m.discovering {
		return []string{m.spin.View() + " " + m.styles.muted.Render("Searching for Rokus on the local network...")}
	}
	if len(m.devices.Items()) == 0 {
		return []string{
			m.styles.meta.Render("No Rokus found."),
			"",
			m.styles.muted.Render("Check that the Roku is on and on the same network, then press r to search again."),
		}
	}
	return []string{m.devices.View()}
}

func (m model) viewFolder() []string {
	return []string{
		m.styles.meta.Render("Enter the folder with your videos"),
		"",
		m.folder.View(),
		"",
		m.styles.muted.Render(`Windows paths such as C:\Users\You\Videos work here.`),
	}
}

func (m model) viewFiles() []string {
	return []string{
		m.styles.muted.Render(m.currentDir),
		"",
		m.files.View(),
	}
}

func (m model) viewSubtitles() []string {
	return []string{
		m.styles.meta.Render(filepath.Base(m.video)),
		"",
		m.styles.meta.Render("Subtitles"),
		m.subs.View(),
	}
}

func (m model) viewStart() []string {
	rows := []string{
		m.styles.title.Render(filepath.Base(m.video)),
		m.styles.muted.Render("Start at"),
		"",
		m.startIn.View(),
	}
	if m.startErr != nil {
		rows = append(rows, m.styles.danger.Render(m.startErr.Error()))
	}
	rows = append(rows,
		"",
		m.styles.muted.Render("Leave it empty to start at the beginning."),
		m.styles.muted.Render("Examples: 12:30, 1:12:30 or 1h12m (1:00 is one hour)."),
	)
	return rows
}

func (m model) viewCasting() []string {
	title := m.styles.title.Render(filepath.Base(m.cast.video))
	on := m.styles.muted.Render("on " + m.cast.target)
	if m.cast.start > 0 {
		on += m.styles.muted.Render(" · from " + clock(m.cast.start))
	}

	switch m.cast.phase {
	case castPreparing:
		message := "Connecting to the Roku and preparing the channel..."
		if m.cast.generate {
			message = "Preparing Whisper and its live subtitles..."
		}
		return []string{
			title,
			on,
			"",
			m.spin.View() + " " + m.styles.muted.Render(message),
		}
	case castStopping:
		return []string{
			title,
			on,
			"",
			m.spin.View() + " " + m.styles.muted.Render("Stopping and sending the Roku home..."),
		}
	case castFailed:
		rows := []string{title, on, "", m.styles.err.Render("Playback failed")}
		if m.cast.err != nil {
			rows = append(rows, m.styles.danger.Render(m.cast.err.Error()))
		}
		rows = append(rows, "", m.styles.muted.Render("esc returns to the file list; q quits."))
		return rows
	}

	rows := []string{title, on, ""}
	if m.cast.transcode != nil {
		rows = append(rows, m.styles.meta.Render(transcodeSummary(*m.cast.transcode)))
	} else if bar := m.progressBar(); bar != "" {
		rows = append(rows, bar)
	}
	state := "playing"
	if m.cast.playhead.Paused {
		state = "paused"
	}
	elapsed := clock(m.cast.playhead.At)
	if m.cast.transcode == nil && m.cast.playhead.Duration > 0 {
		elapsed += " / " + clock(m.cast.playhead.Duration)
	}
	rows = append(rows, m.styles.accent.Render(state)+"  "+m.styles.muted.Render(elapsed))
	if m.cast.generate {
		rows = append(rows, "", m.styles.muted.Render(
			"Subtitles: generated live by Whisper ("+m.cast.language+")"))
	} else if m.cast.subtitle != "" {
		rows = append(rows, "", m.styles.muted.Render(
			"Subtitles: "+filepath.Base(m.cast.subtitle)+" ("+m.cast.language+")"))
	}
	if m.cast.notice != nil {
		rows = append(rows, "", m.styles.err.Render(m.cast.notice.Error()))
	}
	return rows
}

func transcodeSummary(info cast.TranscodeInfo) string {
	parts := []string{
		fmt.Sprintf("%s %dx%d", strings.ToUpper(info.VideoCodec), info.VideoWidth, info.VideoHeight),
	}
	if info.AudioCodec != "" {
		audio := fmt.Sprintf("%s %dch", strings.ToUpper(info.AudioCodec), info.AudioChannels)
		if info.AudioSampleRate > 0 {
			audio += fmt.Sprintf(" %dkHz", info.AudioSampleRate/1000)
		}
		parts = append(parts, audio)
	}
	if info.Encoder != "" {
		parts = append(parts, info.Encoder)
	}
	return "Transcoding: " + strings.Join(parts, " | ")
}

func (m model) viewFinished() []string {
	return []string{
		m.styles.title.Render(filepath.Base(m.cast.video)),
		m.styles.muted.Render("Finished on " + m.cast.target + "."),
		"",
		m.styles.muted.Render("enter browses the folder again; q quits."),
	}
}

// progressBar renders the playhead as a bar; empty when the Roku reports no
// duration, in which case the elapsed time alone is shown.
func (m model) progressBar() string {
	playhead := m.cast.playhead
	if playhead.Duration <= 0 {
		return ""
	}
	fraction := float64(playhead.At) / float64(playhead.Duration)
	width := max(min(m.w-8, 44), 10)
	return m.bar(fraction, width) + "  " + m.styles.meta.Render(fmt.Sprintf("%d%%", int(min(max(fraction, 0), 1)*100+0.5)))
}

// bar draws fraction as a filled and an empty run of cells.
func (m model) bar(fraction float64, width int) string {
	fraction = min(max(fraction, 0), 1)
	filled := int(fraction * float64(width))
	return m.styles.accent.Render(strings.Repeat("█", filled)) +
		m.styles.muted.Render(strings.Repeat("░", width-filled))
}

func (m model) footer() string {
	km := screenHelp{
		k:       m.keys,
		screen:  m.scr,
		playing: m.cast.phase == castPlaying,
	}
	helpLine := "  " + m.help.View(km)
	status := " "
	switch {
	case m.loading:
		status = "  " + m.spin.View() + " " + m.styles.muted.Render("reading folder...")
	case m.loadErr != nil:
		status = "  " + m.styles.err.Render(m.loadErr.Error())
	}
	return lipgloss.JoinVertical(lipgloss.Left, helpLine, status)
}

package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/stupside/rokucastor/internal/config"
)

func testModel(t *testing.T, opts Options) model {
	t.Helper()
	opts.Layout = config.NewAt(t.TempDir())
	return newModel(t.Context(), opts, &resources{})
}

func videoWithSRT(t *testing.T, withSubtitle bool) string {
	t.Helper()
	dir := t.TempDir()
	video := filepath.Join(dir, "movie.mp4")
	if err := os.WriteFile(video, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if withSubtitle {
		if err := os.WriteFile(filepath.Join(dir, "movie.srt"), []byte("1\n00:00:00,000 --> 00:00:01,000\nHi\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return video
}

func TestAFileWithoutASiblingSRTOffersWhisperByDefault(t *testing.T) {
	m := testModel(t, Options{Device: "192.0.2.1"})
	got, cmd := m.chooseVideo(videoWithSRT(t, false))
	m = got.(model)
	if m.scr != screenSubtitles || cmd != nil {
		t.Fatalf("screen = %v cmd = %v, want the subtitle prompt", m.scr, cmd)
	}
	if len(m.subs.Items()) != 2 {
		t.Fatalf("subtitle choices = %d, want Whisper and none", len(m.subs.Items()))
	}
	if choice := m.subs.SelectedItem().(subtitleChoice); !choice.generate {
		t.Fatalf("default choice = %+v, want generating subtitles", choice)
	}
}

func TestASiblingSRTIsOfferedBeforeCasting(t *testing.T) {
	video := videoWithSRT(t, true)
	m := testModel(t, Options{Device: "192.0.2.1"})
	got, cmd := m.chooseVideo(video)
	m = got.(model)
	if m.scr != screenSubtitles || cmd != nil {
		t.Fatalf("screen = %v cmd = %v, want the subtitle prompt", m.scr, cmd)
	}
	if len(m.subs.Items()) != 3 {
		t.Fatalf("subtitle choices = %d, want use-the-SRT, Whisper and none", len(m.subs.Items()))
	}
	first := m.subs.SelectedItem().(subtitleChoice)
	if first.path != filepath.Join(filepath.Dir(video), "movie.srt") {
		t.Errorf("default choice = %+v, want the sibling SRT", first)
	}
	second := m.subs.Items()[1].(subtitleChoice)
	if !second.generate {
		t.Errorf("second choice = %+v, want Whisper to be offered too", second)
	}
}

func TestChoosingNoSubtitlesCastsWithoutATrack(t *testing.T) {
	m := testModel(t, Options{Device: "192.0.2.1"})
	m.scr = screenSubtitles
	m.video = "movie.mp4"
	m.subs.SetItems([]list.Item{subtitleChoice{label: "No subtitles"}})
	m.subs.Select(0)

	got, cmd := m.updateSubtitles(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(model)
	if m.scr != screenStart || cmd == nil {
		t.Fatalf("screen = %v cmd = %v, want the start position prompt", m.scr, cmd)
	}

	got, cmd = m.updateStart(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(model)
	if m.scr != screenCasting || m.cast.subtitle != "" || cmd == nil {
		t.Fatalf("screen = %v subtitle = %q cmd = %v, want a cast without subtitles", m.scr, m.cast.subtitle, cmd)
	}
}

func TestTheStartPromptParsesAPosition(t *testing.T) {
	m := testModel(t, Options{Device: "192.0.2.1"})
	m.scr = screenStart
	m.video = "movie.mp4"

	m.startIn.SetValue("1:75")
	got, _ := m.updateStart(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(model)
	if m.scr != screenStart || m.startErr == nil {
		t.Fatalf("screen = %v err = %v, want the position rejected", m.scr, m.startErr)
	}
	if got := rendered(t, m); !strings.Contains(got, "1:75") {
		t.Errorf("start screen = %q, want the invalid position explained", got)
	}

	m.startIn.SetValue("1:12:30")
	got, cmd := m.updateStart(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(model)
	if m.scr != screenCasting || m.cast.start != time.Hour+12*time.Minute+30*time.Second || cmd == nil {
		t.Fatalf("screen = %v start = %v cmd = %v, want a cast from 1:12:30", m.scr, m.cast.start, cmd)
	}
}

func TestChoosingWhisperAsksForAStartThenCastsLive(t *testing.T) {
	m := testModel(t, Options{Device: "192.0.2.1", StreamModel: "tiny"})
	m.scr = screenSubtitles
	m.video = "movie.mp4"
	m.subs.SetItems([]list.Item{subtitleChoice{label: "Generate", generate: true}})
	m.subs.Select(0)

	got, cmd := m.updateSubtitles(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(model)
	if m.scr != screenStart || !m.generate || m.subtitle != "" || cmd == nil {
		t.Fatalf("screen = %v generate = %v subtitle = %q cmd = %v, want the start prompt asking for live subtitles", m.scr, m.generate, m.subtitle, cmd)
	}

	got, cmd = m.updateStart(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(model)
	if m.scr != screenCasting || m.cast.phase != castPreparing || !m.cast.generate || m.cast.subtitle != "" {
		t.Fatalf("screen = %v phase = %v generate = %v subtitle = %q, want a live-subtitle cast", m.scr, m.cast.phase, m.cast.generate, m.cast.subtitle)
	}
	if cmd == nil {
		t.Fatal("the live-subtitle cast returned no prepare command")
	}
}

func TestChoosingASRTDoesNotAskForGeneratedSubtitles(t *testing.T) {
	m := testModel(t, Options{Device: "192.0.2.1"})
	m.scr = screenSubtitles
	m.video = "movie.mp4"
	m.subs.SetItems([]list.Item{subtitleChoice{label: "Use movie.srt", path: `C:\Videos\movie.srt`}})
	m.subs.Select(0)

	got, _ := m.updateSubtitles(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(model)
	if m.generate || m.subtitle == "" {
		t.Fatalf("generate = %v subtitle = %q, want the chosen SRT only", m.generate, m.subtitle)
	}
}

func TestQuitDuringACastStopsItFirst(t *testing.T) {
	m := testModel(t, Options{Device: "192.0.2.1"})
	ctx, cancel := context.WithCancelCause(t.Context())
	m.scr = screenCasting
	m.castID = 7
	m.cast = castState{id: 7, phase: castPlaying, res: &resource{}, cancel: cancel, ctx: ctx}

	got, cmd := m.updateKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m = got.(model)
	if m.cast.phase != castStopping {
		t.Fatalf("phase = %v, want the cast stopping before the program leaves", m.cast.phase)
	}
	if ctx.Err() == nil {
		t.Error("quitting did not cancel the cast context")
	}
	if cmd == nil {
		t.Fatal("quitting during a cast returned no stop command")
	}
}

func TestAStoppedCastThatWasQuittingEndsTheProgram(t *testing.T) {
	m := testModel(t, Options{Device: "192.0.2.1"})
	m.scr = screenCasting
	m.castID = 7
	m.cast = castState{id: 7, phase: castStopping, res: &resource{}}

	_, cmd := m.Update(stoppedMsg{id: 7})
	if cmd == nil {
		t.Fatal("a stopped cast left the program running")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("a stopped cast did not quit")
	}
}

func TestAStoppedCastReturnsToTheFileList(t *testing.T) {
	m := testModel(t, Options{Device: "192.0.2.1"})
	m.scr = screenCasting
	m.castID = 7
	m.cast = castState{id: 7, phase: castStopping, res: &resource{}}

	got, cmd := m.Update(stoppedMsg{id: 7, returnToFiles: true})
	m = got.(model)
	if m.scr != screenFiles || cmd != nil {
		t.Fatalf("screen = %v cmd = %v, want the file list", m.scr, cmd)
	}
}

func TestFinishedPlaybackOffersTheFileListAgain(t *testing.T) {
	m := testModel(t, Options{Device: "192.0.2.1"})
	m.scr = screenCasting
	m.castID = 3
	m.cast = castState{id: 3, phase: castPlaying, res: &resource{}}

	got, cmd := m.Update(playbackEndedMsg{id: 3})
	m = got.(model)
	if m.scr != screenFinished || m.cast.phase != castFinished {
		t.Fatalf("screen = %v phase = %v, want finished", m.scr, m.cast.phase)
	}
	if cmd == nil {
		t.Fatal("finishing playback did not release the session")
	}

	got, _ = m.updateFinished(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got.(model).scr != screenFiles {
		t.Fatal("enter on the finished screen did not return to the file list")
	}
}

func TestAFailedCastShowsTheErrorAndCanReturnToTheList(t *testing.T) {
	m := testModel(t, Options{Device: "192.0.2.1"})
	m.scr = screenCasting
	m.castID = 4
	m.cast = castState{id: 4, phase: castPlaying, res: &resource{}}

	got, _ := m.Update(playbackEndedMsg{id: 4, err: errors.New("decoder refused")})
	m = got.(model)
	if m.cast.phase != castFailed || m.cast.err == nil {
		t.Fatalf("phase = %v err = %v, want the failure kept", m.cast.phase, m.cast.err)
	}
	if body := m.body(); len(body) == 0 {
		t.Fatal("the failed cast rendered no body")
	}
	got, _ = m.stopToFiles()
	if got.(model).scr != screenFiles {
		t.Fatal("esc on a failed cast did not return to the file list")
	}
}

func TestMessagesForAnOlderCastAreIgnored(t *testing.T) {
	m := testModel(t, Options{Device: "192.0.2.1"})
	m.scr = screenCasting
	m.castID = 9
	m.cast = castState{id: 9, phase: castPlaying, res: &resource{}}

	got, _ := m.Update(playbackEndedMsg{id: 8})
	if got.(model).scr != screenCasting {
		t.Fatal("a message from an older cast changed the screen")
	}
}

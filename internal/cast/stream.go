package cast

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stupside/rokucastor/internal/config"
	"github.com/stupside/rokucastor/internal/subtitle"
	"github.com/stupside/rokucastor/internal/whisper"
)

const (
	// audioPace reads the sound faster than the encoder so cues stay ahead.
	audioPace = 1.5
	// encodePace lets the encoder run slightly ahead of live, so a cue can be
	// committed before the frame that needs it is produced.
	encodePace = 1.1
	// cueLead is how much settled speech to wait for before serving video.
	cueLead = 5 * time.Second
	// cueLeadTimeout keeps a stalled recognizer from delaying the cast forever.
	cueLeadTimeout = 20 * time.Second
)

// liveSubtitles feeds the source's sound to Whisper and keeps the cue file the
// encoder burns in.
type liveSubtitles struct {
	cues       *subtitle.Cues
	path       string
	recognizer *whisper.Server
	cancel     context.CancelFunc
	done       chan struct{}
	writer     func(time.Duration)
	once       sync.Once
}

// startLiveSubtitles starts whisper-server, then extracts the audio from the
// same start position as the HLS timeline and transcribes it as it arrives.
func startLiveSubtitles(ctx context.Context, ffmpeg, video string, start time.Duration, model, language string, layout config.Layout, workDir string) (*liveSubtitles, error) {
	cues := &subtitle.Cues{}
	file := subtitle.NewCueFile(workDir, cues)
	path, err := file.Create()
	if err != nil {
		return nil, err
	}
	recognizer, err := whisper.Stream(ctx, whisper.StreamOptions{
		Layout:   layout,
		Model:    model,
		Language: language,
		FFmpeg:   ffmpeg,
	})
	if err != nil {
		return nil, err
	}

	feedCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(feedCtx, ffmpeg, audioArgs(video, start)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = recognizer.Close()
		return nil, fmt.Errorf("reading the extracted audio: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		_ = recognizer.Close()
		return nil, fmt.Errorf("starting audio extraction: %w%s", err, stderrSuffix(&stderr))
	}

	live := &liveSubtitles{
		cues:       cues,
		path:       path,
		recognizer: recognizer,
		cancel:     cancel,
		done:       make(chan struct{}),
		writer:     file.Writer(ctx),
	}
	go func() {
		transcribeErr := subtitle.Transcribe(feedCtx, stdout, recognizer, cues)
		waitErr := cmd.Wait()
		if transcribeErr != nil && !errors.Is(transcribeErr, context.Canceled) {
			slog.WarnContext(ctx, "live transcription stopped", "error", transcribeErr)
		}
		if waitErr != nil && !errors.Is(waitErr, context.Canceled) && feedCtx.Err() == nil {
			slog.WarnContext(ctx, "audio extraction stopped", "error", waitErr, "stderr", stderrSuffix(&stderr))
		}
		close(live.done)
	}()
	return live, nil
}

// audioArgs extracts mono 16 kHz PCM at a pace faster than realtime. The seek
// matches the encoder's, so cue times are HLS-relative.
func audioArgs(video string, start time.Duration) []string {
	args := []string{"-hide_banner", "-nostdin", "-nostats", "-loglevel", "error"}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start.Seconds(), 'f', 3, 64))
	}
	return append(args,
		"-readrate", strconv.FormatFloat(audioPace, 'f', 1, 64),
		"-i", video,
		"-vn",
		"-af", "aresample=async=1:first_pts=0",
		"-ac", "1",
		"-ar", strconv.Itoa(subtitle.SampleRate),
		"-f", "s16le",
		"pipe:1",
	)
}

// stderrSuffix turns a child's captured error output into a message suffix.
// It is only read after the child has been waited for, so no locking is needed.
func stderrSuffix(buf *bytes.Buffer) string {
	text := strings.TrimSpace(buf.String())
	if text == "" {
		return ""
	}
	return ": " + text
}

// awaitLead waits until enough speech is settled for the encoder to start
// ahead of the cues. A recognizer that fails mid-stream only costs captions,
// not the cast, so this returns instead of failing.
func (l *liveSubtitles) awaitLead(ctx context.Context) {
	deadline := time.NewTimer(cueLeadTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if settled, complete := l.cues.State(); settled >= cueLead.Seconds() || complete {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-l.done:
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

// Close stops the audio feed, waits for the transcription to return and then
// stops whisper-server.
func (l *liveSubtitles) Close() error {
	var err error
	l.once.Do(func() {
		l.cancel()
		<-l.done
		if closeErr := l.recognizer.Close(); closeErr != nil {
			err = closeErr
		}
	})
	return err
}

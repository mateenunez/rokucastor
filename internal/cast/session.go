// Package cast prepares and runs one local video on a Roku: it connects over
// ECP, publishes the source or a progressive transcode, and starts playback.
package cast

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stupside/rokucastor/internal/config"
	"github.com/stupside/rokucastor/internal/device"
	"github.com/stupside/rokucastor/internal/media"
	"github.com/stupside/rokucastor/internal/roku"
	"github.com/stupside/rokucastor/internal/transcode"
)

const closeGrace = 5 * time.Second

// Options selects what to play and where.
type Options struct {
	// Address is the Roku host, host:port or URL.
	Address string
	// Password enables installing or updating rokucastor's dev channel.
	Password string
	// StartupTimeout is how long the Roku may stay in startup or buffering
	// before playback counts as failed; zero uses roku.DefaultStartupTimeout.
	StartupTimeout time.Duration
	// AppID launches an already installed channel instead of sideloading.
	AppID string
	// Video is the local file to play.
	Video string
	// Start is where playback begins, within the video's own timeline.
	Start time.Duration
	// Subtitle is an optional SRT side-loaded track.
	Subtitle string
	// Language is the subtitle language, as ISO 639-1 or ISO 639-2.
	Language string
	// GenerateSubtitles transcribes the sound live and burns the cues into the
	// video during the HLS transcode. It takes precedence over Subtitle.
	GenerateSubtitles bool
	// StreamModel is the Whisper model for live transcription; empty uses the
	// package default.
	StreamModel string
	// Layout is the per-user directory tree Whisper uses; empty resolves the
	// current user's directories.
	Layout config.Layout
}

// Session is one live cast; the caller closes it when playback is over.
type Session struct {
	Player   device.Device
	Server   *media.Server
	Playback device.Playback

	Transcoder    *transcode.Process
	TranscodeInfo *TranscodeInfo
	workDir       string
	subtitles     *liveSubtitles

	closeOnce sync.Once
	closeErr  error
}

// TranscodeInfo describes the playable output, once the first segment is ready.
type TranscodeInfo struct {
	Encoder         string
	VideoCodec      string
	VideoWidth      int
	VideoHeight     int
	AudioCodec      string
	AudioChannels   int
	AudioSampleRate int
}

// Start validates the request, connects to the Roku, publishes the files and
// launches the channel. On any failure it releases whatever it already opened.
func Start(ctx context.Context, opts Options) (*Session, error) {
	if opts.Start < 0 {
		return nil, fmt.Errorf("start position %s is before the beginning", opts.Start)
	}
	format, err := media.FormatFor(opts.Video)
	if err != nil {
		return nil, err
	}
	subtitle := strings.TrimSpace(opts.Subtitle)
	if opts.GenerateSubtitles {
		// Generated cues are burned into the picture; a side-loaded track would
		// duplicate them.
		subtitle = ""
	}
	language := ""
	if subtitle != "" || opts.GenerateSubtitles {
		if language, err = media.LanguageCode(opts.Language); err != nil {
			return nil, err
		}
	}
	plan, err := transcode.Inspect(ctx, opts.Video)
	if err != nil {
		return nil, err
	}
	if opts.GenerateSubtitles {
		if !plan.HasAudio {
			return nil, fmt.Errorf("subtitles cannot be generated: the video has no audio track")
		}
		// The cue text is part of the picture and the audio must share its
		// timestamps, so neither track can be copied.
		plan.CopyVideo = false
		plan.CopyAudio = false
	}
	if plan.Duration > 0 && opts.Start >= plan.Duration {
		return nil, fmt.Errorf("start position %s is at or past the video's length %s", opts.Start.Truncate(time.Second), plan.Duration.Truncate(time.Second))
	}
	slog.InfoContext(ctx, "Roku transcode plan",
		"video_codec", plan.VideoCodec,
		"video_profile", plan.VideoProfile,
		"video_level", plan.VideoLevel,
		"video_width", plan.Width,
		"video_height", plan.Height,
		"video_pixel_format", plan.PixelFormat,
		"video_field_order", plan.FieldOrder,
		"copy_video", plan.CopyVideo,
		"audio_codec", plan.AudioCodec,
		"audio_profile", plan.AudioProfile,
		"audio_channels", plan.AudioChannels,
		"audio_sample_rate", plan.AudioSampleRate,
		"audio_default", plan.AudioDefault,
		"copy_audio", plan.CopyAudio,
		"transcode", plan.NeedsTranscode(),
	)

	ffmpegPath := ""
	videoEncoder := transcode.Encoder{}
	workDir := ""
	if plan.NeedsTranscode() {
		ffmpegPath, err = exec.LookPath("ffmpeg")
		if err != nil {
			return nil, fmt.Errorf("finding ffmpeg on PATH: %w", err)
		}
		if !plan.CopyVideo {
			videoEncoder, err = transcode.SelectEncoder(ctx, ffmpegPath)
			if err != nil {
				return nil, err
			}
			slog.InfoContext(ctx, "selected Roku H.264 encoder", "encoder", videoEncoder.Name())
		}
		workDir, err = os.MkdirTemp("", "rokucastor-transcode-")
		if err != nil {
			return nil, fmt.Errorf("creating a transcode work directory: %w", err)
		}
	}
	if opts.GenerateSubtitles && opts.Layout.Root == "" {
		opts.Layout, err = config.Paths()
		if err != nil {
			if workDir != "" {
				_ = os.RemoveAll(workDir)
			}
			return nil, err
		}
	}

	family := roku.Family{Config: roku.Config{
		AppID:          opts.AppID,
		Password:       opts.Password,
		StartupTimeout: opts.StartupTimeout,
	}}
	player, err := family.ConnectAddress(ctx, opts.Address)
	if err != nil {
		if workDir != "" {
			_ = os.RemoveAll(workDir)
		}
		return nil, err
	}
	server, err := media.New(opts.Address)
	if err != nil {
		_ = player.Close()
		if workDir != "" {
			_ = os.RemoveAll(workDir)
		}
		return nil, err
	}

	s := &Session{Player: player, Server: server, workDir: workDir}
	started := false
	defer func() {
		if !started {
			_ = s.Close()
		}
	}()

	if opts.GenerateSubtitles {
		live, startErr := startLiveSubtitles(ctx, ffmpegPath, opts.Video, opts.Start, opts.StreamModel, opts.Language, opts.Layout, workDir)
		if startErr != nil {
			return nil, startErr
		}
		s.subtitles = live
		// Give the encoder a head start of settled speech, so the first frames
		// it serves already have their caption available.
		live.awaitLead(ctx)
	}

	playback := device.Playback{Format: format.Stream}
	if workDir == "" {
		playback.Start = opts.Start
		playback.URL, err = server.Video(opts.Video)
	} else {
		playback.Format = "hls"
		playback.URL, err = server.PublishHLS(workDir)
		if err == nil {
			var streamOptions []transcode.StreamOptions
			if s.subtitles != nil {
				streamOptions = append(streamOptions, transcode.StreamOptions{
					CueFile:  s.subtitles.path,
					Pace:     encodePace,
					Progress: s.subtitles.writer,
				})
			}
			s.Transcoder, err = transcode.Start(ctx, ffmpegPath, opts.Video, plan, videoEncoder, workDir, opts.Start, server.ReportError, streamOptions...)
		}
		if err == nil {
			var outputPlan transcode.Plan
			outputPlan, err = s.Transcoder.WaitReady(ctx, workDir, plan.HasAudio)
			if err == nil {
				s.TranscodeInfo = &TranscodeInfo{
					Encoder:         videoEncoder.Name(),
					VideoCodec:      outputPlan.VideoCodec,
					VideoWidth:      outputPlan.Width,
					VideoHeight:     outputPlan.Height,
					AudioCodec:      outputPlan.AudioCodec,
					AudioChannels:   outputPlan.AudioChannels,
					AudioSampleRate: outputPlan.AudioSampleRate,
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if subtitle != "" {
		track := subtitle
		if workDir != "" && opts.Start > 0 {
			// A transcode started part-way through keeps an HLS timeline that
			// begins at zero, so the side-loaded cues move with it.
			shifted := filepath.Join(workDir, "subtitles.srt")
			if err := media.ShiftSRT(subtitle, shifted, opts.Start); err != nil {
				return nil, err
			}
			track = shifted
		}
		if playback.SubtitleURL, err = server.Subtitle(track); err != nil {
			return nil, err
		}
		playback.SubtitleLanguage = language
		playback.SubtitleDescription = "Subtitles (" + strings.TrimSpace(opts.Language) + ")"
	}
	if err := player.Play(ctx, playback); err != nil {
		return nil, fmt.Errorf("starting playback: %w", err)
	}
	// The operator may have quit while the channel was launching; do not hand
	// back a session whose Roku is already playing.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.Playback = playback
	started = true
	return s, nil
}

// Close sends the Roku home first, so it lets go of the stream before the
// server stops answering. It is safe to call more than once.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		var errs []error
		if s.Player != nil {
			if err := s.Player.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if s.Transcoder != nil {
			if err := s.Transcoder.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if s.subtitles != nil {
			if err := s.subtitles.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if s.Server != nil {
			stopping, cancel := context.WithTimeout(context.Background(), closeGrace)
			defer cancel()
			if err := s.Server.Close(stopping); err != nil {
				errs = append(errs, err)
			}
		}
		if s.workDir != "" {
			if err := os.RemoveAll(s.workDir); err != nil {
				errs = append(errs, fmt.Errorf("removing transcode work directory: %w", err))
			}
		}
		s.closeErr = errors.Join(errs...)
	})
	return s.closeErr
}

func (s *Session) AwaitEnd(ctx context.Context) error {
	waiting, cancel := context.WithCancel(ctx)
	defer cancel()
	playerDone := make(chan error, 1)
	go func() { playerDone <- s.Player.AwaitEnd(waiting) }()

	select {
	case err := <-playerDone:
		return err
	case err := <-s.Server.Errors():
		cancel()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

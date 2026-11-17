package transcode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stupside/rokucastor/internal/subtitle"
)

const (
	maxWidth  = 1920
	maxHeight = 1080
)

type Plan struct {
	VideoIndex      int
	AudioIndex      int
	Width           int
	Height          int
	Duration        time.Duration
	VideoCodec      string
	VideoProfile    string
	VideoLevel      int
	PixelFormat     string
	FieldOrder      string
	HasAudio        bool
	AudioCodec      string
	AudioProfile    string
	AudioChannels   int
	AudioSampleRate int
	AudioDefault    bool
	CopyVideo       bool
	CopyAudio       bool
	Deinterlace     bool
}

func (p Plan) NeedsTranscode() bool {
	return !p.CopyVideo || p.HasAudio && !p.CopyAudio
}

type streamInfo struct {
	Index       int    `json:"index"`
	CodecType   string `json:"codec_type"`
	CodecName   string `json:"codec_name"`
	Profile     string `json:"profile"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	PixelFormat string `json:"pix_fmt"`
	Level       int    `json:"level"`
	FieldOrder  string `json:"field_order"`
	SampleRate  string `json:"sample_rate"`
	Channels    int    `json:"channels"`
	Disposition struct {
		Default     int `json:"default"`
		AttachedPic int `json:"attached_pic"`
	} `json:"disposition"`
}

type probeResult struct {
	Streams []streamInfo `json:"streams"`
	Format  struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func Inspect(ctx context.Context, path string) (Plan, error) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return Plan{}, fmt.Errorf("finding ffprobe on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_streams", "-show_format", "-of", "json", path)
	var stderr tailWriter
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return Plan{}, ctx.Err()
		}
		return Plan{}, fmt.Errorf("inspecting video with ffprobe: %w%s", err, stderr.suffix())
	}
	var result probeResult
	if err := json.Unmarshal(out, &result); err != nil {
		return Plan{}, fmt.Errorf("decoding ffprobe output: %w", err)
	}
	plan, err := planFor(result.Streams)
	if err != nil {
		return Plan{}, err
	}
	// ffprobe reports the container length in seconds; a missing or malformed
	// value leaves Duration at zero and simply skips range checks.
	if seconds, err := strconv.ParseFloat(result.Format.Duration, 64); err == nil && seconds > 0 {
		plan.Duration = time.Duration(seconds * float64(time.Second))
	}
	return plan, nil
}

func planFor(streams []streamInfo) (Plan, error) {
	var videos []streamInfo
	var audios []streamInfo
	for i := range streams {
		switch streams[i].CodecType {
		case "video":
			if streams[i].Disposition.AttachedPic == 0 {
				videos = append(videos, streams[i])
			}
		case "audio":
			audios = append(audios, streams[i])
		}
	}
	if len(videos) == 0 {
		return Plan{}, fmt.Errorf("video file has no video stream")
	}
	video := videos[0]
	for _, candidate := range videos {
		if candidate.Disposition.Default != 0 {
			video = candidate
			break
		}
	}

	plan := Plan{
		VideoIndex:   video.Index,
		Width:        video.Width,
		Height:       video.Height,
		VideoCodec:   video.CodecName,
		VideoProfile: video.Profile,
		VideoLevel:   video.Level,
		PixelFormat:  video.PixelFormat,
		FieldOrder:   video.FieldOrder,
		CopyVideo:    copyableVideo(video),
		Deinterlace:  interlaced(video.FieldOrder),
	}
	if len(audios) > 0 {
		audio := audios[0]
		for _, candidate := range audios {
			if candidate.Disposition.Default != 0 {
				audio = candidate
				break
			}
		}
		sampleRate, _ := strconv.Atoi(audio.SampleRate)
		plan.AudioIndex = audio.Index
		plan.HasAudio = true
		plan.AudioCodec = audio.CodecName
		plan.AudioProfile = audio.Profile
		plan.AudioChannels = audio.Channels
		plan.AudioSampleRate = sampleRate
		plan.AudioDefault = audio.Disposition.Default != 0
		plan.CopyAudio = strings.EqualFold(audio.CodecName, "aac") &&
			strings.EqualFold(strings.TrimSpace(audio.Profile), "LC") &&
			audio.Channels == 2 && sampleRate > 0 && sampleRate <= 48_000
		// Normalize audio alongside a video encode so HLS output has fresh AAC timestamps.
		if !plan.CopyVideo {
			plan.CopyAudio = false
		}
	}
	return plan, nil
}

func copyableVideo(v streamInfo) bool {
	profile := strings.ToLower(strings.ReplaceAll(v.Profile, " ", ""))
	profileOK := profile == "high" || profile == "main" || profile == "baseline" || profile == "constrainedbaseline"
	return strings.EqualFold(v.CodecName, "h264") &&
		profileOK &&
		v.Width > 0 && v.Width <= maxWidth &&
		v.Height > 0 && v.Height <= maxHeight &&
		v.PixelFormat == "yuv420p" &&
		v.Level > 0 && v.Level <= 42 &&
		strings.EqualFold(v.FieldOrder, "progressive")
}

func interlaced(fieldOrder string) bool {
	switch strings.ToLower(fieldOrder) {
	case "tt", "bb", "tb", "bt":
		return true
	default:
		return false
	}
}

type Encoder struct {
	name  string
	flags []string
}

func (e Encoder) Name() string { return e.name }

// StreamOptions turns the transcode into a live subtitle burn-in. The zero value
// is the fast, unpaced transcode used when no captions are generated.
type StreamOptions struct {
	// CueFile is the text drawtext renders into every frame; empty disables burn-in.
	CueFile string
	// Pace is the input's realtime multiple; zero reads as fast as possible.
	Pace float64
	// Progress receives the encoder's mux position as each report arrives.
	Progress func(time.Duration)
}

func firstStream(options []StreamOptions) StreamOptions {
	if len(options) == 0 {
		return StreamOptions{}
	}
	return options[0]
}

var encoderCache sync.Map

func SelectEncoder(ctx context.Context, ffmpeg string) (Encoder, error) {
	if cached, ok := encoderCache.Load(ffmpeg); ok {
		return cached.(Encoder), nil
	}
	candidates := []Encoder{
		{name: "h264_nvenc", flags: []string{"-preset", "p4", "-pix_fmt", "yuv420p"}},
		{name: "h264_qsv", flags: []string{"-preset", "veryfast", "-pix_fmt", "nv12"}},
		{name: "h264_amf", flags: []string{"-quality", "speed", "-pix_fmt", "nv12"}},
		{name: "libx264", flags: []string{"-preset", "veryfast", "-pix_fmt", "yuv420p"}},
	}
	for _, candidate := range candidates {
		testCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		args := []string{
			"-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "testsrc2=size=128x72:rate=25:duration=0.1",
			"-frames:v", "1", "-c:v", candidate.name,
		}
		args = append(args, candidate.flags...)
		args = append(args, "-profile:v", "high", "-level:v", "4.2", "-b:v", "4M", "-f", "null", "-")
		cmd := exec.CommandContext(testCtx, ffmpeg, args...)
		err := cmd.Run()
		cancel()
		if ctx.Err() != nil {
			return Encoder{}, ctx.Err()
		}
		if err == nil {
			actual, loaded := encoderCache.LoadOrStore(ffmpeg, candidate)
			if loaded {
				return actual.(Encoder), nil
			}
			return candidate, nil
		}
	}
	return Encoder{}, fmt.Errorf("ffmpeg has no usable H.264 encoder (tested NVENC, QSV, AMF, and libx264)")
}

func args(input string, p Plan, videoEncoder Encoder, start time.Duration, options ...StreamOptions) []string {
	stream := firstStream(options)
	burn := stream.CueFile != ""

	args := []string{"-hide_banner", "-nostdin", "-nostats", "-loglevel", "warning"}
	// Seeking before the input is fast: ffmpeg jumps to the nearest keyframe
	// instead of decoding everything it would discard. The HLS timeline then
	// starts at zero, so the channel needs no playStart for this path.
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start.Seconds(), 'f', 3, 64))
	}
	// Pacing is an input option: it stops ffmpeg reading a local file faster
	// than live speech can be transcribed into cues.
	if stream.Pace > 0 {
		args = append(args, "-readrate", formatRate(stream.Pace))
	}
	args = append(args, "-i", input)
	args = append(args, "-map", fmt.Sprintf("0:%d", p.VideoIndex))
	if p.HasAudio {
		args = append(args, "-map", fmt.Sprintf("0:%d", p.AudioIndex))
	}
	args = append(args, "-sn", "-dn")

	if p.CopyVideo && !burn {
		args = append(args, "-c:v", "copy")
	} else {
		filter := "scale=w='min(iw,1920)':h='min(ih,1080)':force_original_aspect_ratio=decrease:force_divisible_by=2"
		if p.Deinterlace {
			filter = "yadif," + filter
		}
		if burn {
			// drawtext re-reads the live cue file for every frame.
			filter += "," + subtitle.DrawtextFilter(stream.CueFile)
		}
		args = append(args, "-vf", filter, "-c:v", videoEncoder.name)
		args = append(args, videoEncoder.flags...)
		args = append(args,
			"-profile:v", "high",
			"-level:v", "4.2",
			"-b:v", "4M",
			"-maxrate", "4M",
			"-bufsize", "8M",
			"-force_key_frames", "expr:gte(t,n_forced*2)",
		)
	}
	if !p.HasAudio {
		args = append(args, "-an")
	} else if p.CopyAudio && !burn {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-b:a", "256k", "-ac", "2", "-ar", "48000")
	}
	if stream.Progress != nil || burn {
		// A burn-in needs a frequent position to advance the cue file.
		args = append(args, "-progress", "pipe:1", "-stats_period", "0.1")
	}
	return append(args,
		"-f", "hls",
		"-hls_time", "2",
		"-hls_list_size", "0",
		"-hls_playlist_type", "event",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", "seg_%05d.ts",
		"stream.m3u8",
	)
}

// formatRate spells a realtime multiple the way -readrate reads it.
func formatRate(multiple float64) string {
	text := strconv.FormatFloat(multiple, 'f', 1, 64)
	return text
}

// watchProgress parses ffmpeg's -progress feed and reports the mux position at
// the end of each block. A broken feed is drained so ffmpeg never blocks on a
// full pipe.
func watchProgress(r io.Reader, report func(time.Duration)) {
	scanner := bufio.NewScanner(r)
	var position time.Duration
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "out_time_us":
			if micros, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				position = time.Duration(micros) * time.Microsecond
			}
		case "progress":
			if report != nil {
				report(position)
			}
		}
	}
	if scanner.Err() != nil {
		_, _ = io.Copy(io.Discard, r)
	}
}

type Process struct {
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	err    error
}

func Start(ctx context.Context, ffmpeg, input string, p Plan, video Encoder, outputDir string, start time.Duration, report func(error), options ...StreamOptions) (*Process, error) {
	stream := firstStream(options)
	if stream.CueFile != "" && video.name == "" {
		return nil, fmt.Errorf("burning in subtitles needs a video encoder")
	}
	input, err := filepath.Abs(input)
	if err != nil {
		return nil, fmt.Errorf("resolving input video path: %w", err)
	}
	outputDir, err = filepath.Abs(outputDir)
	if err != nil {
		return nil, fmt.Errorf("resolving HLS output directory: %w", err)
	}
	info, err := os.Stat(outputDir)
	if err != nil || !info.IsDir() {
		if err != nil {
			return nil, fmt.Errorf("opening HLS output directory: %w", err)
		}
		return nil, fmt.Errorf("HLS output path is not a directory")
	}
	processCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(processCtx, ffmpeg, args(input, p, video, start, stream)...)
	cmd.Dir = outputDir
	var stderr tailWriter
	cmd.Stderr = &stderr
	var progress io.ReadCloser
	if stream.Progress != nil || stream.CueFile != "" {
		progress, err = cmd.StdoutPipe()
		if err != nil {
			cancel()
			return nil, fmt.Errorf("reading ffmpeg progress: %w", err)
		}
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("starting ffmpeg: %w%s", err, stderr.suffix())
	}
	if progress != nil {
		go watchProgress(progress, stream.Progress)
	}
	process := &Process{cancel: cancel, done: make(chan struct{})}
	go func() {
		waitErr := cmd.Wait()
		runErr := waitErr
		if processCtx.Err() != nil {
			runErr = processCtx.Err()
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) && stderr.suffix() != "" {
			runErr = fmt.Errorf("%w%s", runErr, stderr.suffix())
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) && report != nil {
			report(fmt.Errorf("ffmpeg transcoding failed: %w", runErr))
		}
		process.mu.Lock()
		process.err = runErr
		close(process.done)
		process.mu.Unlock()
	}()
	return process, nil
}

func (p *Process) WaitReady(ctx context.Context, outputDir string, expectAudio bool) (Plan, error) {
	timeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		finished, producerErr := p.result()
		if producerErr != nil {
			return Plan{}, fmt.Errorf("ffmpeg failed before producing a playable stream: %w", producerErr)
		}
		if segment, ready := firstHLSSegment(outputDir); ready {
			measured, err := inspectHLSOutput(timeout, outputDir, segment)
			if err == nil {
				if !measured.CopyVideo {
					return Plan{}, fmt.Errorf("transcoded output is not Roku-compatible H.264 within %dx%d: %+v", maxWidth, maxHeight, measured)
				}
				if expectAudio && (!measured.HasAudio || !measured.CopyAudio) {
					return Plan{}, fmt.Errorf("transcoded output is missing Roku-compatible AAC stereo audio: %+v", measured)
				}
				slog.InfoContext(ctx, "Roku HLS output ready",
					"video_codec", measured.VideoCodec,
					"video_profile", measured.VideoProfile,
					"video_level", measured.VideoLevel,
					"video_width", measured.Width,
					"video_height", measured.Height,
					"video_pixel_format", measured.PixelFormat,
					"audio_codec", measured.AudioCodec,
					"audio_profile", measured.AudioProfile,
					"audio_channels", measured.AudioChannels,
					"audio_sample_rate", measured.AudioSampleRate,
				)
				return measured, nil
			}
			if finished {
				return Plan{}, fmt.Errorf("validating HLS media segment: %w", err)
			}
		}
		if finished {
			return Plan{}, fmt.Errorf("ffmpeg ended without producing a playable HLS segment")
		}
		select {
		case <-timeout.Done():
			return Plan{}, fmt.Errorf("waiting for ffmpeg's playable output: %w", timeout.Err())
		case <-ticker.C:
		}
	}
}

func firstHLSSegment(dir string) (string, bool) {
	playlist, err := os.ReadFile(filepath.Join(dir, "stream.m3u8"))
	if err != nil || !strings.HasPrefix(string(playlist), "#EXTM3U") {
		return "", false
	}
	for _, line := range strings.Split(string(playlist), "\n") {
		name := strings.TrimSpace(line)
		if filepath.Base(name) != name || filepath.Ext(name) != ".ts" {
			continue
		}
		segment, err := os.Stat(filepath.Join(dir, name))
		if err == nil && segment.Size() > 0 {
			return filepath.Join(dir, name), true
		}
	}
	return "", false
}

func hlsReady(dir string) bool {
	_, ready := firstHLSSegment(dir)
	return ready
}

func inspectHLSOutput(ctx context.Context, dir, segmentPath string) (Plan, error) {
	if filepath.Ext(segmentPath) != ".ts" || filepath.Dir(segmentPath) != dir {
		return Plan{}, fmt.Errorf("HLS media segment is outside the output directory")
	}
	return Inspect(ctx, segmentPath)
}

func (p *Process) result() (bool, error) {
	select {
	case <-p.done:
		p.mu.Lock()
		defer p.mu.Unlock()
		return true, p.err
	default:
		return false, nil
	}
}

func (p *Process) Close() error {
	p.cancel()
	<-p.done
	p.mu.Lock()
	err := p.err
	p.mu.Unlock()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (p *Process) Wait(ctx context.Context) error {
	select {
	case <-p.done:
		p.mu.Lock()
		err := p.err
		p.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type tailWriter struct{ data []byte }

func (w *tailWriter) Write(p []byte) (int, error) {
	const limit = 4096
	w.data = append(w.data, p...)
	if len(w.data) > limit {
		w.data = append(w.data[:0], w.data[len(w.data)-limit:]...)
	}
	return len(p), nil
}

func (w *tailWriter) suffix() string {
	text := strings.TrimSpace(string(w.data))
	if text == "" {
		return ""
	}
	return ": " + text
}

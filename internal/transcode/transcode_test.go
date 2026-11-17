package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func supportedVideo() streamInfo {
	return streamInfo{
		Index: 0, CodecType: "video", CodecName: "h264", Profile: "High",
		Width: 1920, Height: 1080, PixelFormat: "yuv420p", Level: 42, FieldOrder: "progressive",
	}
}

func supportedAudio() streamInfo {
	return streamInfo{
		Index: 1, CodecType: "audio", CodecName: "aac", Profile: "LC",
		SampleRate: "48000", Channels: 2,
	}
}

func TestPlanUsesRokuCompatibleTracksAndNormalizesHLSAudio(t *testing.T) {
	tests := []struct {
		name        string
		video       streamInfo
		audio       *streamInfo
		copyVideo   bool
		copyAudio   bool
		hasAudio    bool
		deinterlace bool
	}{
		{name: "supported tracks", video: supportedVideo(), audio: audioPointer(supportedAudio()), copyVideo: true, copyAudio: true, hasAudio: true},
		{name: "nonstandard width", video: width(supportedVideo(), 1992), audio: audioPointer(supportedAudio()), hasAudio: true},
		{name: "level above Roku ceiling", video: level(supportedVideo(), 50), audio: audioPointer(supportedAudio()), hasAudio: true},
		{name: "unsupported audio", video: supportedVideo(), audio: audioPointer(audioCodec(supportedAudio(), "opus")), copyVideo: true, hasAudio: true},
		{name: "no audio", video: supportedVideo(), copyVideo: true},
		{name: "interlaced video", video: fieldOrder(supportedVideo(), "tt"), audio: audioPointer(supportedAudio()), hasAudio: true, deinterlace: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streams := []streamInfo{tt.video}
			if tt.audio != nil {
				streams = append(streams, *tt.audio)
			}
			got, err := planFor(streams)
			if err != nil {
				t.Fatal(err)
			}
			if got.CopyVideo != tt.copyVideo || got.CopyAudio != tt.copyAudio ||
				got.HasAudio != tt.hasAudio || got.Deinterlace != tt.deinterlace {
				t.Fatalf("plan = %+v", got)
			}
		})
	}
}

func TestPlanChoosesDefaultAudioTrack(t *testing.T) {
	first := audioCodec(supportedAudio(), "ac3")
	defaultTrack := supportedAudio()
	defaultTrack.Index = 3
	defaultTrack.Disposition.Default = 1
	got, err := planFor([]streamInfo{supportedVideo(), first, defaultTrack})
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasAudio || got.AudioIndex != 3 || !got.CopyAudio {
		t.Fatalf("plan = %+v, want the default AAC track", got)
	}
}

func TestPlanSkipsAttachedCoverArt(t *testing.T) {
	cover := supportedVideo()
	cover.Index = 0
	cover.Width, cover.Height = 600, 600
	cover.Disposition.AttachedPic = 1
	main := supportedVideo()
	main.Index = 1
	got, err := planFor([]streamInfo{cover, main})
	if err != nil {
		t.Fatal(err)
	}
	if got.VideoIndex != 1 || !got.CopyVideo {
		t.Fatalf("plan = %+v, want the main video stream", got)
	}
}

func TestArgsEncodeOnlyTheIncompatibleTracks(t *testing.T) {
	plan := Plan{VideoIndex: 0, AudioIndex: 2, HasAudio: true, CopyVideo: true, CopyAudio: false}
	args := args("input file.mkv", plan, Encoder{}, 0)
	assertContains(t, args,
		"-map", "0:0", "-map", "0:2", "-c:v", "copy", "-c:a", "aac", "-ac", "2", "-ar", "48000",
		"-f", "hls", "-hls_time", "2", "-hls_playlist_type", "event",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_segment_type", "mpegts", "seg_%05d.ts", "stream.m3u8",
	)
}

func TestArgsSeekBeforeTheInput(t *testing.T) {
	got := args("input.mkv", Plan{VideoIndex: 0}, Encoder{}, time.Hour+12*time.Minute+30*time.Second)
	seek := -1
	for i, arg := range got {
		if arg == "-ss" {
			seek = i
			break
		}
	}
	if seek < 0 || seek+1 >= len(got) || got[seek+1] != "4350.000" {
		t.Fatalf("arguments %q do not seek to 1:12:30", got)
	}
	input := -1
	for i, arg := range got {
		if arg == "-i" {
			input = i
			break
		}
	}
	if input < 0 || input < seek {
		t.Fatalf("arguments %q seek after the input", got)
	}
	// Starting at the beginning must not add a seek at all.
	for _, arg := range args("input.mkv", Plan{VideoIndex: 0}, Encoder{}, 0) {
		if arg == "-ss" {
			t.Fatal("a cast from the beginning added a seek")
		}
	}
}

func TestArgsScalesAndDeinterlacesVideoWhenNeeded(t *testing.T) {
	args := args("input.mkv", Plan{VideoIndex: 0, Deinterlace: true}, Encoder{name: "libx264", flags: []string{"-preset", "veryfast", "-pix_fmt", "yuv420p"}}, 0)
	assertContains(t, args,
		"-vf", "yadif,scale=w='min(iw,1920)':h='min(ih,1080)':force_original_aspect_ratio=decrease:force_divisible_by=2",
		"-c:v", "libx264", "-force_key_frames", "expr:gte(t,n_forced*2)",
	)
}

func TestTranscodeProducesPlayableHLSWithH264AndAAC(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	input := filepath.Join(t.TempDir(), "source.mkv")
	makeSource := exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=2560x1440:rate=25:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "mpeg4", "-q:v", "4", "-c:a", "libmp3lame", "-b:a", "128k",
		input,
	)
	if output, err := makeSource.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot create the integration fixture: %v: %s", err, output)
	}
	plan, err := Inspect(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Duration <= 0 {
		t.Fatalf("source plan = %+v, want the container duration", plan)
	}
	if !plan.NeedsTranscode() || plan.CopyVideo || plan.CopyAudio {
		t.Fatalf("source plan = %+v, want both tracks transcoded", plan)
	}
	encoder, err := SelectEncoder(ctx, ffmpeg)
	if err != nil {
		t.Fatal(err)
	}

	outputDir := t.TempDir()
	process, err := Start(ctx, ffmpeg, input, plan, encoder, outputDir, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := process.WaitReady(ctx, outputDir, plan.HasAudio); err != nil {
		_ = process.Close()
		t.Fatal(err)
	}
	if err := process.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}

	segment, ready := firstHLSSegment(outputDir)
	if !ready {
		t.Fatal("completed HLS output has no media segment")
	}
	outputPlan, err := inspectHLSOutput(ctx, outputDir, segment)
	if err != nil {
		t.Fatal(err)
	}
	if !outputPlan.CopyVideo || !outputPlan.HasAudio || !outputPlan.CopyAudio ||
		outputPlan.Width > maxWidth || outputPlan.Height > maxHeight {
		t.Fatalf("transcoded output plan = %+v, want Roku-compatible H.264/AAC within %dx%d", outputPlan, maxWidth, maxHeight)
	}
	if outputPlan.VideoIndex < 0 {
		t.Fatalf("invalid output video index: %+v", outputPlan)
	}
	playlist, err := os.ReadFile(filepath.Join(outputDir, "stream.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(playlist), "#EXT-X-ENDLIST") ||
		!strings.Contains(string(playlist), "seg_00000.ts") {
		t.Fatalf("completed HLS playlist = %q", playlist)
	}
	packets, err := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "packet=size",
		"-of", "csv=p=0",
		filepath.Join(outputDir, "stream.m3u8"),
	).Output()
	if err != nil {
		t.Fatalf("probing HLS audio packets: %v", err)
	}
	if len(strings.TrimSpace(string(packets))) == 0 {
		t.Fatal("completed HLS output has no audio packets")
	}
}

func TestHLSReadinessRequiresAMediaSegment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stream.m3u8"), []byte("#EXTM3U\n#EXTINF:2,\nseg_00000.ts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if hlsReady(dir) {
		t.Fatal("HLS became ready before its segment existed")
	}
	if err := os.WriteFile(filepath.Join(dir, "seg_00000.ts"), []byte("segment"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hlsReady(dir) {
		t.Fatal("HLS was not ready after its first segment was written")
	}
}

func TestPlanForRejectsAudioOnlyInput(t *testing.T) {
	if _, err := planFor([]streamInfo{{Index: 0, CodecType: "audio", CodecName: "aac"}}); err == nil {
		t.Fatal("plan accepted audio-only input")
	}
}

func TestInspectPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Inspect(ctx, "missing.mp4"); err == nil {
		t.Fatal("Inspect accepted a canceled context")
	}
}

func TestArgsBurnsLiveCuesAndPacesTheRead(t *testing.T) {
	plan := Plan{VideoIndex: 0, AudioIndex: 1, HasAudio: true, CopyVideo: true, CopyAudio: true}
	got := args("input.mp4", plan, Encoder{name: "libx264", flags: []string{"-preset", "veryfast"}}, 0,
		StreamOptions{CueFile: `C:\work\cue.txt`, Pace: 1.1})
	assertContains(t, got,
		"-readrate", "1.1",
		"-c:v", "libx264", "-c:a", "aac",
		"-progress", "pipe:1", "-stats_period", "0.1",
	)
	if strings.Contains(strings.Join(got, " "), "drawtext=textfile=") == false {
		t.Errorf("arguments %q do not render the cue file", got)
	}
	for i := 0; i+1 < len(got); i++ {
		if got[i] == "-readrate" && got[i+1] == "1.1" {
			if input := indexOf(got, "-i"); input >= 0 && input < i {
				t.Errorf("arguments %q pace after the input", got)
			}
		}
		if got[i] == "-c:v" && got[i+1] == "copy" {
			t.Errorf("burn-in copied video instead of re-encoding: %q", got)
		}
	}
}

func TestStartRefusesBurnInWithoutAnEncoder(t *testing.T) {
	_, err := Start(t.Context(), "ffmpeg", "input.mp4", Plan{VideoIndex: 0}, Encoder{}, t.TempDir(), 0, nil,
		StreamOptions{CueFile: "cue.txt"})
	if err == nil || !strings.Contains(err.Error(), "video encoder") {
		t.Fatalf("Start = %v, want the missing-encoder refusal", err)
	}
}

func TestWatchProgressReportsTheMuxPositionOfEachBlock(t *testing.T) {
	feed := strings.NewReader("out_time_us=1500000\nprogress=continue\nout_time_us=3000000\nspeed=1.1x\nprogress=end\n")
	var got []time.Duration
	watchProgress(feed, func(position time.Duration) { got = append(got, position) })
	if len(got) != 2 || got[0] != 1500*time.Millisecond || got[1] != 3*time.Second {
		t.Fatalf("positions = %v, want 1.5s and 3s", got)
	}
}

func indexOf(args []string, want string) int {
	for i, arg := range args {
		if arg == want {
			return i
		}
	}
	return -1
}

func audioPointer(v streamInfo) *streamInfo { return &v }

func width(v streamInfo, width int) streamInfo {
	v.Width = width
	return v
}

func level(v streamInfo, level int) streamInfo {
	v.Level = level
	return v
}

func audioCodec(v streamInfo, codec string) streamInfo {
	v.CodecName = codec
	return v
}

func fieldOrder(v streamInfo, fieldOrder string) streamInfo {
	v.FieldOrder = fieldOrder
	return v
}

func assertContains(t *testing.T, got []string, want ...string) {
	t.Helper()
	for _, item := range want {
		found := false
		for _, candidate := range got {
			if candidate == item {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("arguments %q do not contain %q", got, item)
		}
	}
}

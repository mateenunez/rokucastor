package whisper

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stupside/rokucastor/internal/config"
	"github.com/stupside/rokucastor/internal/media"
	"github.com/stupside/rokucastor/internal/subtitle"
)

const (
	// serverReadyTimeout bounds model loading, which is the slow part of a
	// first start; downloads happen before the process is launched.
	serverReadyTimeout = 90 * time.Second

	// inferenceTimeout bounds one live recognition. A three-second window with
	// the tiny model takes about a second on CPU; a much slower machine should
	// fail this window rather than stall the feed forever.
	inferenceTimeout = 30 * time.Second
)

// StreamOptions selects the model and language for live transcription.
type StreamOptions struct {
	// Layout is the per-user directory tree; Ensure may be called on it.
	Layout config.Layout
	// Model is a Whisper model name; empty uses DefaultStreamModel.
	Model string
	// Language is the spoken language, as ISO 639-1 or ISO 639-2.
	Language string
	// HTTP downloads the tool and models; nil uses http.DefaultClient.
	HTTP *http.Client
	// FFmpeg is the ffmpeg executable; empty uses "ffmpeg" on PATH.
	FFmpeg string
	// Progress receives download updates; nil is silent.
	Progress func(Progress)
}

// Server is a running whisper-server that recognizes windows of mono 16 kHz
// samples for live subtitles. It implements subtitle.Recognizer.
type Server struct {
	cmd      *exec.Cmd
	url      string
	client   *http.Client
	language string

	inferMu sync.Mutex
	mu      sync.Mutex
	err     error
	done    chan struct{}

	cancel context.CancelFunc
	once   sync.Once
	tail   *lockedTail
}

// Stream downloads what it needs and starts whisper-server with Silero VAD. The
// recognizer is warmed up before it returns so a broken installation fails the
// cast instead of the first caption.
func Stream(ctx context.Context, opts StreamOptions) (*Server, error) {
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = DefaultStreamModel
	}
	if !ValidModel(model) {
		return nil, fmt.Errorf("unknown streaming Whisper model %q: use %q, %q, %q or %q", model, ModelTiny, ModelTinyEN, ModelBase, ModelSmall)
	}
	language, err := media.WhisperCode(opts.Language)
	if err != nil {
		return nil, err
	}
	if err := opts.Layout.Ensure(); err != nil {
		return nil, err
	}

	client := opts.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	ffmpeg := opts.FFmpeg
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	install := &installer{
		layout:   opts.Layout,
		http:     client,
		ffmpeg:   ffmpeg,
		progress: opts.Progress,
	}
	_, serverPath, err := install.tools(ctx)
	if err != nil {
		return nil, err
	}
	modelPath, err := install.model(ctx, model)
	if err != nil {
		return nil, err
	}
	vadPath, err := install.vadModel(ctx)
	if err != nil {
		return nil, err
	}

	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("finding a local port for whisper-server: %w", err)
	}
	host := "127.0.0.1"
	processCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(processCtx, serverPath, serverArgs(modelPath, vadPath, host, port)...)
	tail := &lockedTail{}
	cmd.Stdout = io.Discard
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("starting whisper-server: %w%s", err, tail.suffix())
	}

	server := &Server{
		cmd:      cmd,
		url:      "http://" + net.JoinHostPort(host, strconv.Itoa(port)),
		client:   client,
		language: language,
		done:     make(chan struct{}),
		cancel:   cancel,
		tail:     tail,
	}
	go server.reap()

	if err := server.awaitReady(ctx); err != nil {
		_ = server.Close()
		return nil, err
	}
	if err := server.warm(ctx); err != nil {
		_ = server.Close()
		return nil, err
	}
	return server, nil
}

// serverArgs loads the model and the Silero VAD model once, and keeps verbose
// JSON output small. Requests restore the language and timestamps they need.
func serverArgs(model, vad, host string, port int) []string {
	return []string{
		"-m", model,
		"--vad", "-vm", vad,
		"-nlp",
		"-t", strconv.Itoa(min(runtime.NumCPU(), maxThreads)),
		"--host", host,
		"--port", strconv.Itoa(port),
	}
}

func (s *Server) reap() {
	err := s.cmd.Wait()
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
	close(s.done)
}

func (s *Server) awaitReady(ctx context.Context) error {
	deadline := time.Now().Add(serverReadyTimeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		select {
		case <-s.done:
			return fmt.Errorf("whisper-server stopped before it was ready: %w%s", s.failure(), s.tail.suffix())
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("whisper-server did not become ready within %s%s", serverReadyTimeout, s.tail.suffix())
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url+"/health", nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// warm runs an empty inference. It forces the model to finish loading and makes
// a first-use failure visible before the cast starts serving video.
func (s *Server) warm(ctx context.Context) error {
	if _, err := s.Recognize(ctx, make([]float32, subtitle.SampleRate), 0, ""); err != nil {
		return fmt.Errorf("whisper-server could not transcribe: %w", err)
	}
	return nil
}

// Recognize posts a WAV window and returns its token-timed words, shifted by
// offset seconds into the cast's HLS timeline.
func (s *Server) Recognize(ctx context.Context, samples []float32, offset float64, prompt string) ([]subtitle.Word, error) {
	s.inferMu.Lock()
	defer s.inferMu.Unlock()

	select {
	case <-s.done:
		return nil, fmt.Errorf("whisper-server is not running: %w%s", s.failure(), s.tail.suffix())
	default:
	}

	ctx, cancel := context.WithTimeout(ctx, inferenceTimeout)
	defer cancel()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", "audio.wav")
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(wav(samples)); err != nil {
		return nil, err
	}
	for _, field := range []struct{ key, value string }{
		{"response_format", "verbose_json"},
		{"token_timestamps", "true"},
		{"split_on_word", "true"},
		{"language", s.language},
		{"temperature", "0.0"},
		{"prompt", prompt},
	} {
		if field.value == "" {
			continue
		}
		if err := form.WriteField(field.key, field.value); err != nil {
			return nil, err
		}
	}
	if err := form.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url+"/inference", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling whisper-server: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading whisper-server's answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("whisper-server answered %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	return parseWords(payload, offset)
}

type inferenceResponse struct {
	Segments []struct {
		Words []struct {
			Word  string  `json:"word"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"words"`
	} `json:"segments"`
}

// parseWords flattens verbose_json token data and moves it onto the cast's
// timeline. Whitespace-only tokens carry no caption text.
func parseWords(payload []byte, offset float64) ([]subtitle.Word, error) {
	var result inferenceResponse
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("decoding whisper-server output: %w", err)
	}
	var words []subtitle.Word
	for _, segment := range result.Segments {
		for _, token := range segment.Words {
			text := strings.TrimSpace(token.Word)
			if text == "" {
				continue
			}
			words = append(words, subtitle.Word{
				Start: offset + token.Start,
				End:   offset + token.End,
				Text:  text,
			})
		}
	}
	return words, nil
}

// wav wraps mono float32 samples as the 16-bit PCM WAV whisper-server reads.
func wav(samples []float32) []byte {
	data := make([]byte, 44+len(samples)*2)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(data[22:24], 1) // mono
	binary.LittleEndian.PutUint32(data[24:28], subtitle.SampleRate)
	binary.LittleEndian.PutUint32(data[28:32], subtitle.SampleRate*2)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(len(samples)*2))
	for i, sample := range samples {
		sample = max(-1, min(1, sample))
		binary.LittleEndian.PutUint16(data[44+i*2:], uint16(int16(sample*32767)))
	}
	return data
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func (s *Server) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		return errors.New("process exited")
	}
	return s.err
}

// Close stops the server and waits for the process to go. It is safe to call
// more than once.
func (s *Server) Close() error {
	var err error
	s.once.Do(func() {
		s.cancel()
		<-s.done
		s.mu.Lock()
		runErr := s.err
		s.mu.Unlock()
		if runErr != nil && !errors.Is(runErr, context.Canceled) && !isExitFromCancel(runErr) {
			err = fmt.Errorf("whisper-server: %w", runErr)
		}
	})
	slog.Debug("whisper-server stopped")
	return err
}

// isExitFromCancel covers Windows reporting a killed child as an exit status
// rather than a context error.
func isExitFromCancel(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && !exit.Success()
}

// lockedTail keeps the end of the server's stderr safe for the process writer
// to share with the readiness check.
type lockedTail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *lockedTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailLimit {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-tailLimit:]...)
	}
	return len(p), nil
}

func (t *lockedTail) suffix() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	text := strings.TrimSpace(string(t.buf))
	if text == "" {
		return ""
	}
	return ": " + text
}

package media

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 2 * time.Minute
)

// Server publishes files and progressive streams over HTTP on the interface
// that reaches a device. Each route gets an unguessable URL.
type Server struct {
	http   *http.Server
	base   *url.URL
	done   chan struct{}
	errors chan error

	mu       sync.Mutex
	routes   map[string]route
	serveErr error

	closeOnce sync.Once
	closeErr  error
}

type route struct {
	path        string
	contentType string
	progressive *Progressive
	hlsDir      string
}

// New listens on the local IPv4 address that routes to device and serves from
// there. The device may be a host, host:port or URL.
func New(device string) (*Server, error) {
	ip, err := routeIP(device)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		return nil, fmt.Errorf("opening a device-reachable media server: %w", err)
	}
	s := &Server{
		base:   &url.URL{Scheme: "http", Host: listener.Addr().String()},
		done:   make(chan struct{}),
		errors: make(chan error, 1),
		routes: make(map[string]route),
	}
	s.http = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	slog.Info("media server listening", "address", s.base.Host)
	go func() {
		err := s.http.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.fail(err)
		}
		close(s.done)
	}()
	return s, nil
}

// Errors reports the first server or media-production failure. It is never
// closed, so a listener still sees one event with a canceled context.
func (s *Server) Errors() <-chan error { return s.errors }

func (s *Server) fail(err error) {
	s.report(fmt.Errorf("serving media: %w", err))
}

func (s *Server) ReportError(err error) {
	if err != nil {
		s.report(fmt.Errorf("producing media: %w", err))
	}
}

func (s *Server) report(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.serveErr != nil {
		return
	}
	s.serveErr = err
	s.errors <- s.serveErr
}

// Video publishes a local video and returns the URL the Roku can fetch. The
// URL carries the container Roku should expect, not the file's extension.
func (s *Server) Video(path string) (*url.URL, error) {
	format, err := FormatFor(path)
	if err != nil {
		return nil, err
	}
	return s.publish(path, "/v/", format.Ext, format.MIME)
}

func (s *Server) PublishProgressive(stream *Progressive) (*url.URL, error) {
	if stream == nil || stream.Path() == "" {
		return nil, fmt.Errorf("progressive media stream is required")
	}
	return s.publishProgressive(stream)
}

func (s *Server) PublishHLS(dir string) (*url.URL, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("opening HLS output directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("HLS output path is not a directory")
	}
	token, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("generating a media URL: %w", err)
	}
	prefix := "/h/" + token + "/"
	s.mu.Lock()
	s.routes[prefix] = route{hlsDir: dir}
	s.mu.Unlock()

	u := *s.base
	u.Path = prefix + "stream.m3u8"
	return &u, nil
}

func (s *Server) publishProgressive(stream *Progressive) (*url.URL, error) {
	token, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("generating a media URL: %w", err)
	}
	routePath := "/v/" + token + ".mp4"
	s.mu.Lock()
	s.routes[routePath] = route{path: stream.Path(), contentType: "video/mp4", progressive: stream}
	s.mu.Unlock()
	u := *s.base
	u.Path = routePath
	return &u, nil
}

// Subtitle publishes a generated SRT and returns the URL for a subtitle track.
func (s *Server) Subtitle(path string) (*url.URL, error) {
	if !strings.EqualFold(filepath.Ext(path), ".srt") {
		return nil, fmt.Errorf("subtitles must be an SRT file, not %q", filepath.Base(path))
	}
	return s.publish(path, "/s/", ".srt", SubtitleMIME)
}

func (s *Server) publish(path, prefix, ext, contentType string) (*url.URL, error) {
	path, err := regularFile(path)
	if err != nil {
		return nil, err
	}
	token, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("generating a media URL: %w", err)
	}
	routePath := prefix + token + ext

	s.mu.Lock()
	s.routes[routePath] = route{path: path, contentType: contentType}
	s.mu.Unlock()

	u := *s.base
	u.Path = routePath
	return &u, nil
}

// ServeHTTP answers only the published paths, with byte ranges so the Roku can
// seek inside the file.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	logged := &responseLogger{ResponseWriter: w}
	fileName := "unpublished"
	fileSize := int64(-1)
	defer func() {
		slog.InfoContext(r.Context(), "media HTTP request",
			"method", r.Method,
			"file", fileName,
			"file_size", fileSize,
			"range", r.Header.Get("Range"),
			"content_range", logged.Header().Get("Content-Range"),
			"status", logged.status(),
			"bytes", logged.bytes,
			"duration", time.Since(started),
			"write_error", logged.writeErr,
		)
	}()

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		logged.Header().Set("Allow", "GET, HEAD")
		http.Error(logged, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	published, ok := s.routes[r.URL.Path]
	relative := ""
	if !ok {
		for prefix, candidate := range s.routes {
			if candidate.hlsDir != "" && strings.HasPrefix(r.URL.Path, prefix) {
				published = candidate
				relative = strings.TrimPrefix(r.URL.Path, prefix)
				ok = true
				break
			}
		}
	}
	s.mu.Unlock()
	if !ok {
		http.NotFound(logged, r)
		return
	}
	if published.hlsDir != "" {
		if relative == "" || filepath.Base(relative) != relative {
			http.NotFound(logged, r)
			return
		}
		contentType, ok := hlsArtifactContentType(relative)
		if !ok {
			http.NotFound(logged, r)
			return
		}
		fileName = relative
		path := filepath.Join(published.hlsDir, relative)
		file, err := os.Open(path)
		if err != nil {
			http.NotFound(logged, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(logged, r)
			return
		}
		fileSize = info.Size()
		logged.Header().Set("Content-Type", contentType)
		logged.Header().Set("Cache-Control", "no-store")
		http.ServeContent(logged, r, fileName, info.ModTime(), file)
		return
	}
	fileName = filepath.Base(published.path)
	if published.progressive != nil {
		logged.Header().Set("Cache-Control", "no-store")
		published.progressive.ServeHTTP(logged, r)
		fileSize, _, _ = published.progressive.Snapshot()
		return
	}

	file, err := os.Open(published.path)
	if err != nil {
		http.Error(logged, "media is no longer available", http.StatusNotFound)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		http.Error(logged, "media is no longer available", http.StatusNotFound)
		return
	}
	fileSize = info.Size()
	logged.Header().Set("Content-Type", published.contentType)
	logged.Header().Set("Cache-Control", "no-store")
	http.ServeContent(logged, r, fileName, info.ModTime(), file)
}

func hlsArtifactContentType(name string) (string, bool) {
	switch name {
	case "stream.m3u8":
		return "application/vnd.apple.mpegurl", true
	}
	if strings.HasPrefix(name, "seg_") && strings.HasSuffix(name, ".ts") {
		return "video/mp2t", true
	}
	return "", false
}

type responseLogger struct {
	http.ResponseWriter
	statusCode int
	bytes      int64
	writeErr   error
}

func (w *responseLogger) WriteHeader(statusCode int) {
	if w.statusCode == 0 {
		w.statusCode = statusCode
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseLogger) Write(data []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	if err != nil {
		w.writeErr = err
	}
	return n, err
}

func (w *responseLogger) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseLogger) status() int {
	if w.statusCode == 0 {
		return http.StatusOK
	}
	return w.statusCode
}

// Close stops serving. It is safe to call more than once.
func (s *Server) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		shutdownErr := s.http.Shutdown(ctx)
		if shutdownErr != nil {
			// A device may still hold a long download open; stop it now rather
			// than leave the listener behind.
			_ = s.http.Close()
		}
		<-s.done

		s.mu.Lock()
		serveErr := s.serveErr
		s.mu.Unlock()

		switch {
		case shutdownErr != nil && !errors.Is(shutdownErr, context.Canceled):
			s.closeErr = fmt.Errorf("stopping the media server: %w", shutdownErr)
		case serveErr != nil:
			s.closeErr = serveErr
		}
	})
	return s.closeErr
}

func regularFile(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", path, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("opening %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", path)
	}
	return absolute, nil
}

func newToken() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

// routeIP finds the local address that reaches device, so the Roku is handed a
// URL on its own network instead of 127.0.0.1 or a second interface.
func routeIP(device string) (string, error) {
	host, port, err := deviceEndpoint(device)
	if err != nil {
		return "", err
	}
	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, port), 2*time.Second)
	if err != nil {
		return "", fmt.Errorf("finding the network interface for device %q: %w", device, err)
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP.To4() == nil || addr.IP.IsUnspecified() {
		return "", fmt.Errorf("no IPv4 route to device %q", device)
	}
	return addr.IP.String(), nil
}

func deviceEndpoint(address string) (string, string, error) {
	if strings.Contains(address, "://") {
		u, err := url.Parse(address)
		if err != nil || u.Hostname() == "" {
			return "", "", fmt.Errorf("invalid device address %q", address)
		}
		if port := u.Port(); port != "" {
			return u.Hostname(), port, nil
		}
		return u.Hostname(), "9", nil
	}
	if host, port, err := net.SplitHostPort(address); err == nil {
		return host, port, nil
	}
	if ip := net.ParseIP(strings.Trim(address, "[]")); ip != nil {
		return ip.String(), "9", nil
	}
	if address != "" && !strings.ContainsAny(address, " /\\") {
		return address, "9", nil
	}
	return "", "", fmt.Errorf("cannot determine a network address from device %q", address)
}

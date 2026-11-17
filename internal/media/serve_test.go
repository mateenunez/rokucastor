package media

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testServer() *Server {
	return &Server{
		base:   &url.URL{Scheme: "http", Host: "media.test"},
		routes: make(map[string]route),
	}
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func get(t *testing.T, client *http.Client, url string, rangeHeader string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(body)
}

func TestPublishedVideoServesBytesAndRanges(t *testing.T) {
	s := testServer()
	path := writeFile(t, "movie.mp4", "0123456789")
	u, err := s.Video(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.Path, "/v/") || !strings.HasSuffix(u.Path, ".mp4") {
		t.Errorf("video URL = %q, want a tokenized .mp4 path", u.Path)
	}

	ts := httptest.NewServer(s)
	defer ts.Close()

	status, headers, body := get(t, ts.Client(), ts.URL+u.Path, "")
	if status != http.StatusOK || body != "0123456789" {
		t.Errorf("full request = %d %q, want 200 with the file", status, body)
	}
	if got := headers.Get("Content-Type"); got != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", got)
	}

	status, _, body = get(t, ts.Client(), ts.URL+u.Path, "bytes=2-5")
	if status != http.StatusPartialContent || body != "2345" {
		t.Errorf("range request = %d %q, want 206 %q", status, body, "2345")
	}
}

func TestPublishedHLSServesPlaylistsAndSegments(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"stream.m3u8":  "#EXTM3U\n#EXTINF:2,\nseg_00000.ts\n",
		"seg_00000.ts": "0123456789",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	s := testServer()
	u, err := s.PublishHLS(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.Path, "/h/") || !strings.HasSuffix(u.Path, "/stream.m3u8") {
		t.Fatalf("HLS URL = %q, want a tokenized playlist path", u.Path)
	}

	ts := httptest.NewServer(s)
	defer ts.Close()

	status, headers, body := get(t, ts.Client(), ts.URL+u.Path, "")
	if status != http.StatusOK || !strings.Contains(body, "seg_00000.ts") {
		t.Fatalf("playlist = %d %q", status, body)
	}
	if got := headers.Get("Content-Type"); got != "application/vnd.apple.mpegurl" {
		t.Errorf("playlist Content-Type = %q", got)
	}

	segmentURL := ts.URL + strings.TrimSuffix(u.Path, "stream.m3u8") + "seg_00000.ts"
	status, headers, body = get(t, ts.Client(), segmentURL, "bytes=2-5")
	if status != http.StatusPartialContent || body != "2345" {
		t.Errorf("segment range = %d %q, want 206 %q", status, body, "2345")
	}
	if got := headers.Get("Content-Type"); got != "video/mp2t" {
		t.Errorf("segment Content-Type = %q", got)
	}

	status, _, _ = get(t, ts.Client(), ts.URL+strings.TrimSuffix(u.Path, "stream.m3u8")+"unknown.txt", "")
	if status != http.StatusNotFound {
		t.Errorf("unrecognized HLS artifact = %d, want 404", status)
	}
	status, _, _ = get(t, ts.Client(), ts.URL+strings.TrimSuffix(u.Path, "stream.m3u8")+"probe-temp.ts", "")
	if status != http.StatusNotFound {
		t.Errorf("unlisted MP4 artifact = %d, want 404", status)
	}
}

func TestRangeRequestLogsTransferDetailsWithoutTokenizedURL(t *testing.T) {
	s := testServer()
	u, err := s.Video(writeFile(t, "movie.mp4", "0123456789"))
	if err != nil {
		t.Fatal(err)
	}

	previous := slog.Default()
	var logs strings.Builder
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	ts := httptest.NewServer(s)
	defer ts.Close()
	status, _, body := get(t, ts.Client(), ts.URL+u.Path, "bytes=2-5")
	if status != http.StatusPartialContent || body != "2345" {
		t.Fatalf("range response = %d %q, want 206 %q", status, body, "2345")
	}

	output := logs.String()
	for _, field := range []string{
		"file=movie.mp4",
		"file_size=10",
		`range="bytes=2-5"`,
		`content_range="bytes 2-5/10"`,
		"status=206",
		"bytes=4",
	} {
		if !strings.Contains(output, field) {
			t.Errorf("log %q does not contain %q", output, field)
		}
	}
	if strings.Contains(output, u.Path) {
		t.Errorf("log contains the tokenized media URL path %q", u.Path)
	}
}

func TestPublishedFilesGetDistinctTokens(t *testing.T) {
	s := testServer()
	first, err := s.Video(writeFile(t, "a.mp4", "a"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Video(writeFile(t, "a.mp4", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Path == second.Path {
		t.Errorf("two publishes shared the path %q", first.Path)
	}
	if len(strings.TrimSuffix(strings.TrimPrefix(first.Path, "/v/"), ".mp4")) != 32 {
		t.Errorf("token in %q is not 16 random bytes", first.Path)
	}
}

func TestSubtitleIsPublishedAsSRT(t *testing.T) {
	s := testServer()
	path := writeFile(t, "movie.srt", "1\n00:00:00,000 --> 00:00:01,000\nHola\n")
	u, err := s.Subtitle(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.Path, "/s/") || !strings.HasSuffix(u.Path, ".srt") {
		t.Errorf("subtitle URL = %q, want /s/<token>.srt", u.Path)
	}

	ts := httptest.NewServer(s)
	defer ts.Close()
	status, headers, body := get(t, ts.Client(), ts.URL+u.Path, "")
	if status != http.StatusOK || !strings.Contains(body, "Hola") {
		t.Errorf("subtitle = %d %q", status, body)
	}
	if got := headers.Get("Content-Type"); got != SubtitleMIME {
		t.Errorf("Content-Type = %q, want %q", got, SubtitleMIME)
	}
}

func TestServerRefusesUnpublishedPathsAndMethods(t *testing.T) {
	s := testServer()
	if _, err := s.Video(writeFile(t, "movie.mp4", "x")); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()

	status, _, _ := get(t, ts.Client(), ts.URL+"/v/not-a-token.mp4", "")
	if status != http.StatusNotFound {
		t.Errorf("unknown path = %d, want 404", status)
	}

	resp, err := ts.Client().Post(ts.URL+"/v/anything", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", resp.StatusCode)
	}
}

func TestInterruptedDownloadDoesNotBlockShutdown(t *testing.T) {
	s, err := New("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Video(writeFile(t, "movie.mp4", strings.Repeat("x", 1<<20)))
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(io.LimitReader(resp.Body, 1)); err != nil {
		t.Fatal(err)
	}

	// The device is still holding the connection, as a paused Roku would.
	stopping, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := s.Close(stopping); err != nil {
		t.Logf("Close reported %v (an interrupted download is expected)", err)
	}
}

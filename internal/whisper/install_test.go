package whisper

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stupside/rokucastor/internal/config"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestValidModel(t *testing.T) {
	for _, name := range []string{ModelTiny, ModelTinyEN, ModelSmall, ModelBase} {
		if !ValidModel(name) {
			t.Errorf("ValidModel(%q) = false", name)
		}
	}
	for _, name := range []string{"", "medium", "large"} {
		if ValidModel(name) {
			t.Errorf("ValidModel(%q) = true, want only the pinned downloads", name)
		}
	}
}

func TestFetchDownloadsVerifiesAndRecordsTheChecksum(t *testing.T) {
	payload := []byte("pinned bytes")
	sum := sha256.Sum256(payload)
	art := artifact{url: "ignored", size: int64(len(payload)), sha256: hex.EncodeToString(sum[:]), detail: "tool.zip"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "12")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	art.url = server.URL

	dir := t.TempDir()
	dest := filepath.Join(dir, "tool.zip")
	var last Progress
	install := &installer{
		http:     server.Client(),
		progress: func(p Progress) { last = p },
	}
	if err := install.fetch(t.Context(), art, dest, StageTool); err != nil {
		t.Fatalf("fetch = %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("downloaded %q, %v; want the payload", got, err)
	}
	marker, err := os.ReadFile(dest + ".sha256")
	if err != nil || strings.TrimSpace(string(marker)) != art.sha256 {
		t.Fatalf("checksum marker = %q, %v; want %q", marker, err, art.sha256)
	}
	if last.Stage != StageTool || last.Percent != 100 {
		t.Errorf("last progress = %+v, want the tool at 100%%", last)
	}
}

func TestFetchRefusesBytesThatDoNotMatchThePinnedHash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "tool.zip")
	art := artifact{url: server.URL, size: 8, sha256: strings.Repeat("0", 64), detail: "tool.zip"}
	install := &installer{http: server.Client()}
	err := install.fetch(t.Context(), art, dest, StageTool)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("fetch = %v, want a checksum refusal", err)
	}
	if regular(dest) {
		t.Fatal("a refused download was kept")
	}
	if _, err := os.Stat(dest + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused download left its partial file")
	}
}

func TestFetchKeepsACompleteFileWithoutDownloadingAgain(t *testing.T) {
	payload := []byte("already here")
	sum := sha256.Sum256(payload)
	art := artifact{size: int64(len(payload)), sha256: hex.EncodeToString(sum[:]), detail: "ggml-small.bin"}

	dest := filepath.Join(t.TempDir(), "ggml-small.bin")
	writeFile(t, dest, string(payload))

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a verified file was downloaded again")
	}))
	defer server.Close()
	art.url = server.URL

	install := &installer{http: server.Client()}
	if err := install.fetch(t.Context(), art, dest, StageModel); err != nil {
		t.Fatalf("fetch = %v", err)
	}
	if marker, err := os.ReadFile(dest + ".sha256"); err != nil || strings.TrimSpace(string(marker)) != art.sha256 {
		t.Fatalf("checksum marker = %q, %v; want the file to be recorded", marker, err)
	}
}

func TestExtractToolUnpacksTheExecutablesAndDLLsOnly(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "tool.zip")
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"Release/whisper-cli.exe":    "exe",
		"Release/whisper-server.exe": "server",
		"Release/whisper.dll":        "dll",
		"Release/readme.txt":         "notes",
		"../../escape.dll":           "evil",
	} {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractTool(zipPath, bin); err != nil {
		t.Fatalf("extractTool = %v", err)
	}
	for _, name := range []string{"whisper-cli.exe", "whisper-server.exe", "whisper.dll", "escape.dll"} {
		if !regular(filepath.Join(bin, name)) {
			t.Errorf("%s was not extracted into the bin directory", name)
		}
	}
	if regular(filepath.Join(bin, "readme.txt")) {
		t.Error("extractTool unpacked a file that is not a tool or a DLL")
	}
	if regular(filepath.Join(filepath.Dir(dir), "escape.dll")) {
		t.Fatal("extractTool wrote outside the bin directory")
	}
}

func TestExtractToolRequiresBothExecutables(t *testing.T) {
	for _, missing := range []string{"whisper-cli.exe", "whisper-server.exe"} {
		t.Run("without "+missing, func(t *testing.T) {
			dir := t.TempDir()
			zipPath := filepath.Join(dir, "tool.zip")
			var buf bytes.Buffer
			archive := zip.NewWriter(&buf)
			for _, name := range []string{"Release/whisper-cli.exe", "Release/whisper-server.exe", "Release/whisper.dll"} {
				if strings.HasSuffix(name, missing) {
					continue
				}
				entry, err := archive.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write([]byte("x")); err != nil {
					t.Fatal(err)
				}
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(zipPath, buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := extractTool(zipPath, dir); err == nil {
				t.Fatalf("extractTool accepted an archive without %s", missing)
			}
		})
	}
}

func TestVideoUsesACachedTranscriptWithoutTouchingTheNetwork(t *testing.T) {
	layout := config.NewAt(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(layout.Root, "movie.mp4")
	writeFile(t, video, "not really a video")

	key, err := cacheKey(video, DefaultModel, "spa")
	if err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(layout.Subs, key+".srt")
	writeFile(t, cached, "1\n00:00:00,000 --> 00:00:01,000\nHola\n")

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("a cached transcript still hit the network")
		return nil, errors.New("unexpected request")
	})}
	got, err := Video(t.Context(), Options{
		Layout:   layout,
		Video:    video,
		Language: "es",
		HTTP:     client,
	})
	if err != nil || got != cached {
		t.Fatalf("Video = %q, %v; want the cached file %q", got, err, cached)
	}
}

func TestVideoRejectsBadOptionsBeforeDownloading(t *testing.T) {
	layout := config.NewAt(t.TempDir())
	video := filepath.Join(t.TempDir(), "movie.mp4")
	writeFile(t, video, "x")

	for _, opts := range []Options{
		{Layout: layout, Video: video, Model: "huge", Language: "es"},
		{Layout: layout, Video: video, Model: DefaultModel, Language: "xx"},
		{Layout: layout, Video: filepath.Join(t.TempDir(), "missing.mp4"), Language: "es"},
	} {
		if _, err := Video(t.Context(), opts); err == nil {
			t.Errorf("Video(%+v) accepted an invalid request", opts)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProgressReaderReportsEachNewPercentOnce(t *testing.T) {
	var got []int
	reader := &progressReader{total: 100, report: func(percent int) { got = append(got, percent) }}
	for range 100 {
		_, _ = reader.Write([]byte("x"))
	}
	if len(got) < 2 || got[0] != 1 || got[len(got)-1] != 99 {
		t.Fatalf("percentages = %v, want 1 through 99 without repeats", got)
	}
}

func TestContextCancellationStopsADownload(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	install := &installer{http: http.DefaultClient}
	art := artifact{url: "http://192.0.2.1/tool.zip", size: 1, sha256: strings.Repeat("0", 64), detail: "tool.zip"}
	if err := install.fetch(ctx, art, filepath.Join(t.TempDir(), "tool.zip"), StageTool); !errors.Is(err, context.Canceled) {
		t.Fatalf("fetch = %v, want the cancellation", err)
	}
}

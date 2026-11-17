package whisper

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stupside/rokucastor/internal/config"
)

func TestCacheKeySeparatesVideosModelsAndLanguages(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "one.mp4")
	two := filepath.Join(dir, "two.mp4")
	writeFile(t, one, "same video bytes")
	writeFile(t, two, "same video bytes")

	base, err := cacheKey(one, ModelSmall, "spa")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := cacheKey(one, ModelSmall, "spa"); again != base {
		t.Error("cacheKey is not stable for the same request")
	}
	if moved, _ := cacheKey(two, ModelSmall, "spa"); moved != base {
		t.Error("cacheKey depends on the file name instead of its content")
	}
	if other, _ := cacheKey(one, ModelBase, "spa"); other == base {
		t.Error("a different Whisper model shares a cached transcript")
	}
	if other, _ := cacheKey(one, ModelSmall, "eng"); other == base {
		t.Error("a different language shares a cached transcript")
	}
}

func TestCacheKeyFollowsContentChanges(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "movie.mp4")
	writeFile(t, video, "the first cut")
	first, err := cacheKey(video, ModelSmall, "spa")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, video, "the second cut")
	second, err := cacheKey(video, ModelSmall, "spa")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("a changed video kept the old transcript key")
	}
}

func TestCacheKeySamplesBothEndsOfALargeVideo(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "movie.mp4")
	body := make([]byte, 3*cacheSample)
	for i := range body {
		body[i] = byte(i)
	}
	if err := os.WriteFile(video, body, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := cacheKey(video, ModelSmall, "spa")
	if err != nil {
		t.Fatal(err)
	}
	// A change in the middle, which the sample does not read, is the known
	// trade-off; one at either end must be seen.
	body[0] ^= 0xff
	if err := os.WriteFile(video, body, 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := cacheKey(video, ModelSmall, "spa")
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("a change at the start of the file did not change the key")
	}

	body[len(body)-1] ^= 0xff
	if err := os.WriteFile(video, body, 0o600); err != nil {
		t.Fatal(err)
	}
	end, err := cacheKey(video, ModelSmall, "spa")
	if err != nil {
		t.Fatal(err)
	}
	if end == after {
		t.Fatal("a change at the end of the file did not change the key")
	}
}

func TestCachedFindsOnlyMatchingTranscripts(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "movie.mp4")
	writeFile(t, video, "the movie")
	layout := config.NewAt(filepath.Join(dir, "cache"))
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if _, ok := Cached(layout, video, ModelSmall, "es"); ok {
		t.Fatal("Cached found a transcript before one was written")
	}
	key, err := cacheKey(video, ModelSmall, "spa")
	if err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(layout.Subs, key+".srt")
	writeFile(t, cached, "1\n00:00:01,000 --> 00:00:02,000\nhello\n")
	if got, ok := Cached(layout, video, ModelSmall, "es"); !ok || got != cached {
		t.Fatalf("Cached() = %q, %v; want %q", got, ok, cached)
	}
	if _, ok := Cached(layout, video, ModelBase, "es"); ok {
		t.Fatal("Cached reused a transcript from another model")
	}
	if _, ok := Cached(layout, video, ModelSmall, "xx"); ok {
		t.Fatal("Cached accepted an unknown language")
	}
}

func TestCacheKeyRejectsAMissingFile(t *testing.T) {
	if _, err := cacheKey(filepath.Join(t.TempDir(), "missing.mp4"), ModelSmall, "spa"); err == nil {
		t.Fatal("cacheKey accepted a missing file")
	}
}

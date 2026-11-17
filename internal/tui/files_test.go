package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLocalVideosListsFoldersFirstAndOnlyPlayableFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "Series"), 0o700); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(dir, "b.MKV"))
	touch(t, filepath.Join(dir, "a.mp4"))
	touch(t, filepath.Join(dir, "notes.txt"))
	touch(t, filepath.Join(dir, "sub.srt"))

	items, err := localVideos(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, item := range items {
		names = append(names, item.(entry).name)
	}
	want := []string{"..", "Series", "a.mp4", "b.MKV"}
	if len(names) != len(want) {
		t.Fatalf("localVideos = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("localVideos = %v, want %v", names, want)
		}
	}
}

func TestLocalVideosHasNoParentEntryAtADriveRoot(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	if filepath.Dir(root) == root {
		items, err := localVideos(root)
		if err != nil {
			t.Skipf("drive root is not readable: %v", err)
		}
		if len(items) > 0 && items[0].(entry).name == ".." {
			t.Fatal("drive root listed a parent entry")
		}
	}
}

func TestSiblingSubtitleFindsTheSRTNextToTheVideo(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "movie.mp4")
	touch(t, video)
	srt := filepath.Join(dir, "movie.srt")
	touch(t, srt)

	if got := siblingSubtitle(video); got != srt {
		t.Fatalf("siblingSubtitle = %q, want %q", got, srt)
	}
}

func TestSiblingSubtitleIsEmptyWithoutOne(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "movie.mp4")
	touch(t, video)
	touch(t, filepath.Join(dir, "other.srt"))

	if got := siblingSubtitle(video); got != "" {
		t.Fatalf("siblingSubtitle = %q, want none", got)
	}
}

func TestResolveFolderRejectsFilesAndEmptyPaths(t *testing.T) {
	file := filepath.Join(t.TempDir(), "movie.mp4")
	touch(t, file)
	for _, path := range []string{"", "   ", file} {
		if _, err := resolveFolder(path); err == nil {
			t.Errorf("resolveFolder(%q) accepted a non-folder", path)
		}
	}
}

func TestClock(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                "0:00",
		65 * time.Second: "1:05",
		time.Hour + 2*time.Minute + 5*time.Second: "1:02:05",
	} {
		if got := clock(d); got != want {
			t.Errorf("clock(%s) = %q, want %q", d, got, want)
		}
	}
}

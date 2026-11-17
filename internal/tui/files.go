package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/stupside/rokucastor/internal/media"
)

// entry is one row of the local video browser.
type entry struct {
	name string
	path string
	dir  bool
	size int64
}

func (e entry) Title() string {
	if e.dir {
		return "[folder] " + e.name
	}
	return e.name
}

func (e entry) Description() string {
	if e.dir {
		return ""
	}
	return humanSize(e.size)
}

func (e entry) FilterValue() string { return e.name }

// localVideos lists a folder's subfolders and playable videos. The parent
// entry is first, unless the folder is a drive root.
func localVideos(path string) ([]list.Item, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("reading folder %q: %w", path, err)
	}

	items := make([]entry, 0, len(entries)+1)
	if parent := filepath.Dir(path); parent != path {
		items = append(items, entry{name: "..", path: parent, dir: true})
	}
	for _, item := range entries {
		full := filepath.Join(path, item.Name())
		if item.IsDir() {
			items = append(items, entry{name: item.Name(), path: full, dir: true})
			continue
		}
		if item.Type()&os.ModeSymlink != 0 {
			continue
		}
		if _, err := media.FormatFor(item.Name()); err != nil {
			continue
		}
		info, err := item.Info()
		if err != nil {
			return nil, fmt.Errorf("reading file info for %q: %w", full, err)
		}
		if info.Mode().IsRegular() {
			items = append(items, entry{name: item.Name(), path: full, size: info.Size()})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.name == ".." {
			return true
		}
		if b.name == ".." {
			return false
		}
		if a.dir != b.dir {
			return a.dir
		}
		return strings.ToLower(a.name) < strings.ToLower(b.name)
	})

	rows := make([]list.Item, len(items))
	for i, item := range items {
		rows[i] = item
	}
	return rows, nil
}

// siblingSubtitle finds an SRT next to the video with the same base name.
func siblingSubtitle(video string) string {
	base := strings.TrimSuffix(video, filepath.Ext(video)) + ".srt"
	if regular(base) {
		return base
	}
	// A case-sensitive filesystem may hold Movie.SRT where the video is
	// Movie.mp4; Windows matches it already, but the scan costs one read.
	dir, want := filepath.Dir(video), strings.ToLower(filepath.Base(base))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, item := range entries {
		if !item.IsDir() && strings.ToLower(item.Name()) == want {
			return filepath.Join(dir, item.Name())
		}
	}
	return ""
}

func regular(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// resolveFolder cleans a typed path and checks it is a directory before the
// list screen tries to read it.
func resolveFolder(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("enter a folder path")
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolving folder: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("reading folder %q: %w", absolute, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a folder", absolute)
	}
	return absolute, nil
}

func humanSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// clock renders a duration as m:ss, or h:mm:ss once it passes an hour.
func clock(d time.Duration) string {
	total := int64(d / time.Second)
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

// folderLoadedMsg is the result of reading a folder off the UI goroutine.
type folderLoadedMsg struct {
	tok   int
	path  string
	items []list.Item
	err   error
}

func loadFolderCmd(ctx context.Context, path string, tok int) tea.Cmd {
	return func() tea.Msg {
		absolute, err := resolveFolder(path)
		if err != nil {
			return folderLoadedMsg{tok: tok, err: err}
		}
		items, err := localVideos(absolute)
		if err != nil {
			return folderLoadedMsg{tok: tok, err: err}
		}
		if err := ctx.Err(); err != nil {
			return folderLoadedMsg{tok: tok, err: err}
		}
		return folderLoadedMsg{tok: tok, path: absolute, items: items}
	}
}

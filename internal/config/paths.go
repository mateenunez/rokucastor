// Package config locates rokucastor's per-user files and settings.
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Layout are the per-user directories rokucastor reads and writes.
type Layout struct {
	Root   string
	Config string
	Bin    string
	Models string
	Subs   string
	Logs   string
}

// Paths returns the directories under the user's local application data.
// On Windows that is %LOCALAPPDATA%\rokucastor.
func Paths() (Layout, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return Layout{}, fmt.Errorf("locating the user cache directory: %w", err)
	}
	return NewAt(filepath.Join(base, "rokucastor")), nil
}

// NewAt builds the layout under root without touching the filesystem.
func NewAt(root string) Layout {
	return Layout{
		Root:   root,
		Config: filepath.Join(root, "config.json"),
		Bin:    filepath.Join(root, "bin"),
		Models: filepath.Join(root, "models"),
		Subs:   filepath.Join(root, "subs"),
		Logs:   filepath.Join(root, "logs"),
	}
}

// Ensure creates the writable directories, leaving existing ones untouched.
func (p Layout) Ensure() error {
	for _, dir := range []string{p.Root, p.Bin, p.Models, p.Subs, p.Logs} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating %q: %w", dir, err)
		}
	}
	return nil
}

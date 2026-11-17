package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewAtBuildsTheLayoutUnderRoot(t *testing.T) {
	root := filepath.Join("C:", "Users", "someone", "AppData", "Local", "rokucastor")
	paths := NewAt(root)

	cases := map[string]string{
		"root":   root,
		"config": filepath.Join(root, "config.json"),
		"bin":    filepath.Join(root, "bin"),
		"models": filepath.Join(root, "models"),
		"subs":   filepath.Join(root, "subs"),
		"logs":   filepath.Join(root, "logs"),
	}
	got := map[string]string{
		"root":   paths.Root,
		"config": paths.Config,
		"bin":    paths.Bin,
		"models": paths.Models,
		"subs":   paths.Subs,
		"logs":   paths.Logs,
	}
	for name, want := range cases {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
}

func TestEnsureCreatesEveryDirectory(t *testing.T) {
	paths := NewAt(t.TempDir())
	if err := paths.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, dir := range []string{paths.Root, paths.Bin, paths.Models, paths.Subs, paths.Logs} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %q: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%q is not a directory", dir)
		}
	}
	if _, err := os.Stat(paths.Config); !os.IsNotExist(err) {
		t.Fatalf("config file should not be created by Ensure; stat error = %v", err)
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	paths := NewAt(t.TempDir())
	if err := paths.Ensure(); err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	if err := paths.Ensure(); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
}

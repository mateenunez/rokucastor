package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadSettingsWithoutAFileIsEmpty(t *testing.T) {
	settings, err := LoadSettings(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings != (Settings{}) {
		t.Fatalf("settings = %+v, want zero settings", settings)
	}
	if got := settings.StartupTimeout(); got != DefaultStartupTimeout {
		t.Fatalf("StartupTimeout = %s, want the default %s", got, DefaultStartupTimeout)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	want := Settings{Password: "mi-clave", StartupTimeoutSeconds: 120}
	if err := SaveSettings(path, want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got != want {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
	if got := got.StartupTimeout(); got != 2*time.Minute {
		t.Fatalf("StartupTimeout = %s, want 2m", got)
	}

	// The file is replaced in one step, so no temporary file is left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("directory holds %v, want only config.json", entries)
	}
}

func TestSettingsStartupTimeoutFallsBackToTheDefault(t *testing.T) {
	for _, seconds := range []int{0, -5} {
		settings := Settings{StartupTimeoutSeconds: seconds}
		if got := settings.StartupTimeout(); got != DefaultStartupTimeout {
			t.Errorf("StartupTimeout(%d) = %s, want %s", seconds, got, DefaultStartupTimeout)
		}
	}
}

func TestLoadSettingsRejectsBrokenJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSettings(path)
	if err == nil || !strings.Contains(err.Error(), "parsing settings") {
		t.Fatalf("LoadSettings = %v, want a parse error naming %q", err, path)
	}
}

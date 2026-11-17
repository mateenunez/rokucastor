package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DefaultStartupTimeout is how long a Roku may stay in startup or buffering
// before rokucastor gives up, when the settings do not say otherwise.
const DefaultStartupTimeout = 30 * time.Second

// Settings are the options rokucastor remembers between runs. The password is
// stored in plain text, so the file is only readable by the current user.
type Settings struct {
	Password              string `json:"password,omitempty"`
	StartupTimeoutSeconds int    `json:"startup_timeout_seconds,omitempty"`
}

// StartupTimeout is how long to wait for the Roku to start playing. Zero or a
// negative value means the default.
func (s Settings) StartupTimeout() time.Duration {
	if s.StartupTimeoutSeconds <= 0 {
		return DefaultStartupTimeout
	}
	return time.Duration(s.StartupTimeoutSeconds) * time.Second
}

// LoadSettings reads the settings at path. A missing file is not an error: it
// means nothing has been remembered yet.
func LoadSettings(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("reading settings %q: %w", path, err)
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, fmt.Errorf("parsing settings %q: %w", path, err)
	}
	return settings, nil
}

// SaveSettings writes the settings at path, replacing any previous file in one
// step so a crash cannot leave half a file behind.
func SaveSettings(path string, settings Settings) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %q: %w", dir, err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding settings: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return fmt.Errorf("writing settings: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setting permissions on %q: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing settings: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replacing settings %q: %w", path, err)
	}
	return nil
}

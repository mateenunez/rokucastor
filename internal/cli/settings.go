package cli

import (
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/stupside/rokucastor/internal/config"
)

// rememberSettings loads the settings saved by earlier runs and applies any
// flag given now, so the next run starts with the same password and timeout.
// A settings file that cannot be read or written is reported but does not stop
// a cast.
func rememberSettings(cmd *cli.Command) (config.Settings, error) {
	var settings config.Settings
	paths, err := config.Paths()
	if err != nil {
		warnSettings(cmd, err)
	} else if loaded, err := config.LoadSettings(paths.Config); err != nil {
		warnSettings(cmd, err)
	} else {
		settings = loaded
	}

	changed := false
	if cmd.IsSet("password") {
		settings.Password = cmd.String("password")
		changed = true
	}
	if cmd.IsSet("startup-timeout") {
		seconds := cmd.Int("startup-timeout")
		if seconds < 1 {
			return config.Settings{}, fmt.Errorf("--startup-timeout must be at least 1 second, got %d", seconds)
		}
		settings.StartupTimeoutSeconds = seconds
		changed = true
	}
	if changed && paths.Config != "" {
		if err := config.SaveSettings(paths.Config, settings); err != nil {
			warnSettings(cmd, err)
		}
	}
	return settings, nil
}

func warnSettings(cmd *cli.Command, err error) {
	fmt.Fprintf(cmd.Root().ErrWriter, "rokucastor: settings: %v\n", err)
}

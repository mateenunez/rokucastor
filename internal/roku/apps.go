package roku

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
)

// ensureChannel guarantees our dev channel occupies the sideload slot, and
// only installs it when it is missing or another app occupies that slot.
func (s *Session) ensureChannel(ctx context.Context, cfg Config) error {
	apps, err := s.queryApps(ctx)
	if err != nil {
		return fmt.Errorf("querying roku apps: %w", err)
	}
	return ensureChannelFromApps(apps, cfg.Password, func() error {
		slog.InfoContext(ctx, "sideloading roku channel", "host", s.ecp.Hostname(), "reason", "missing or different dev app")
		return s.sideloadChannel(ctx, cfg.Password)
	})
}

func ensureChannelFromApps(apps []app, password string, install func() error) error {
	slot := slices.IndexFunc(apps, func(a app) bool { return a.ID == devAppID })
	if slot >= 0 {
		title := strings.TrimSpace(apps[slot].Title)
		switch title {
		case channelTitle:
			return nil
		case legacyChannelTitle:
			// The old channel still plays our media. Replacing it needs the
			// developer password, so without one it is reused as is.
			if password == "" {
				return nil
			}
			return install()
		}
	}
	if password != "" {
		return install()
	}
	if slot >= 0 {
		return errors.New("a different sideloaded channel occupies the Roku dev slot; set a developer password so rokucastor can replace it")
	}
	return errors.New("roku channel not installed and no developer password set: enable Developer Mode on the Roku, set a web-server password, and pass --password")
}

type app struct {
	ID    string `xml:"id,attr"`
	Title string `xml:",chardata"`
}

func (s *Session) queryApps(ctx context.Context) ([]app, error) {
	body, err := get(ctx, s.hc, s.ecp.JoinPath("query", "apps"), 1<<20)
	if err != nil {
		return nil, err
	}
	var list struct {
		XMLName xml.Name `xml:"apps"`
		Apps    []app    `xml:"app"`
	}
	if err := xml.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decoding roku apps: %w", err)
	}
	return list.Apps, nil
}

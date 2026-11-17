// Package roku reaches Rokus over ECP and plays through a channel rokucastor
// sideloads.
package roku

import (
	"cmp"
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/huin/goupnp/httpu"
	"github.com/huin/goupnp/ssdp"

	"github.com/stupside/rokucastor/internal/device"
)

const (
	searchTarget   = "roku:ecp"
	discoverySends = 3
	devAppID       = "dev"
	devUser        = "rokudev"
	httpTimeout    = 10 * time.Second
	// Leave time in the discovery window to fetch owner-set names.
	searchTimeout = 3 * time.Second
	ecpPort       = "8060"
)

// Config selects a published channel to launch, or the developer password to
// sideload rokucastor's own.
type Config struct {
	AppID    string
	Password string
	// StartupTimeout is how long the Roku may stay in startup or buffering
	// before playback counts as failed; zero uses DefaultStartupTimeout.
	StartupTimeout time.Duration
}

// Family discovers and connects to Rokus.
type Family struct {
	Config Config
}

// Discover finds Rokus over SSDP, naming each once however often it answered.
func (Family) Discover(ctx context.Context) []device.Info {
	hc, err := httpu.NewHTTPUClient()
	if err != nil {
		slog.WarnContext(ctx, "roku discovery", "error", err)
		return nil
	}
	defer hc.Close()
	return discover(ctx, func(searchCtx context.Context) ([]*http.Response, error) {
		return ssdp.RawSearch(searchCtx, hc, searchTarget, discoverySends)
	}, &http.Client{Timeout: httpTimeout})
}

// discover keeps SSDP's wait separate from the name lookups.
func discover(ctx context.Context, search func(context.Context) ([]*http.Response, error), client *http.Client) []device.Info {
	searchWindow := searchTimeout
	if deadline, ok := ctx.Deadline(); ok {
		// Reserve the last third of a shorter caller window for device-info.
		searchWindow = min(searchWindow, time.Until(deadline)*2/3)
	}
	searchCtx, cancel := context.WithTimeout(ctx, searchWindow)
	responses, err := search(searchCtx)
	cancel()
	if err != nil {
		slog.WarnContext(ctx, "roku discovery", "error", err)
		return nil
	}

	found := map[string]*url.URL{}
	for _, resp := range responses {
		if loc, err := resp.Location(); err == nil && loc.Host != "" {
			found[cmp.Or(resp.Header.Get("USN"), loc.String())] = loc
		}
	}
	ids := slices.Sorted(maps.Keys(found))

	// One window for every name, and the lookups run over it together.
	nctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	devices := make([]device.Info, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() {
			loc := found[id]
			devices[i] = device.Info{ID: id, Name: deviceName(nctx, client, loc), Address: loc.String()}
		})
	}
	wg.Wait()
	return devices
}

// Locate reduces a pinned address to its ECP root, bypassing SSDP discovery.
func (Family) Locate(_ context.Context, address string) (string, error) {
	host := strings.TrimSpace(address)
	if host == "" {
		return "", fmt.Errorf("no Roku address given")
	}
	if u, err := url.Parse(address); err == nil && u.Host != "" {
		host = u.Host
	}
	return (&url.URL{Scheme: "http", Host: withPort(host, ecpPort)}).String(), nil
}

// withPort completes a bare host, bracketed or not, with a default port.
func withPort(address, port string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(strings.Trim(address, "[]"), port)
}

// ConnectAddress connects to a pinned address without SSDP discovery, reading
// its owner-set name from ECP.
func (f Family) ConnectAddress(ctx context.Context, address string) (device.Device, error) {
	root, err := f.Locate(ctx, address)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(root)
	if err != nil {
		return nil, fmt.Errorf("parsing roku address %q: %w", root, err)
	}
	info := device.Info{Address: root, Name: deviceName(ctx, &http.Client{Timeout: httpTimeout}, u)}
	return f.Connect(ctx, info)
}

// deviceName reads the owner-set name from /query/device-info, falling back to
// the host on any failure.
func deviceName(ctx context.Context, hc *http.Client, ecpRoot *url.URL) string {
	body, err := get(ctx, hc, ecpRoot.JoinPath("query", "device-info"), 1<<16)
	if err != nil {
		return ecpRoot.Hostname()
	}
	return cmp.Or(parseDeviceInfoName(body), ecpRoot.Hostname())
}

func parseDeviceInfoName(body []byte) string {
	var info struct {
		UserDeviceName string `xml:"user-device-name"`
		ModelName      string `xml:"model-name"`
	}
	if err := xml.Unmarshal(body, &info); err != nil {
		return ""
	}
	return cmp.Or(strings.TrimSpace(info.UserDeviceName), strings.TrimSpace(info.ModelName))
}

// Connect opens a session to the Roku at info, making sure its channel exists.
func (f Family) Connect(ctx context.Context, info device.Info) (device.Device, error) {
	root, err := url.Parse(info.Address)
	if err != nil {
		return nil, fmt.Errorf("parsing roku address %q: %w", info.Address, err)
	}
	if root.Host == "" {
		return nil, fmt.Errorf("roku address %q has no host", info.Address)
	}

	s := &Session{
		ecp:     &url.URL{Scheme: "http", Host: root.Host},
		appID:   cmp.Or(f.Config.AppID, devAppID),
		name:    cmp.Or(info.Name, root.Hostname()),
		hc:      &http.Client{Timeout: httpTimeout},
		startup: f.Config.StartupTimeout,
	}
	if s.appID == devAppID {
		if err := s.ensureChannel(ctx, f.Config); err != nil {
			return nil, err
		}
		return s, nil
	}

	apps, err := s.queryApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying roku apps: %w", err)
	}
	if !slices.ContainsFunc(apps, func(a app) bool { return a.ID == s.appID }) {
		return nil, fmt.Errorf("roku channel %q is not installed on %q", s.appID, s.name)
	}
	return s, nil
}

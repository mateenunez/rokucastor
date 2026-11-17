package roku

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/rokucastor/internal/device"
)

type deviceInfoRoundTripper func(*http.Request) (*http.Response, error)

func (f deviceInfoRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDiscoveryHasTimeToReadRokuNamesAfterSSDPSearch(t *testing.T) {
	for _, timeout := range []time.Duration{3 * time.Second, 5 * time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), timeout)
				defer cancel()
				client := &http.Client{Transport: deviceInfoRoundTripper(func(req *http.Request) (*http.Response, error) {
					if req.Context().Err() != nil || req.URL.Path != "/query/device-info" {
						t.Errorf("device-info request ran after discovery expired: %v", req.Context().Err())
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`<device-info><user-device-name>Living Room</user-device-name></device-info>`))}, nil
				})}
				found := discover(ctx, func(searchCtx context.Context) ([]*http.Response, error) {
					<-searchCtx.Done() // SSDP consumes its full search window before returning replies.
					return []*http.Response{{Header: http.Header{"Location": {"http://roku.test:8060/"}, "Usn": {"uuid:roku-1"}}}}, nil
				}, client)
				if len(found) != 1 || found[0].Name != "Living Room" {
					t.Fatalf("discovery returned %+v, want the owner-set Roku name", found)
				}
				if ctx.Err() != nil {
					t.Fatal("name lookup consumed the caller's whole discovery window")
				}
			})
		})
	}
}

func TestDiscoveryNamesAnUnnamedRokuByItsHost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		client := &http.Client{Transport: deviceInfoRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
		})}
		found := discover(ctx, func(context.Context) ([]*http.Response, error) {
			return []*http.Response{{Header: http.Header{"Location": {"http://192.0.2.10:8060/"}}}}, nil
		}, client)
		if len(found) != 1 || found[0].Name != "192.0.2.10" {
			t.Fatalf("discovery returned %+v, want the host as the name", found)
		}
	})
}

func TestConnectVerifiesTheChannelItWillLaunch(t *testing.T) {
	for _, tt := range []struct {
		name, apps, appID string
		wantErr           bool
	}{
		{name: "rokucastor's dev channel is installed", apps: `<apps><app id="dev">Rokucastor</app></apps>`},
		{name: "the legacy dev channel is reused", apps: `<apps><app id="dev">Rokucast</app></apps>`},
		{name: "a foreign dev channel and no password", apps: `<apps><app id="dev">SomeoneElse</app></apps>`, wantErr: true},
		{name: "a published app that is not installed", apps: `<apps><app id="12345">MyChannel</app></apps>`, appID: "99999", wantErr: true},
		{name: "a published app is installed", apps: `<apps><app id="12345">MyChannel</app></apps>`, appID: "12345"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/query/apps" {
					http.NotFound(w, r)
					return
				}
				_, _ = io.WriteString(w, tt.apps)
			}))
			defer ts.Close()
			_, err := Family{Config: Config{AppID: tt.appID}}.Connect(t.Context(), device.Info{Name: "Bedroom Roku", Address: ts.URL})
			if (err != nil) != tt.wantErr {
				t.Errorf("Connect() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEnsureChannelInstallsOnlyWhenNeeded(t *testing.T) {
	for _, tt := range []struct {
		name        string
		apps        []app
		password    string
		wantInstall bool
		wantErr     string
	}{
		{name: "reuse installed Rokucastor even with password", apps: []app{{ID: "dev", Title: " Rokucastor "}}, password: "pw"},
		{name: "reuse the legacy channel without a password", apps: []app{{ID: "dev", Title: "Rokucast"}}},
		{name: "replace the legacy channel when a password allows it", apps: []app{{ID: "dev", Title: "Rokucast"}}, password: "pw", wantInstall: true},
		{name: "install into empty dev slot", password: "pw", wantInstall: true},
		{name: "replace another dev app", apps: []app{{ID: "dev", Title: "Other"}}, password: "pw", wantInstall: true},
		{name: "refuse empty slot without password", wantErr: "no developer password"},
		{name: "refuse foreign dev app without password", apps: []app{{ID: "dev", Title: "Other"}}, wantErr: "different sideloaded channel"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			installCalls := 0
			err := ensureChannelFromApps(tt.apps, tt.password, func() error {
				installCalls++
				return nil
			})
			if (err != nil) != (tt.wantErr != "") || tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ensureChannelFromApps() = %v, want error containing %q", err, tt.wantErr)
			}
			wantCalls := 0
			if tt.wantInstall {
				wantCalls = 1
			}
			if installCalls != wantCalls {
				t.Errorf("sideload called %d times, want %d", installCalls, wantCalls)
			}
		})
	}
}

func TestLocate(t *testing.T) {
	for address, want := range map[string]string{
		"192.168.0.3":              "http://192.168.0.3:8060",
		"192.168.0.3:8888":         "http://192.168.0.3:8888",
		"http://192.168.0.3:8888/": "http://192.168.0.3:8888",
		"[2001:db8::3]":            "http://[2001:db8::3]:8060",
	} {
		if got, err := (Family{}).Locate(t.Context(), address); err != nil || got != want {
			t.Errorf("Locate(%q) = %q, %v, want %q", address, got, err, want)
		}
	}
}

func TestLocateRejectsAnEmptyAddress(t *testing.T) {
	if _, err := (Family{}).Locate(t.Context(), "  "); err == nil {
		t.Fatal("Locate accepted an empty address")
	}
}

func TestMalformedAppListDoesNotTriggerSideloading(t *testing.T) {
	for _, body := range []string{`<apps><app id="dev">Rokucastor`, `<html><body>Device login required</body></html>`} {
		t.Run(body, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/query/apps" {
					t.Errorf("malformed app list triggered %s %s", r.Method, r.URL.Path)
				}
				_, _ = io.WriteString(w, body)
			}))
			defer ts.Close()
			_, err := Family{Config: Config{Password: "developer-password"}}.Connect(t.Context(), device.Info{Address: ts.URL})
			if err == nil || !strings.Contains(err.Error(), "decoding roku apps") {
				t.Fatalf("Connect = %v, want the app-list decoding failure before sideloading", err)
			}
		})
	}
}

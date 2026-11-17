package cast

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stupside/rokucastor/internal/device"
	"github.com/stupside/rokucastor/internal/media"
)

func TestStartRejectsAnUnsupportedVideoBeforeReachingTheNetwork(t *testing.T) {
	_, err := Start(t.Context(), Options{Address: "192.0.2.1", Video: "movie.avi"})
	if err == nil || !strings.Contains(err.Error(), "unsupported video format") {
		t.Fatalf("Start = %v, want the format refusal before any connection", err)
	}
}

func TestStartRejectsAnUnknownSubtitleLanguageBeforeReachingTheNetwork(t *testing.T) {
	_, err := Start(t.Context(), Options{
		Address:  "192.0.2.1",
		Video:    "movie.mp4",
		Subtitle: "movie.srt",
		Language: "xx",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown subtitle language") {
		t.Fatalf("Start = %v, want the language refusal before any connection", err)
	}
}

type waitingDevice struct{}

func (waitingDevice) Name() string                                { return "test Roku" }
func (waitingDevice) Play(context.Context, device.Playback) error { return nil }
func (waitingDevice) AwaitEnd(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
func (waitingDevice) Pause(context.Context) error  { return nil }
func (waitingDevice) Resume(context.Context) error { return nil }
func (waitingDevice) Stop(context.Context) error   { return nil }
func (waitingDevice) Position(context.Context) (device.Playhead, error) {
	return device.Playhead{}, device.ErrNoPosition
}
func (waitingDevice) Close() error { return nil }

func TestAwaitEndSurfacesTranscoderFailure(t *testing.T) {
	server, err := media.New("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	session := &Session{Player: waitingDevice{}, Server: server}
	server.ReportError(errors.New("encoder failed"))
	if err := session.AwaitEnd(t.Context()); err == nil || !strings.Contains(err.Error(), "encoder failed") {
		t.Fatalf("AwaitEnd = %v, want the transcoder failure", err)
	}
}

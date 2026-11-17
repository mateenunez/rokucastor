package media

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestProgressivePublishesBytesBeforeProductionEnds(t *testing.T) {
	stream, err := NewProgressive(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	server := testServer()
	u, err := server.PublishProgressive(stream)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server)
	defer ts.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+u.Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=2-")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("X-Stream-Restarted") != "true" {
		t.Fatalf("active range was not restarted: %v", resp.Header)
	}

	if _, err := stream.Write([]byte("init")); err != nil {
		t.Fatal(err)
	}
	first := make(chan string, 1)
	go func() {
		buf := make([]byte, 4)
		_, err := io.ReadFull(resp.Body, buf)
		if err != nil {
			first <- err.Error()
			return
		}
		first <- string(buf)
	}()
	select {
	case got := <-first:
		if got != "init" {
			t.Fatalf("first progressive bytes = %q, want init", got)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP did not deliver the first bytes while the producer was still open")
	}
	if _, err := stream.Write([]byte("tail")); err != nil {
		t.Fatal(err)
	}
	stream.Finish(nil)
	rest, err := io.ReadAll(resp.Body)
	if err != nil || string(rest) != "tail" {
		t.Fatalf("remaining stream = %q, err %v; want tail", rest, err)
	}

	status, _, body := get(t, ts.Client(), ts.URL+u.Path, "bytes=1-3")
	if status != http.StatusPartialContent || body != "nit" {
		t.Fatalf("completed range = %d %q, want 206 nit", status, body)
	}
}

func TestProgressiveReportsProducerFailureAfterServingMadeBytes(t *testing.T) {
	stream, err := NewProgressive(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	producerErr := errors.New("encoder failed")
	if _, err := stream.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	stream.Finish(producerErr)
	if _, finished, gotErr := stream.Snapshot(); !finished || !errors.Is(gotErr, producerErr) {
		t.Fatalf("snapshot = finished %v, error %v", finished, gotErr)
	}
	recorder := httptest.NewRecorder()
	stream.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("failed progressive output status = %d, want 502", recorder.Code)
	}
}

func TestProgressiveCloseRemovesItsSpool(t *testing.T) {
	stream, err := NewProgressive(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := stream.Path()
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spool stat error = %v, want not-exist", err)
	}
}

func TestProgressiveRequestStopsOnContextCancellation(t *testing.T) {
	stream, err := NewProgressive(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	ctx, cancel := context.WithCancel(t.Context())
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		stream.ServeHTTP(recorder, req)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("progressive request did not stop after cancellation")
	}
}

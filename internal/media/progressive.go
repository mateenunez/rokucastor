package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const progressiveLimit = 8 << 30

type Progressive struct {
	file *os.File
	path string

	mu       sync.Mutex
	cond     *sync.Cond
	size     int64
	finished bool
	err      error
}

func NewProgressive(dir string) (*Progressive, error) {
	file, err := os.CreateTemp(dir, "stream-*.mp4")
	if err != nil {
		return nil, fmt.Errorf("creating a progressive media spool: %w", err)
	}
	p := &Progressive{file: file, path: file.Name()}
	p.cond = sync.NewCond(&p.mu)
	return p, nil
}

func (p *Progressive) Path() string { return p.path }

func (p *Progressive) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return 0, os.ErrClosed
	}
	if int64(len(data)) > progressiveLimit-p.size {
		return 0, fmt.Errorf("progressive media exceeds %d bytes", progressiveLimit)
	}
	n, err := p.file.Write(data)
	p.size += int64(n)
	p.cond.Broadcast()
	return n, err
}

func (p *Progressive) Finish(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	p.finished = true
	p.err = err
	p.cond.Broadcast()
}

func (p *Progressive) Snapshot() (int64, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.size, p.finished, p.err
}

func (p *Progressive) Close() error {
	p.Finish(nil)
	if err := p.file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("closing progressive media: %w", err)
	}
	if err := os.Remove(p.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing progressive media: %w", err)
	}
	return nil
}

func (p *Progressive) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")

	_, finished, err := p.Snapshot()
	if finished {
		if err != nil {
			http.Error(w, "transcoding failed", http.StatusBadGateway)
			return
		}
		file, openErr := os.Open(p.path)
		if openErr != nil {
			http.Error(w, "media is no longer available", http.StatusNotFound)
			return
		}
		defer file.Close()
		info, statErr := file.Stat()
		if statErr != nil {
			http.Error(w, "media is no longer available", http.StatusNotFound)
			return
		}
		http.ServeContent(w, r, filepath.Base(p.path), info.ModTime(), file)
		return
	}

	w.Header().Set("Accept-Ranges", "none")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	if start, _, ok := byteRange(r.Header.Get("Range")); ok && start > 0 {
		w.Header().Set("X-Stream-Restarted", "true")
	}
	w.WriteHeader(http.StatusOK)
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}

	ctx := r.Context()
	stop := context.AfterFunc(ctx, func() {
		p.mu.Lock()
		p.cond.Broadcast()
		p.mu.Unlock()
	})
	defer stop()

	var offset int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := p.readAt(ctx, offset, buf)
		if n > 0 {
			written, writeErr := w.Write(buf[:n])
			offset += int64(written)
			if writeErr != nil || written != n {
				return
			}
			if flushErr := controller.Flush(); flushErr != nil {
				return
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && !errors.Is(readErr, context.Canceled) {
				panic(http.ErrAbortHandler)
			}
			return
		}
	}
}

func (p *Progressive) readAt(ctx context.Context, offset int64, dst []byte) (int, error) {
	for {
		p.mu.Lock()
		for offset >= p.size && !p.finished && ctx.Err() == nil {
			p.cond.Wait()
		}
		if err := ctx.Err(); err != nil {
			p.mu.Unlock()
			return 0, err
		}
		size, finished, finishErr := p.size, p.finished, p.err
		if offset >= size {
			p.mu.Unlock()
			if finished {
				if finishErr != nil {
					return 0, finishErr
				}
				return 0, io.EOF
			}
			continue
		}
		n := min(int64(len(dst)), size-offset)
		p.mu.Unlock()

		read, err := p.file.ReadAt(dst[:n], offset)
		if errors.Is(err, io.EOF) && int64(read) == n {
			err = nil
		}
		return read, err
	}
}

func byteRange(header string) (int64, int64, bool) {
	if header == "" {
		return 0, 0, false
	}
	spec, ok := strings.CutPrefix(strings.TrimSpace(header), "bytes=")
	if !ok {
		return 0, 0, false
	}
	first, _, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, false
	}
	if first == "" {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(first), 10, 64)
	return start, 0, err == nil && start >= 0
}

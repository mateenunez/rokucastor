package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// InitLogging configures slog to append structured text logs to rokucastor.log
// inside logsDir. It returns a cleanup function to close the log file.
func InitLogging(logsDir string) (func(), error) {
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		return func() {}, fmt.Errorf("creating logs directory %q: %w", logsDir, err)
	}
	path := filepath.Join(logsDir, "rokucastor.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return func() {}, fmt.Errorf("opening log file %q: %w", path, err)
	}

	previous := slog.Default()
	logger := slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	slog.Info("rokucastor session started", "time", time.Now().Format(time.RFC3339))
	return func() {
		slog.Info("rokucastor session ended", "time", time.Now().Format(time.RFC3339))
		_ = file.Close()
		slog.SetDefault(previous)
	}, nil
}

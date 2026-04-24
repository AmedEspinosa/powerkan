package logging

import (
	"io"
	"log/slog"
	"os"
)

// Result holds the constructed logger and the underlying file handle.
type Result struct {
	Logger *slog.Logger
	File   *os.File
}

// New creates a structured logger that writes to both stderr and the app log.
func New(logPath string, verbose bool) (Result, error) {
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Result{}, err
	}

	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

	handler := slog.NewTextHandler(io.MultiWriter(os.Stderr, file), &slog.HandlerOptions{
		Level: level,
	})

	return Result{
		Logger: slog.New(handler),
		File:   file,
	}, nil
}

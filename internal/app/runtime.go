package app

import (
	"database/sql"
	"log/slog"

	"github.com/amedespinosa/powerkan/internal/config"
	"github.com/amedespinosa/powerkan/internal/platform"
)

// Runtime holds initialized application dependencies for a single command run.
type Runtime struct {
	Paths   platform.Paths
	Config  config.Config
	Logger  *slog.Logger
	DB      *sql.DB
	logFile closer
}

type closer interface {
	Close() error
}

// NewRuntime constructs a runtime with managed resources.
func NewRuntime(paths platform.Paths, cfg config.Config, logger *slog.Logger, db *sql.DB, logFile closer) *Runtime {
	return &Runtime{
		Paths:   paths,
		Config:  cfg,
		Logger:  logger,
		DB:      db,
		logFile: logFile,
	}
}

// Close releases runtime resources.
func (r *Runtime) Close() error {
	var firstErr error

	if r.DB != nil {
		if err := r.DB.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if r.logFile != nil {
		if err := r.logFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

package bootstrap

import (
	"context"

	"github.com/amedespinosa/powerkan/internal/app"
	"github.com/amedespinosa/powerkan/internal/config"
	"github.com/amedespinosa/powerkan/internal/logging"
	"github.com/amedespinosa/powerkan/internal/platform"
	"github.com/amedespinosa/powerkan/internal/storage"
)

// Options controls runtime bootstrap behavior.
type Options struct {
	ConfigPath string
	Verbose    bool
}

// Init prepares directories, logging, config, database access, and migrations.
func Init(ctx context.Context, opts Options) (*app.Runtime, error) {
	paths, err := platform.ResolvePaths(opts.ConfigPath)
	if err != nil {
		return nil, err
	}

	if err := platform.EnsureDirectories(paths); err != nil {
		return nil, err
	}

	cfg, err := config.Load(paths.ConfigFile, paths.ExportsDir)
	if err != nil {
		return nil, err
	}

	logResult, err := logging.New(paths.LogFile, opts.Verbose)
	if err != nil {
		return nil, err
	}

	db, err := storage.Open(ctx, paths.DatabaseFile)
	if err != nil {
		_ = logResult.File.Close()
		return nil, err
	}

	if err := storage.ApplyMigrations(ctx, db); err != nil {
		_ = db.Close()
		_ = logResult.File.Close()
		return nil, err
	}

	return app.NewRuntime(paths, cfg, logResult.Logger, db, logResult.File), nil
}

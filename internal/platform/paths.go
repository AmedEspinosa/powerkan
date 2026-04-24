package platform

import (
	"os"
	"path/filepath"
)

const appName = "powerkan"

// Paths defines filesystem locations used by the app.
type Paths struct {
	RootDir      string
	ConfigFile   string
	DatabaseFile string
	LogsDir      string
	LogFile      string
	ExportsDir   string
}

// ResolvePaths builds the default app-support layout or applies config overrides.
func ResolvePaths(configOverride string) (Paths, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, err
	}

	root := filepath.Join(base, appName)
	configFile := filepath.Join(root, "config.yaml")
	if configOverride != "" {
		configFile = configOverride
	}

	logsDir := filepath.Join(root, "logs")
	exportsDir := filepath.Join(root, "exports")

	return Paths{
		RootDir:      root,
		ConfigFile:   configFile,
		DatabaseFile: filepath.Join(root, "kanban.db"),
		LogsDir:      logsDir,
		LogFile:      filepath.Join(logsDir, "powerkan.log"),
		ExportsDir:   exportsDir,
	}, nil
}

// EnsureDirectories creates the required app folders on first run.
func EnsureDirectories(paths Paths) error {
	for _, dir := range []string{paths.RootDir, paths.LogsDir, paths.ExportsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	return nil
}

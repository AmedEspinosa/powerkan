package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsUsesUserConfigDir(t *testing.T) {
	paths, err := ResolvePaths("")
	if err != nil {
		t.Fatalf("ResolvePaths returned error: %v", err)
	}

	if filepath.Base(paths.RootDir) != "powerkan" {
		t.Fatalf("expected root dir to end with powerkan, got %q", paths.RootDir)
	}
	if filepath.Base(paths.DatabaseFile) != "kanban.db" {
		t.Fatalf("expected db file name, got %q", paths.DatabaseFile)
	}
}

func TestEnsureDirectoriesCreatesExpectedFolders(t *testing.T) {
	root := t.TempDir()
	paths := Paths{
		RootDir:    filepath.Join(root, "app"),
		LogsDir:    filepath.Join(root, "app", "logs"),
		ExportsDir: filepath.Join(root, "app", "exports"),
	}

	if err := EnsureDirectories(paths); err != nil {
		t.Fatalf("EnsureDirectories returned error: %v", err)
	}

	for _, dir := range []string{paths.RootDir, paths.LogsDir, paths.ExportsDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("Stat returned error for %q: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("expected %q to be a directory", dir)
		}
	}
}

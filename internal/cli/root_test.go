package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/amedespinosa/powerkan/internal/platform"
)

func TestRootCommandIncludesPhaseZeroCommands(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()

	if _, _, err := cmd.Find([]string{"export", "ticket"}); err != nil {
		t.Fatalf("expected export ticket command: %v", err)
	}
	if _, _, err := cmd.Find([]string{"webhook", "sprint-end"}); err != nil {
		t.Fatalf("expected webhook sprint-end command: %v", err)
	}
}

func TestExportTicketRejectsUnknownFormatBeforeBootstrap(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"export", "ticket", "--id", "ABC-FEA-2604241200", "--format", "json"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected invalid format error")
	}
	if err.Error() != `invalid --format "json": must be md or csv` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRootCommandRejectsNonInteractiveTUIBeforeBootstrap(t *testing.T) {
	originalIsTerminal := isTerminal
	isTerminal = func(fd uintptr) bool { return false }
	defer func() { isTerminal = originalIsTerminal }()

	home := t.TempDir()
	t.Setenv("HOME", home)

	paths, err := platform.ResolvePaths("")
	if err != nil {
		t.Fatalf("ResolvePaths returned error: %v", err)
	}

	cmd := NewRootCommand()
	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected non-interactive terminal error")
	}
	if err.Error() != "powerkan TUI requires an interactive terminal" {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, statErr := os.Stat(paths.RootDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected app root %q to not exist, stat error=%v", paths.RootDir, statErr)
	}

	expectedRoot := filepath.Join(home, "Library", "Application Support", "powerkan")
	if paths.RootDir != expectedRoot {
		t.Fatalf("expected root dir %q, got %q", expectedRoot, paths.RootDir)
	}
}

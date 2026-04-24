package cli

import "testing"

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

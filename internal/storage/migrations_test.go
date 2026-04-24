package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestApplyMigrationsCreatesTablesAndIsIdempotent(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "kanban.db")
	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := ApplyMigrations(context.Background(), db); err != nil {
		t.Fatalf("ApplyMigrations returned error: %v", err)
	}
	if err := ApplyMigrations(context.Background(), db); err != nil {
		t.Fatalf("second ApplyMigrations returned error: %v", err)
	}

	for _, table := range []string{"schema_version", "epics", "sprints", "tickets", "ticket_comments", "webhook_posts"} {
		var count int
		err := db.QueryRowContext(context.Background(), "SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count)
		if err != nil {
			t.Fatalf("table lookup for %q failed: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("expected table %q to exist exactly once, got %d", table, count)
		}
	}
}

package storage

import (
	"context"
	"path/filepath"
	"sync"
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

func TestApplyMigrationsSucceedsAcrossConcurrentStartup(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "kanban.db")
	db1, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open first db returned error: %v", err)
	}
	defer func() { _ = db1.Close() }()

	db2, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open second db returned error: %v", err)
	}
	defer func() { _ = db2.Close() }()

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		errs <- ApplyMigrations(context.Background(), db1)
	}()

	go func() {
		defer wg.Done()
		errs <- ApplyMigrations(context.Background(), db2)
	}()

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("expected concurrent migrations to succeed, got %v", err)
		}
	}

	var count int
	if err := db1.QueryRowContext(context.Background(), `SELECT COUNT(1) FROM schema_version WHERE version = ?`, migrations[0].version).Scan(&count); err != nil {
		t.Fatalf("schema_version lookup failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one schema_version row for version %d, got %d", migrations[0].version, count)
	}

	for _, table := range []string{"epics", "sprints", "tickets", "ticket_comments", "webhook_posts"} {
		if err := db1.QueryRowContext(context.Background(), "SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil {
			t.Fatalf("table lookup for %q failed: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("expected table %q to exist exactly once, got %d", table, count)
		}
	}
}

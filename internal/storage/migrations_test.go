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

func TestApplyMigrationsUpgradesLegacyTicketsTableWithPosition(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "kanban.db")
	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(context.Background(), `
CREATE TABLE schema_version (
	version INTEGER PRIMARY KEY,
	applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO schema_version(version) VALUES(1);
CREATE TABLE epics (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	created_at DATETIME NOT NULL
);
CREATE TABLE sprints (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	quarter TEXT NOT NULL,
	start_date DATE NOT NULL,
	end_date DATE NOT NULL,
	created_at DATETIME NOT NULL,
	completed_at DATETIME
);
CREATE TABLE tickets (
	id INTEGER PRIMARY KEY,
	ticket_id TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('NOT_STARTED', 'IN_PROGRESS', 'UNDER_REVIEW', 'DONE')),
	type TEXT NOT NULL CHECK (type IN ('FEATURE', 'BUG', 'FIX', 'DOCS')),
	blocked INTEGER NOT NULL DEFAULT 0,
	story_points INTEGER NOT NULL DEFAULT 0,
	epic_id INTEGER NOT NULL,
	sprint_id INTEGER,
	github_pr_url TEXT,
	description TEXT,
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL,
	FOREIGN KEY (epic_id) REFERENCES epics(id),
	FOREIGN KEY (sprint_id) REFERENCES sprints(id)
);
CREATE TABLE ticket_comments (
	id INTEGER PRIMARY KEY,
	ticket_id INTEGER NOT NULL,
	kind TEXT NOT NULL CHECK (kind IN ('TEXT', 'URL', 'FILE_PATH')),
	body TEXT NOT NULL,
	created_at DATETIME NOT NULL,
	FOREIGN KEY (ticket_id) REFERENCES tickets(id)
);
CREATE TABLE webhook_posts (
	id INTEGER PRIMARY KEY,
	sprint_id INTEGER NOT NULL,
	endpoint_url TEXT NOT NULL,
	payload_hash TEXT NOT NULL,
	posted_at DATETIME NOT NULL,
	UNIQUE (sprint_id, endpoint_url, payload_hash),
	FOREIGN KEY (sprint_id) REFERENCES sprints(id)
);`); err != nil {
		t.Fatalf("seed legacy schema returned error: %v", err)
	}

	if err := ApplyMigrations(context.Background(), db); err != nil {
		t.Fatalf("ApplyMigrations returned error: %v", err)
	}

	rows, err := db.QueryContext(context.Background(), `PRAGMA table_info(tickets)`)
	if err != nil {
		t.Fatalf("table_info query returned error: %v", err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("table_info scan returned error: %v", err)
		}
		if name == "position" {
			found = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows returned error: %v", err)
	}
	if !found {
		t.Fatal("expected tickets.position column after migration")
	}
}

package storage

import (
	"context"
	"database/sql"
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

func TestApplyMigrationsBackfillsTicketPositionsForLegacyData(t *testing.T) {
	t.Parallel()

	db := openLegacyV2DB(t)
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(context.Background(), `
INSERT INTO epics(id, name, created_at) VALUES (1, 'Epic', '2026-04-20T10:00:00Z');
INSERT INTO sprints(id, name, quarter, start_date, end_date, created_at) VALUES (1, 'Sprint 1', '2026Q2', '2026-04-20', '2026-04-26', '2026-04-20T10:00:00Z');
INSERT INTO tickets(id, ticket_id, title, status, type, blocked, story_points, epic_id, sprint_id, github_pr_url, description, created_at, updated_at, position) VALUES
	(1, 'TICKET-001', 'First', 'NOT_STARTED', 'FEATURE', 0, 1, 1, 1, '', '', '2026-04-20T10:00:00Z', '2026-04-20T10:03:00Z', 5),
	(2, 'TICKET-002', 'Second', 'NOT_STARTED', 'FEATURE', 0, 1, 1, 1, '', '', '2026-04-20T10:00:00Z', '2026-04-20T10:02:00Z', 5),
	(3, 'TICKET-003', 'Third', 'NOT_STARTED', 'FEATURE', 0, 1, 1, NULL, '', '', '2026-04-20T10:00:00Z', '2026-04-20T10:01:00Z', 9),
	(4, 'TICKET-004', 'Fourth', 'NOT_STARTED', 'FEATURE', 0, 1, 1, NULL, '', '', '2026-04-20T10:00:00Z', '2026-04-20T10:04:00Z', 9),
	(5, 'TICKET-005', 'Fifth', 'IN_PROGRESS', 'FEATURE', 0, 1, 1, 1, '', '', '2026-04-20T10:00:00Z', '2026-04-20T10:05:00Z', 7),
	(6, 'TICKET-006', 'Sixth', 'IN_PROGRESS', 'FEATURE', 0, 1, 1, 1, '', '', '2026-04-20T10:00:00Z', '2026-04-20T10:01:00Z', 3);`); err != nil {
		t.Fatalf("seed legacy tickets returned error: %v", err)
	}

	if err := ApplyMigrations(context.Background(), db); err != nil {
		t.Fatalf("ApplyMigrations returned error: %v", err)
	}

	assertTicketPositions(t, db, []ticketPositionExpectation{
		{ticketID: "TICKET-004", position: 0},
		{ticketID: "TICKET-003", position: 1},
		{ticketID: "TICKET-001", position: 0},
		{ticketID: "TICKET-002", position: 1},
		{ticketID: "TICKET-006", position: 0},
		{ticketID: "TICKET-005", position: 1},
	})
}

func TestApplyMigrationsBackfillsWhenVersionTwoAlreadyApplied(t *testing.T) {
	t.Parallel()

	db := openLegacyV2DB(t)
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(context.Background(), `
INSERT INTO epics(id, name, created_at) VALUES (1, 'Epic', '2026-04-20T10:00:00Z');
INSERT INTO tickets(id, ticket_id, title, status, type, blocked, story_points, epic_id, sprint_id, github_pr_url, description, created_at, updated_at, position) VALUES
	(1, 'TICKET-001', 'First', 'DONE', 'FEATURE', 0, 1, 1, NULL, '', '', '2026-04-20T10:00:00Z', '2026-04-20T10:02:00Z', 4),
	(2, 'TICKET-002', 'Second', 'DONE', 'FEATURE', 0, 1, 1, NULL, '', '', '2026-04-20T10:00:00Z', '2026-04-20T10:01:00Z', 4);`); err != nil {
		t.Fatalf("seed version two data returned error: %v", err)
	}

	if err := ApplyMigrations(context.Background(), db); err != nil {
		t.Fatalf("ApplyMigrations returned error: %v", err)
	}

	assertTicketPositions(t, db, []ticketPositionExpectation{
		{ticketID: "TICKET-001", position: 0},
		{ticketID: "TICKET-002", position: 1},
	})

	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(1) FROM schema_version WHERE version = 3`).Scan(&count); err != nil {
		t.Fatalf("schema_version lookup for v3 failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected schema version 3 to be recorded once, got %d", count)
	}
}

type ticketPositionExpectation struct {
	ticketID string
	position int
}

func openLegacyV2DB(t *testing.T) *sql.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "kanban.db")
	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	if _, err := db.ExecContext(context.Background(), `
CREATE TABLE schema_version (
	version INTEGER PRIMARY KEY,
	applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO schema_version(version) VALUES(1);
INSERT INTO schema_version(version) VALUES(2);
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
	position INTEGER NOT NULL DEFAULT 0,
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
		t.Fatalf("seed legacy v2 schema returned error: %v", err)
	}

	return db
}

func assertTicketPositions(t *testing.T, db *sql.DB, expected []ticketPositionExpectation) {
	t.Helper()

	for _, want := range expected {
		var got int
		if err := db.QueryRowContext(context.Background(), `SELECT position FROM tickets WHERE ticket_id = ?`, want.ticketID).Scan(&got); err != nil {
			t.Fatalf("position lookup for %s failed: %v", want.ticketID, err)
		}
		if got != want.position {
			t.Fatalf("expected %s position %d, got %d", want.ticketID, want.position, got)
		}
	}
}

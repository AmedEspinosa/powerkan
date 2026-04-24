package storage

import (
	"context"
	"database/sql"
	"fmt"
)

type migration struct {
	version int
	sql     string
}

var migrations = []migration{
	{
		version: 1,
		sql: `
CREATE TABLE IF NOT EXISTS epics (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS sprints (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	quarter TEXT NOT NULL,
	start_date DATE NOT NULL,
	end_date DATE NOT NULL,
	created_at DATETIME NOT NULL,
	completed_at DATETIME
);

CREATE TABLE IF NOT EXISTS tickets (
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

CREATE TABLE IF NOT EXISTS ticket_comments (
	id INTEGER PRIMARY KEY,
	ticket_id INTEGER NOT NULL,
	kind TEXT NOT NULL CHECK (kind IN ('TEXT', 'URL', 'FILE_PATH')),
	body TEXT NOT NULL,
	created_at DATETIME NOT NULL,
	FOREIGN KEY (ticket_id) REFERENCES tickets(id)
);

CREATE TABLE IF NOT EXISTS webhook_posts (
	id INTEGER PRIMARY KEY,
	sprint_id INTEGER NOT NULL,
	endpoint_url TEXT NOT NULL,
	payload_hash TEXT NOT NULL,
	posted_at DATETIME NOT NULL,
	UNIQUE (sprint_id, endpoint_url, payload_hash),
	FOREIGN KEY (sprint_id) REFERENCES sprints(id)
);
`,
	},
}

// ApplyMigrations creates the schema_version table and applies pending migrations.
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER PRIMARY KEY,
	applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`); err != nil {
		return fmt.Errorf("create schema_version table: %w", err)
	}

	for _, migration := range migrations {
		applied, err := migrationApplied(ctx, db, migration.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", migration.version, err)
		}

		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", migration.version, err)
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version(version, applied_at) VALUES(?, CURRENT_TIMESTAMP)`, migration.version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", migration.version, err)
		}
	}

	return nil
}

func migrationApplied(ctx context.Context, db *sql.DB, version int) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_version WHERE version = ?`, version).Scan(&count); err != nil {
		return false, fmt.Errorf("check migration %d: %w", version, err)
	}

	return count > 0, nil
}

package storage

import (
	"context"
	"database/sql"
	"fmt"
)

type migration struct {
	version int
	sql     string
	run     func(context.Context, *sql.Tx) error
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
	{
		version: 2,
		sql: `
ALTER TABLE tickets ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
`,
	},
	{
		version: 3,
		run:     backfillTicketPositions,
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
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", migration.version, err)
		}

		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_version(version, applied_at) VALUES(?, CURRENT_TIMESTAMP)`, migration.version)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("claim migration %d: %w", migration.version, err)
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("inspect migration %d claim: %w", migration.version, err)
		}

		if rowsAffected == 0 {
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit skipped migration %d: %w", migration.version, err)
			}
			continue
		}

		if migration.sql != "" {
			if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("apply migration %d: %w", migration.version, err)
			}
		}
		if migration.run != nil {
			if err := migration.run(ctx, tx); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("apply migration %d: %w", migration.version, err)
			}
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", migration.version, err)
		}
	}

	return nil
}

type ticketPositionRow struct {
	id       int64
	status   string
	sprintID sql.NullInt64
}

func backfillTicketPositions(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
SELECT id, status, sprint_id
FROM tickets
ORDER BY status, COALESCE(sprint_id, -1), COALESCE(position, 0), updated_at DESC, id DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	updateStmt, err := tx.PrepareContext(ctx, `UPDATE tickets SET position = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer updateStmt.Close()

	var (
		currentKey string
		nextPos    int
	)
	for rows.Next() {
		var row ticketPositionRow
		if err := rows.Scan(&row.id, &row.status, &row.sprintID); err != nil {
			return err
		}
		key := row.status + "|backlog"
		if row.sprintID.Valid {
			key = fmt.Sprintf("%s|%d", row.status, row.sprintID.Int64)
		}
		if key != currentKey {
			currentKey = key
			nextPos = 0
		}
		if _, err := updateStmt.ExecContext(ctx, nextPos, row.id); err != nil {
			return err
		}
		nextPos++
	}

	return rows.Err()
}

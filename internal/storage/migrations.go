package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	story_points REAL NOT NULL DEFAULT 0,
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
	{version: 2, sql: `ALTER TABLE tickets ADD COLUMN position INTEGER NOT NULL DEFAULT 0;`},
	{version: 3, run: backfillTicketPositions},
	{version: 4, run: migrateTicketStoryPointsToReal},
	{version: 5, run: migrateToSprintMembershipSchema},
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

	hasPending, err := hasPendingMigrations(ctx, db)
	if err != nil {
		return err
	}

	var backupPath string
	if hasPending {
		backupPath, err = backupDatabase(ctx, db)
		if err != nil {
			return err
		}
		log.Printf("migration backup created path=%s", backupPath)
	}

	for _, migration := range migrations {
		log.Printf("migration start version=%d", migration.version)

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return restoreMigrationFailure(ctx, db, backupPath, fmt.Errorf("begin migration %d: %w", migration.version, err))
		}

		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_version(version, applied_at) VALUES(?, CURRENT_TIMESTAMP)`, migration.version)
		if err != nil {
			_ = tx.Rollback()
			return restoreMigrationFailure(ctx, db, backupPath, fmt.Errorf("claim migration %d: %w", migration.version, err))
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			_ = tx.Rollback()
			return restoreMigrationFailure(ctx, db, backupPath, fmt.Errorf("inspect migration %d claim: %w", migration.version, err))
		}

		if rowsAffected == 0 {
			if err := tx.Commit(); err != nil {
				return restoreMigrationFailure(ctx, db, backupPath, fmt.Errorf("commit skipped migration %d: %w", migration.version, err))
			}
			log.Printf("migration skipped version=%d", migration.version)
			continue
		}

		if migration.sql != "" {
			if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
				_ = tx.Rollback()
				return restoreMigrationFailure(ctx, db, backupPath, fmt.Errorf("apply migration %d: %w", migration.version, err))
			}
		}
		if migration.run != nil {
			if err := migration.run(ctx, tx); err != nil {
				_ = tx.Rollback()
				return restoreMigrationFailure(ctx, db, backupPath, fmt.Errorf("apply migration %d: %w", migration.version, err))
			}
		}

		if err := tx.Commit(); err != nil {
			return restoreMigrationFailure(ctx, db, backupPath, fmt.Errorf("commit migration %d: %w", migration.version, err))
		}
		log.Printf("migration complete version=%d", migration.version)
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

func migrateTicketStoryPointsToReal(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer func() {
		_, _ = tx.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	}()

	if _, err := tx.ExecContext(ctx, `
CREATE TABLE tickets_new (
	id INTEGER PRIMARY KEY,
	ticket_id TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('NOT_STARTED', 'IN_PROGRESS', 'UNDER_REVIEW', 'DONE')),
	type TEXT NOT NULL CHECK (type IN ('FEATURE', 'BUG', 'FIX', 'DOCS')),
	blocked INTEGER NOT NULL DEFAULT 0,
	story_points REAL NOT NULL DEFAULT 0,
	epic_id INTEGER NOT NULL,
	sprint_id INTEGER,
	github_pr_url TEXT,
	description TEXT,
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL,
	position INTEGER NOT NULL DEFAULT 0,
	FOREIGN KEY (epic_id) REFERENCES epics(id),
	FOREIGN KEY (sprint_id) REFERENCES sprints(id)
);`); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO tickets_new (
	id, ticket_id, title, status, type, blocked, story_points, epic_id, sprint_id,
	github_pr_url, description, created_at, updated_at, position
)
SELECT
	id, ticket_id, title, status, type, blocked, story_points, epic_id, sprint_id,
	github_pr_url, description, created_at, updated_at, position
FROM tickets;`); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DROP TABLE tickets`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE tickets_new RENAME TO tickets`); err != nil {
		return err
	}
	return nil
}

func migrateToSprintMembershipSchema(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer func() {
		_, _ = tx.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	}()

	if _, err := tx.ExecContext(ctx, `
CREATE TABLE epics_new (
	id INTEGER PRIMARY KEY,
	title TEXT NOT NULL UNIQUE,
	status TEXT NOT NULL CHECK (status IN ('open', 'done')),
	color TEXT,
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
);

CREATE TABLE sprints_new (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	goal TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL CHECK (status IN ('planned', 'active', 'closed')),
	starts_on DATE NOT NULL,
	ends_on DATE NOT NULL,
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL,
	closed_at DATETIME
);

CREATE TABLE tickets_new (
	id INTEGER PRIMARY KEY,
	ticket_id TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('NOT_STARTED', 'IN_PROGRESS', 'UNDER_REVIEW', 'DONE')),
	type TEXT NOT NULL CHECK (type IN ('FEATURE', 'BUG', 'FIX', 'DOCS')),
	blocked INTEGER NOT NULL DEFAULT 0,
	story_points REAL NOT NULL DEFAULT 0,
	epic_id INTEGER,
	github_pr_url TEXT,
	description TEXT,
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL,
	position INTEGER NOT NULL DEFAULT 0,
	FOREIGN KEY (epic_id) REFERENCES epics_new(id) ON DELETE SET NULL
);

CREATE TABLE sprint_tickets (
	sprint_id INTEGER NOT NULL,
	ticket_id INTEGER NOT NULL,
	added_at DATETIME NOT NULL,
	removed_at DATETIME,
	PRIMARY KEY (sprint_id, ticket_id, added_at),
	FOREIGN KEY (sprint_id) REFERENCES sprints_new(id) ON DELETE CASCADE,
	FOREIGN KEY (ticket_id) REFERENCES tickets_new(id) ON DELETE CASCADE
);
`); err != nil {
		return err
	}

	if tableExistsTx(ctx, tx, "epics") {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO epics_new (id, title, status, color, created_at, updated_at)
SELECT id, name, 'open', NULL, created_at, created_at
FROM epics`); err != nil {
			return err
		}
	}

	if tableExistsTx(ctx, tx, "sprints") {
		now := time.Now().UTC().Format("2006-01-02")
		if _, err := tx.ExecContext(ctx, `
INSERT INTO sprints_new (id, name, goal, status, starts_on, ends_on, created_at, updated_at, closed_at)
SELECT
	id,
	name,
	'',
	CASE
		WHEN completed_at IS NOT NULL OR end_date < ? THEN 'closed'
		WHEN start_date <= ? AND end_date >= ? THEN 'active'
		ELSE 'planned'
	END,
	start_date,
	end_date,
	created_at,
	COALESCE(completed_at, created_at),
	completed_at
FROM sprints`, now, now, now); err != nil {
			return err
		}
	}

	if tableExistsTx(ctx, tx, "tickets") {
		if hasColumnTx(ctx, tx, "tickets", "sprint_id") {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO tickets_new (
	id, ticket_id, title, status, type, blocked, story_points, epic_id,
	github_pr_url, description, created_at, updated_at, position
)
SELECT
	id, ticket_id, title, status, type, blocked, story_points, epic_id,
	github_pr_url, description, created_at, updated_at, COALESCE(position, 0)
FROM tickets`); err != nil {
				return err
			}

			if _, err := tx.ExecContext(ctx, `
INSERT INTO sprint_tickets (sprint_id, ticket_id, added_at, removed_at)
SELECT
	t.sprint_id,
	t.id,
	t.created_at,
	CASE
		WHEN s.status = 'closed' THEN COALESCE(s.closed_at, t.updated_at)
		ELSE NULL
	END
FROM tickets t
INNER JOIN sprints_new s ON s.id = t.sprint_id
WHERE t.sprint_id IS NOT NULL`); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO tickets_new (
	id, ticket_id, title, status, type, blocked, story_points, epic_id,
	github_pr_url, description, created_at, updated_at, position
)
SELECT
	id, ticket_id, title, status, type, blocked, story_points, epic_id,
	github_pr_url, description, created_at, updated_at, COALESCE(position, 0)
FROM tickets`); err != nil {
				return err
			}
		}
	}

	if tableExistsTx(ctx, tx, "ticket_comments") {
		if _, err := tx.ExecContext(ctx, `
ALTER TABLE ticket_comments RENAME TO ticket_comments_old`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE ticket_comments (
	id INTEGER PRIMARY KEY,
	ticket_id INTEGER NOT NULL,
	kind TEXT NOT NULL CHECK (kind IN ('TEXT', 'URL', 'FILE_PATH')),
	body TEXT NOT NULL,
	created_at DATETIME NOT NULL,
	FOREIGN KEY (ticket_id) REFERENCES tickets_new(id) ON DELETE CASCADE
);`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO ticket_comments(id, ticket_id, kind, body, created_at)
SELECT id, ticket_id, kind, body, created_at FROM ticket_comments_old`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE ticket_comments_old`); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE ticket_comments (
	id INTEGER PRIMARY KEY,
	ticket_id INTEGER NOT NULL,
	kind TEXT NOT NULL CHECK (kind IN ('TEXT', 'URL', 'FILE_PATH')),
	body TEXT NOT NULL,
	created_at DATETIME NOT NULL,
	FOREIGN KEY (ticket_id) REFERENCES tickets_new(id) ON DELETE CASCADE
);`); err != nil {
			return err
		}
	}

	if tableExistsTx(ctx, tx, "webhook_posts") {
		if _, err := tx.ExecContext(ctx, `
ALTER TABLE webhook_posts RENAME TO webhook_posts_old`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE webhook_posts (
	id INTEGER PRIMARY KEY,
	sprint_id INTEGER NOT NULL,
	endpoint_url TEXT NOT NULL,
	payload_hash TEXT NOT NULL,
	posted_at DATETIME NOT NULL,
	UNIQUE (sprint_id, endpoint_url, payload_hash),
	FOREIGN KEY (sprint_id) REFERENCES sprints_new(id) ON DELETE CASCADE
);`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO webhook_posts(id, sprint_id, endpoint_url, payload_hash, posted_at)
SELECT id, sprint_id, endpoint_url, payload_hash, posted_at FROM webhook_posts_old`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE webhook_posts_old`); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE webhook_posts (
	id INTEGER PRIMARY KEY,
	sprint_id INTEGER NOT NULL,
	endpoint_url TEXT NOT NULL,
	payload_hash TEXT NOT NULL,
	posted_at DATETIME NOT NULL,
	UNIQUE (sprint_id, endpoint_url, payload_hash),
	FOREIGN KEY (sprint_id) REFERENCES sprints_new(id) ON DELETE CASCADE
);`); err != nil {
			return err
		}
	}

	for _, statement := range []string{
		`DROP TABLE IF EXISTS tickets`,
		`DROP TABLE IF EXISTS sprints`,
		`DROP TABLE IF EXISTS epics`,
		`ALTER TABLE epics_new RENAME TO epics`,
		`ALTER TABLE sprints_new RENAME TO sprints`,
		`ALTER TABLE tickets_new RENAME TO tickets`,
		`CREATE INDEX IF NOT EXISTS idx_tickets_epic_id ON tickets(epic_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sprints_status ON sprints(status)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_sprints_single_active ON sprints(status) WHERE status = 'active'`,
		`CREATE INDEX IF NOT EXISTS idx_sprint_tickets_ticket_removed ON sprint_tickets(ticket_id, removed_at)`,
		`CREATE INDEX IF NOT EXISTS idx_sprint_tickets_sprint_removed ON sprint_tickets(sprint_id, removed_at)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	return nil
}

func hasPendingMigrations(ctx context.Context, db *sql.DB) (bool, error) {
	for _, migration := range migrations {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_version WHERE version = ?`, migration.version).Scan(&count); err != nil {
			return false, fmt.Errorf("check schema_version %d: %w", migration.version, err)
		}
		if count == 0 {
			return true, nil
		}
	}
	return false, nil
}

func backupDatabase(ctx context.Context, db *sql.DB) (string, error) {
	path, err := databasePath(ctx, db)
	if err != nil {
		return "", err
	}
	if path == "" || path == ":memory:" {
		return "", nil
	}
	backupPath := fmt.Sprintf("%s.bak.%s", path, time.Now().UTC().Format("20060102T150405.000000000"))
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`VACUUM INTO %s`, quoteSQLiteString(backupPath))); err != nil {
		return "", fmt.Errorf("backup database: %w", err)
	}
	return backupPath, nil
}

func restoreMigrationFailure(ctx context.Context, db *sql.DB, backupPath string, migrationErr error) error {
	if backupPath == "" {
		return migrationErr
	}
	if err := restoreDatabaseFromBackup(ctx, db, backupPath); err != nil {
		return fmt.Errorf("%v; restore backup: %w", migrationErr, err)
	}
	return fmt.Errorf("%v; restored backup %s", migrationErr, backupPath)
}

func restoreDatabaseFromBackup(ctx context.Context, db *sql.DB, backupPath string) error {
	path, err := databasePath(ctx, db)
	if err != nil {
		return err
	}
	if path == "" || path == ":memory:" {
		return nil
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close database before restore: %w", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
	source, err := os.Open(backupPath)
	if err != nil {
		return err
	}
	defer source.Close()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	target, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer target.Close()

	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return nil
}

func databasePath(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return "", fmt.Errorf("query database_list: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var seq int
		var name string
		var file string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return "", err
		}
		if name == "main" {
			return file, nil
		}
	}
	return "", rows.Err()
}

func quoteSQLiteString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func tableExistsTx(ctx context.Context, tx *sql.Tx, name string) bool {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

func hasColumnTx(ctx context.Context, tx *sql.Tx, table, column string) bool {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var dataType string
		var notNull int
		var dfltValue any
		var pk int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &dfltValue, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

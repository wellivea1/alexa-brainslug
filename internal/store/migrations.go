package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migration is one forward schema or data change.
//
// There are no down migrations. Rolling a schema backwards on a local SQLite
// file is more likely to lose history than to recover from a bad deploy; the
// recovery path is to restore the file.
type migration struct {
	// version is the ordinal. It must never be reused or reordered once
	// released, because applied versions are recorded by number.
	version int

	// name describes the change for operators reading the migrations table.
	name string

	// statements run in order inside a single transaction with the version
	// record, so a failure leaves the database on the previous version rather
	// than half-migrated.
	statements []string
}

// migrations is the ordered list of every change ever applied. Append only.
var migrations = []migration{
	{
		version: 1,
		name:    "create interactions table",
		statements: []string{
			`CREATE TABLE IF NOT EXISTS interactions (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				created_at TEXT NOT NULL,
				alexa_user_id TEXT NOT NULL,
				session_id TEXT NOT NULL,
				provider TEXT NOT NULL,
				model TEXT NOT NULL,
				prompt TEXT NOT NULL,
				short_answer TEXT NOT NULL,
				full_answer TEXT NOT NULL,
				latency_ms INTEGER NOT NULL DEFAULT 0,
				status TEXT NOT NULL,
				error_message TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE INDEX IF NOT EXISTS idx_interactions_user_created
				ON interactions (alexa_user_id, created_at DESC, id DESC)`,
			`CREATE INDEX IF NOT EXISTS idx_interactions_created
				ON interactions (created_at DESC, id DESC)`,
		},
	},
	{
		version: 2,
		name:    "pad created_at to a fixed width",
		// Timestamps were written with time.RFC3339Nano, which drops trailing
		// zeros from the fractional second. That makes the column sort
		// incorrectly as text: "...T12:00:00Z" compares greater than
		// "...T12:00:00.1Z", because 'Z' sorts after '.', so an older row can
		// win an ORDER BY created_at DESC. That is the query behind the "last
		// response" intent, which could therefore speak a stale answer.
		//
		// Rows are now written with a fixed nine-digit fraction. These two
		// statements pad rows written before that change: the first pads a
		// short fraction, the second adds one where it was dropped entirely.
		statements: []string{
			`UPDATE interactions
				SET created_at =
					substr(created_at, 1, instr(created_at, '.'))
					|| substr(
						substr(created_at, instr(created_at, '.') + 1, length(created_at) - instr(created_at, '.') - 1)
							|| '000000000',
						1, 9)
					|| 'Z'
				WHERE created_at LIKE '%.%Z'
				  AND length(created_at) <> 30`,
			`UPDATE interactions
				SET created_at = substr(created_at, 1, 19) || '.000000000Z'
				WHERE created_at NOT LIKE '%.%'
				  AND length(created_at) = 20`,
		},
	},
}

// migrate applies every migration that has not been recorded yet.
//
// Each migration and its version record share a transaction, so an
// interrupted run leaves the database on a known version.
func migrate(ctx context.Context, db *sql.DB) error {
	const createVersions = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`
	if _, err := db.ExecContext(ctx, createVersions); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

// applyMigration runs one migration and records it atomically.
func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		// A rollback after a successful commit is a no-op; this only matters
		// on the error paths below.
		_ = tx.Rollback()
	}()

	for _, statement := range m.statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	const record = `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`
	if _, err := tx.ExecContext(ctx, record, m.version, m.name, nowUTC().Format(timestampFormat)); err != nil {
		return err
	}
	return tx.Commit()
}

// appliedVersions reads the set of migrations already recorded.
func appliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return applied, nil
}

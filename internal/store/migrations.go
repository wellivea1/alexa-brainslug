package store

import (
	"context"
	"database/sql"
)

const schema = `
CREATE TABLE IF NOT EXISTS interactions (
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
);

CREATE INDEX IF NOT EXISTS idx_interactions_user_created
	ON interactions (alexa_user_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_interactions_created
	ON interactions (created_at DESC, id DESC);
`

func runMigrations(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, schema)
	return err
}

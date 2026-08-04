// Package store persists Alexa interactions in a local SQLite database.
//
// The database holds the questions asked and the answers given, so it is
// private data. Nothing here writes credentials: provider errors are redacted
// by the caller before they arrive.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// modernc.org/sqlite is a pure-Go SQLite driver. It is imported for its
	// side effect of registering the "sqlite" driver name. Pure Go keeps
	// CGO_ENABLED=0 builds and the scratch container image possible.
	_ "modernc.org/sqlite"
)

// ErrNotFound means a query matched no rows.
var ErrNotFound = errors.New("not found")

// timestampFormat is RFC 3339 with a fixed nine-digit fraction.
//
// The fixed width is load bearing. created_at is a TEXT column and is sorted
// as text, so a variable-width fraction sorts incorrectly: time.RFC3339Nano
// drops trailing zeros, and "...T12:00:00Z" compares greater than
// "...T12:00:00.1Z". Padding every value to the same width makes text order
// match chronological order.
const timestampFormat = "2006-01-02T15:04:05.000000000Z07:00"

// defaultListLimit and maxListLimit bound history queries so that a caller
// cannot ask for an unbounded result set.
const (
	defaultListLimit = 25
	maxListLimit     = 100
)

// nowUTC is the clock, replaceable in tests.
var nowUTC = func() time.Time { return time.Now().UTC() }

// Store is a handle on the local history database. It is safe for concurrent
// use.
type Store struct {
	db *sql.DB
}

// Interaction is one question and its outcome.
type Interaction struct {
	ID          int64     `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	AlexaUserID string    `json:"alexa_user_id"`
	SessionID   string    `json:"session_id"`
	Provider    string    `json:"provider"`
	Model       string    `json:"model"`
	Prompt      string    `json:"prompt"`
	ShortAnswer string    `json:"short_answer"`
	FullAnswer  string    `json:"full_answer"`
	LatencyMS   int64     `json:"latency_ms"`

	// Status is "ok" or "error".
	Status string `json:"status"`

	// ErrorMessage is set when Status is "error". It has already been through
	// secret redaction.
	ErrorMessage string `json:"error_message,omitempty"`
}

// Open opens the database at path, creating the file and any missing parent
// directories, and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("sqlite path is empty")
	}
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	// SQLite allows one writer at a time. Serialising through a single
	// connection trades read concurrency the gateway does not need for the
	// absence of SQLITE_BUSY retries, which at one request per voice command
	// is the right trade.
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;`); err != nil {
		return nil, errors.Join(fmt.Errorf("configure sqlite: %w", err), db.Close())
	}
	if err := migrate(ctx, db); err != nil {
		return nil, errors.Join(fmt.Errorf("run migrations: %w", err), db.Close())
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// SaveInteraction records one interaction and returns its row ID.
func (s *Store) SaveInteraction(ctx context.Context, interaction Interaction) (int64, error) {
	if interaction.CreatedAt.IsZero() {
		interaction.CreatedAt = nowUTC()
	}

	const query = `
INSERT INTO interactions (
	created_at, alexa_user_id, session_id, provider, model, prompt,
	short_answer, full_answer, latency_ms, status, error_message
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	result, err := s.db.ExecContext(ctx, query,
		interaction.CreatedAt.UTC().Format(timestampFormat),
		interaction.AlexaUserID,
		interaction.SessionID,
		interaction.Provider,
		interaction.Model,
		interaction.Prompt,
		interaction.ShortAnswer,
		interaction.FullAnswer,
		interaction.LatencyMS,
		interaction.Status,
		interaction.ErrorMessage,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// interactionColumns is the shared select list, so that the column order
// always matches scanInteraction.
const interactionColumns = `id, created_at, alexa_user_id, session_id, provider, model, prompt,
	short_answer, full_answer, latency_ms, status, error_message`

// GetLatestResponse returns the most recent successful answer for a user, or
// ErrNotFound.
//
// Results are scoped to one Alexa user ID: one household member must not be
// able to replay another's answer.
func (s *Store) GetLatestResponse(ctx context.Context, userID string) (*Interaction, error) {
	query := `SELECT ` + interactionColumns + `
FROM interactions
WHERE alexa_user_id = ?
	AND status = 'ok'
	AND (short_answer <> '' OR full_answer <> '')
ORDER BY created_at DESC, id DESC
LIMIT 1`

	interaction, err := scanInteraction(s.db.QueryRowContext(ctx, query, userID))
	if err != nil {
		return nil, err
	}
	return &interaction, nil
}

// ListRecent returns the most recent interactions across all users, newest
// first. A limit outside (0, maxListLimit] is replaced with the default.
func (s *Store) ListRecent(ctx context.Context, limit int) ([]Interaction, error) {
	if limit <= 0 || limit > maxListLimit {
		limit = defaultListLimit
	}

	query := `SELECT ` + interactionColumns + `
FROM interactions
ORDER BY created_at DESC, id DESC
LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	interactions := make([]Interaction, 0, limit)
	for rows.Next() {
		interaction, err := scanInteraction(rows)
		if err != nil {
			return nil, err
		}
		interactions = append(interactions, interaction)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return interactions, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanInteraction reads one row. Column order must match interactionColumns.
func scanInteraction(scanner rowScanner) (Interaction, error) {
	var (
		interaction Interaction
		createdAt   string
	)
	err := scanner.Scan(
		&interaction.ID,
		&createdAt,
		&interaction.AlexaUserID,
		&interaction.SessionID,
		&interaction.Provider,
		&interaction.Model,
		&interaction.Prompt,
		&interaction.ShortAnswer,
		&interaction.FullAnswer,
		&interaction.LatencyMS,
		&interaction.Status,
		&interaction.ErrorMessage,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Interaction{}, ErrNotFound
		}
		return Interaction{}, err
	}

	// Accept both the fixed-width format and RFC3339Nano so that a database
	// carrying rows written before migration 2 still reads.
	parsed, err := time.Parse(timestampFormat, createdAt)
	if err != nil {
		if parsed, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return Interaction{}, fmt.Errorf("parse created_at %q: %w", createdAt, err)
		}
	}
	interaction.CreatedAt = parsed
	return interaction, nil
}

// ensureParentDir creates the directory holding the database file. In-memory
// and DSN-style paths have no parent directory to create.
func ensureParentDir(path string) error {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	// 0o750: the database holds private prompts and answers, so it is not
	// world readable.
	return os.MkdirAll(dir, 0o750)
}

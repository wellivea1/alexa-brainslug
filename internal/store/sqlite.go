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

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

type Interaction struct {
	ID           int64     `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	AlexaUserID  string    `json:"alexa_user_id"`
	SessionID    string    `json:"session_id"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	Prompt       string    `json:"prompt"`
	ShortAnswer  string    `json:"short_answer"`
	FullAnswer   string    `json:"full_answer"`
	LatencyMS    int64     `json:"latency_ms"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"error_message,omitempty"`
}

func Open(ctx context.Context, path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("sqlite path is empty")
	}
	if shouldCreateParent(path) {
		if err := mkdirForDB(path); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure sqlite: %w", err)
	}
	if err := runMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) SaveInteraction(ctx context.Context, interaction Interaction) (int64, error) {
	if interaction.CreatedAt.IsZero() {
		interaction.CreatedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO interactions (
	created_at, alexa_user_id, session_id, provider, model, prompt,
	short_answer, full_answer, latency_ms, status, error_message
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		interaction.CreatedAt.UTC().Format(time.RFC3339Nano),
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

func (s *Store) GetLatestResponse(ctx context.Context, userID string) (*Interaction, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, created_at, alexa_user_id, session_id, provider, model, prompt,
	short_answer, full_answer, latency_ms, status, error_message
FROM interactions
WHERE alexa_user_id = ?
	AND status = 'ok'
	AND (short_answer <> '' OR full_answer <> '')
ORDER BY created_at DESC, id DESC
LIMIT 1`, userID)
	interaction, err := scanInteraction(row)
	if err != nil {
		return nil, err
	}
	return &interaction, nil
}

func (s *Store) ListRecent(ctx context.Context, limit int) ([]Interaction, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, created_at, alexa_user_id, session_id, provider, model, prompt,
	short_answer, full_answer, latency_ms, status, error_message
FROM interactions
ORDER BY created_at DESC, id DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var interactions []Interaction
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

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInteraction(scanner rowScanner) (Interaction, error) {
	var interaction Interaction
	var createdAt string
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
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Interaction{}, fmt.Errorf("parse created_at: %w", err)
	}
	interaction.CreatedAt = parsed
	return interaction, nil
}

func shouldCreateParent(path string) bool {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return false
	}
	return true
}

func mkdirForDB(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return mkdirAll(dir)
}

var mkdirAll = func(path string) error {
	return os.MkdirAll(path, 0o755)
}

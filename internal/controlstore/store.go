// Package controlstore persists controller jobs in its own local SQLite database.
// This schema is independent of the voice gateway's interaction history.
package controlstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Register the controller's independent SQLite driver.

	"github.com/wellivea1/alexa-brainslug/internal/control"
)

// Store serializes SQLite transactions without sharing a database over the LAN.
type Store struct{ db *sql.DB }

// Open creates the independent, append-only operational schema.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("controller database path is required")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{`PRAGMA foreign_keys = ON`, `PRAGMA busy_timeout = 5000`, `PRAGMA journal_mode = WAL`} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	var voiceTables int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name IN ('interactions','worker_attempts')`).Scan(&voiceTables); err != nil || voiceTables != 0 {
		_ = db.Close()
		return nil, errors.New("controller requires a separate database from Alexa history")
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS control_migrations (version INTEGER PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS control_jobs (
		 id TEXT PRIMARY KEY, namespace TEXT NOT NULL, subject TEXT NOT NULL,
		 source TEXT NOT NULL, request_id TEXT NOT NULL, payload_hash TEXT NOT NULL,
		 state TEXT NOT NULL, revision INTEGER NOT NULL, created_at TEXT NOT NULL,
		 updated_at TEXT NOT NULL, record TEXT NOT NULL,
		 UNIQUE(namespace, subject, source, request_id))`,
		`CREATE TABLE IF NOT EXISTS control_events (
		 job_id TEXT NOT NULL REFERENCES control_jobs(id) ON DELETE CASCADE,
		 sequence INTEGER NOT NULL, state TEXT NOT NULL, message TEXT NOT NULL,
		 created_at TEXT NOT NULL, PRIMARY KEY(job_id, sequence))`,
		`CREATE TABLE IF NOT EXISTS control_conversations (
		 id TEXT PRIMARY KEY, namespace TEXT NOT NULL, subject TEXT NOT NULL, record TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS control_jobs_owner ON control_jobs(namespace, subject, created_at)`,
		`INSERT OR IGNORE INTO control_migrations(version) VALUES(1)`,
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var migrationTable int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name='control_migrations'`).Scan(&migrationTable); err != nil {
		_ = tx.Rollback()
		_ = db.Close()
		return nil, err
	}
	if migrationTable != 0 {
		var version int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM control_migrations`).Scan(&version); err != nil || version > 1 {
			_ = tx.Rollback()
			_ = db.Close()
			return nil, errors.New("controller database schema is newer than this binary")
		}
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			_ = tx.Rollback()
			_ = db.Close()
			return nil, fmt.Errorf("controller migration: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the local database.
func (s *Store) Close() error { return s.db.Close() }

// SaveConversation registers an owner-approved import without granting a new destination.
func (s *Store) SaveConversation(ctx context.Context, conversation *control.Conversation) error {
	data, err := json.Marshal(conversation)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO control_conversations(id,namespace,subject,record) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record WHERE namespace=excluded.namespace AND subject=excluded.subject`, conversation.ID, conversation.Principal.Namespace, conversation.Principal.Subject, string(data))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return control.ErrForbidden
	}
	return err
}

// NewID creates an unpredictable local identifier without platform identity.
func NewID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func decodeJob(row *sql.Row) (*control.Job, error) {
	var record string
	if err := row.Scan(&record); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, control.ErrNotFound
		}
		return nil, err
	}
	var job control.Job
	if err := json.Unmarshal([]byte(record), &job); err != nil {
		return nil, fmt.Errorf("decode saved task: %w", err)
	}
	return &job, nil
}

// Accept persists before spending resources, returning the original job for duplicates.
func (s *Store) Accept(ctx context.Context, request *control.Request, policy *control.Options) (*control.Job, bool, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return nil, false, err
	}
	hash := sha256.Sum256(data)
	hashText := hex.EncodeToString(hash[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var existingHash, record string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,record FROM control_jobs WHERE namespace=? AND subject=? AND source=? AND request_id=?`, request.Principal.Namespace, request.Principal.Subject, request.Source, request.RequestID).Scan(&existingHash, &record)
	if err == nil {
		if existingHash != hashText {
			return nil, false, control.ErrConflict
		}
		var job control.Job
		if err := json.Unmarshal([]byte(record), &job); err != nil {
			return nil, false, err
		}
		return &job, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if request.ReplyTo != "" {
		previous, err := decodeJob(tx.QueryRowContext(ctx, `SELECT record FROM control_jobs WHERE id=?`, request.ReplyTo))
		if err != nil {
			return nil, false, err
		}
		if previous.Request.Principal != request.Principal || previous.State != control.AwaitingInput {
			return nil, false, control.ErrConflict
		}
		previous.State = control.Canceled
		previous.Revision++
		previous.UpdatedAt = time.Now().UTC()
		encoded, err := json.Marshal(previous)
		if err != nil {
			return nil, false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE control_jobs SET state=?,revision=?,updated_at=?,record=? WHERE id=?`, previous.State, previous.Revision, stamp(previous.UpdatedAt), string(encoded), previous.ID); err != nil {
			return nil, false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO control_events(job_id,sequence,state,message,created_at) VALUES(?,?,?,'Clarification reply accepted',?)`, previous.ID, previous.Revision, previous.State, stamp(previous.UpdatedAt)); err != nil {
			return nil, false, err
		}
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM control_jobs WHERE state NOT IN ('completed','failed','canceled')`).Scan(&active); err != nil {
		return nil, false, err
	}
	if active >= policy.QueueLimit {
		return nil, false, control.ErrQueueFull
	}
	id, err := NewID()
	if err != nil {
		return nil, false, err
	}
	now := time.Now().UTC()
	job := &control.Job{ID: id, Request: *request, State: control.Accepted, Revision: 1, CreatedAt: now, UpdatedAt: now, PolicyVersion: policy.PolicyVersion, PolicySnapshot: policy}
	encoded, err := json.Marshal(job)
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO control_jobs(id,namespace,subject,source,request_id,payload_hash,state,revision,created_at,updated_at,record) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, request.Principal.Namespace, request.Principal.Subject, request.Source, request.RequestID, hashText, job.State, job.Revision, stamp(now), stamp(now), string(encoded)); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO control_events(job_id,sequence,state,message,created_at) VALUES(?,1,?,'Saved for routing',?)`, id, job.State, stamp(now)); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return job, true, nil
}

// Get retrieves an operational record; the use-case layer checks ownership.
func (s *Store) Get(ctx context.Context, id string) (*control.Job, error) {
	return decodeJob(s.db.QueryRowContext(ctx, `SELECT record FROM control_jobs WHERE id=?`, id))
}

// Attempt retrieves the exact saved dispatch for result-receipt confirmation.
func (s *Store) Attempt(ctx context.Context, id string) (*control.Job, error) {
	return decodeJob(s.db.QueryRowContext(ctx, `SELECT record FROM control_jobs WHERE json_extract(record,'$.attempt_id')=?`, id))
}

// List returns bounded recent jobs, optionally scoped to one principal.
func (s *Store) List(ctx context.Context, owner *control.Principal, limit int) ([]*control.Job, error) {
	limit = max(1, min(limit, 256))
	query := `SELECT record FROM control_jobs`
	var args []any
	if owner != nil {
		query += ` WHERE namespace=? AND subject=?`
		args = append(args, owner.Namespace, owner.Subject)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]*control.Job, 0)
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		var job control.Job
		if err := json.Unmarshal([]byte(text), &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, &job)
	}
	return jobs, rows.Err()
}

// Active returns every nonterminal record, including older jobs needing attention.
func (s *Store) Active(ctx context.Context) ([]*control.Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM control_jobs WHERE state NOT IN ('completed','failed','canceled') ORDER BY created_at LIMIT 256`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var jobs []*control.Job
	for rows.Next() {
		var text string
		var job control.Job
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(text), &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, &job)
	}
	return jobs, rows.Err()
}

// Update atomically records a state revision, event, and any newly bound conversation.
func (s *Store) Update(ctx context.Context, job *control.Job, expected int64, message string) error {
	job.Revision = expected + 1
	job.UpdatedAt = time.Now().UTC()
	encoded, err := json.Marshal(job)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE control_jobs SET state=?,revision=?,updated_at=?,record=? WHERE id=? AND revision=?`, job.State, job.Revision, stamp(job.UpdatedAt), string(encoded), job.ID, expected)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return control.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO control_events(job_id,sequence,state,message,created_at) VALUES(?,?,?,?,?)`, job.ID, job.Revision, job.State, message, stamp(job.UpdatedAt)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM control_events WHERE job_id=? AND sequence<?`, job.ID, job.Revision-255); err != nil {
		return err
	}
	if job.ConversationID != "" && job.ThreadID != "" {
		conversation := control.Conversation{ID: job.ConversationID, Principal: job.Request.Principal, HostID: job.Decision.HostID, ProjectID: job.Decision.ProjectID, ThreadID: job.ThreadID, Profile: job.Decision.Profile, ModelPair: job.Profile.ModelPair, Title: job.Decision.Title}
		data, err := json.Marshal(conversation)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO control_conversations(id,namespace,subject,record) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record WHERE namespace=excluded.namespace AND subject=excluded.subject`, conversation.ID, conversation.Principal.Namespace, conversation.Principal.Subject, string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Events returns a resumable, bounded event page after sequence.
func (s *Store) Events(ctx context.Context, id string, after int64, limit int) ([]control.Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,state,message,created_at FROM control_events WHERE job_id=? AND sequence>? ORDER BY sequence LIMIT ?`, id, after, max(1, min(limit, 128)))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := make([]control.Event, 0)
	for rows.Next() {
		event := control.Event{JobID: id}
		var created string
		if err := rows.Scan(&event.Sequence, &event.State, &event.Message, &created); err != nil {
			return nil, err
		}
		event.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// Conversations returns only the owner's registered Brainslug conversations.
func (s *Store) Conversations(ctx context.Context, owner control.Principal) ([]control.Conversation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM control_conversations WHERE namespace=? AND subject=? LIMIT 128`, owner.Namespace, owner.Subject)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []control.Conversation
	for rows.Next() {
		var text string
		var conversation control.Conversation
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(text), &conversation); err != nil {
			return nil, err
		}
		result = append(result, conversation)
	}
	return result, rows.Err()
}

// Conversation retrieves a binding for use-case authorization.
func (s *Store) Conversation(ctx context.Context, id string) (*control.Conversation, error) {
	var text string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM control_conversations WHERE id=?`, id).Scan(&text); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, control.ErrNotFound
		}
		return nil, err
	}
	var conversation control.Conversation
	if err := json.Unmarshal([]byte(text), &conversation); err != nil {
		return nil, err
	}
	return &conversation, nil
}

// Recover retries only nonmutating routing. Possibly dispatched work remains uncertain.
func (s *Store) Recover(ctx context.Context) error {
	jobs, err := s.Active(ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		switch job.State {
		case control.Routing:
			job.State = control.Accepted
		case control.Dispatching, control.Running, control.AwaitingApproval:
			job.State = control.NeedsAttention
			job.Problem = "Controller restarted; waiting for worker reconciliation"
		default:
			continue
		}
		if err := s.Update(ctx, job, job.Revision, "Restart recovery"); err != nil {
			return err
		}
	}
	return nil
}

// Purge deletes only old terminal operational records, preserving thread bindings.
func (s *Store) Purge(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM control_jobs WHERE state IN ('completed','failed','canceled') AND updated_at<? AND (COALESCE(json_extract(record,'$.attempt_id'),'')='' OR json_extract(record,'$.worker_acknowledged')=1)`, stamp(before))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func stamp(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000000000Z") }

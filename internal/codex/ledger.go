package codex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite" // Register the worker's independent SQLite driver.

	"github.com/wellivea1/alexa-brainslug/internal/control"
)

// Record is the worker's durable evidence of a possibly dispatched attempt.
type Record struct {
	Command      control.Command `json:"command"`
	Stage        string          `json:"stage"`
	ThreadID     string          `json:"thread_id"`
	TurnID       string          `json:"turn_id"`
	Sequence     int64           `json:"sequence"`
	Acknowledged int64           `json:"acknowledged"`
	Root         string          `json:"root"`
}

// Ledger persists attempt deduplication and result spools on the execution host.
type Ledger struct{ db *sql.DB }

// OpenLedger creates an independent worker database, never a network-shared file.
func OpenLedger(ctx context.Context, path string) (*Ledger, error) {
	if path == "" {
		return nil, errors.New("worker ledger path is required")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var wrongTables int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name IN ('interactions','control_jobs')`).Scan(&wrongTables); err != nil || wrongTables != 0 {
		_ = db.Close()
		return nil, errors.New("worker requires its own independent ledger database")
	}
	statements := []string{
		`PRAGMA foreign_keys=ON`, `PRAGMA busy_timeout=5000`, `PRAGMA journal_mode=WAL`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	var table int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name='worker_migrations'`).Scan(&table); err != nil {
		_ = db.Close()
		return nil, err
	}
	if table != 0 {
		var version int
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM worker_migrations`).Scan(&version); err != nil || version > 1 {
			_ = db.Close()
			return nil, errors.New("worker ledger schema is newer than this binary")
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{`CREATE TABLE IF NOT EXISTS worker_migrations(version INTEGER PRIMARY KEY)`, `CREATE TABLE IF NOT EXISTS worker_attempts(attempt_id TEXT PRIMARY KEY,job_id TEXT NOT NULL,payload_hash TEXT NOT NULL,record TEXT NOT NULL,updated_at TEXT NOT NULL)`, `CREATE TABLE IF NOT EXISTS worker_events(attempt_id TEXT NOT NULL REFERENCES worker_attempts(attempt_id) ON DELETE CASCADE,sequence INTEGER NOT NULL,record TEXT NOT NULL,PRIMARY KEY(attempt_id,sequence))`, `INSERT OR IGNORE INTO worker_migrations(version) VALUES(1)`} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			_ = tx.Rollback()
			_ = db.Close()
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Ledger{db: db}, nil
}

// Close releases the worker's local ledger.
func (l *Ledger) Close() error { return l.db.Close() }

// Accept records an attempt before invoking Codex, refusing concurrent execution.
func (l *Ledger) Accept(ctx context.Context, command control.Command, root string) (*Record, bool, error) {
	encoded, err := json.Marshal(command)
	if err != nil {
		return nil, false, err
	}
	hash := sha256.Sum256(encoded)
	key := hex.EncodeToString(hash[:])
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var text, previousHash string
	err = tx.QueryRowContext(ctx, `SELECT record,payload_hash FROM worker_attempts WHERE attempt_id=?`, command.AttemptID).Scan(&text, &previousHash)
	if err == nil {
		if key != previousHash {
			return nil, false, control.ErrConflict
		}
		var record Record
		if err := json.Unmarshal([]byte(text), &record); err != nil {
			return nil, false, err
		}
		return &record, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	var retained int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM worker_attempts`).Scan(&retained); err != nil {
		return nil, false, err
	}
	if retained >= 512 {
		return nil, false, control.ErrQueueFull
	}
	rows, err := tx.QueryContext(ctx, `SELECT record FROM worker_attempts`)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	busy := false
	for rows.Next() {
		var saved string
		var record Record
		if err := rows.Scan(&saved); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		if err := json.Unmarshal([]byte(saved), &record); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		if !control.Terminal(record.Stage) {
			busy = true
		}
	}
	rowErr := rows.Err()
	_ = rows.Close()
	if rowErr != nil {
		return nil, false, rowErr
	}
	if busy {
		return nil, false, control.ErrQueueFull
	}
	record := &Record{Command: command, Root: root, Stage: "accepted"}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO worker_attempts(attempt_id,job_id,payload_hash,record,updated_at) VALUES(?,?,?,?,?)`, command.AttemptID, command.JobID, key, string(data), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return record, true, nil
}

// Get retrieves one locally accepted attempt.
func (l *Ledger) Get(ctx context.Context, id string) (*Record, error) {
	var text string
	if err := l.db.QueryRowContext(ctx, `SELECT record FROM worker_attempts WHERE attempt_id=?`, id).Scan(&text); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, control.ErrNotFound
		}
		return nil, err
	}
	var record Record
	if err := json.Unmarshal([]byte(text), &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// Records lists retained attempt evidence for restart reconciliation.
func (l *Ledger) Records(ctx context.Context) ([]*Record, error) {
	return l.records(ctx, false)
}

// Pending avoids rereading acknowledged terminal prompts on every idle heartbeat.
func (l *Ledger) Pending(ctx context.Context) ([]*Record, error) { return l.records(ctx, true) }

func (l *Ledger) records(ctx context.Context, pending bool) ([]*Record, error) {
	query := `SELECT record FROM worker_attempts`
	if pending {
		query += ` WHERE json_extract(record,'$.sequence')>json_extract(record,'$.acknowledged') OR json_extract(record,'$.stage') NOT IN ('completed','failed','canceled')`
	}
	query += ` ORDER BY updated_at LIMIT 512`
	rows, err := l.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var records []*Record
	for rows.Next() {
		var text string
		var record Record
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(text), &record); err != nil {
			return nil, err
		}
		records = append(records, &record)
	}
	return records, rows.Err()
}

// Append saves evidence and a replayable event in one transaction before sending it.
func (l *Ledger) Append(ctx context.Context, id string, event control.WorkerEvent) (control.WorkerEvent, error) {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return event, err
	}
	defer func() { _ = tx.Rollback() }()
	var text string
	if err := tx.QueryRowContext(ctx, `SELECT record FROM worker_attempts WHERE attempt_id=?`, id).Scan(&text); err != nil {
		return event, err
	}
	var record Record
	if err := json.Unmarshal([]byte(text), &record); err != nil {
		return event, err
	}
	if record.Sequence-record.Acknowledged >= 1024 {
		return event, errors.New("worker event spool reached its bound")
	}
	if record.Sequence-record.Acknowledged >= 1000 && event.State == control.Progress && record.TurnID != "" {
		return event, nil
	}
	record.Sequence++
	event.Sequence = record.Sequence
	event.AttemptID = id
	event.JobID = record.Command.JobID
	if len(event.Text) > 32768 {
		end := 32768
		for end > 0 && !utf8.ValidString(event.Text[:end]) {
			end--
		}
		event.Text = event.Text[:end] + "\n[Result shortened for the controller; complete text remains in the Codex conversation.]"
	}
	if event.ThreadID != "" {
		record.ThreadID = event.ThreadID
	}
	if event.TurnID != "" {
		record.TurnID = event.TurnID
	}
	if event.State == control.Progress && record.TurnID == "" && record.ThreadID != "" {
		record.Stage = "starting"
	} else if event.State != control.Progress {
		record.Stage = event.State
	}
	data, err := json.Marshal(record)
	if err != nil {
		return event, err
	}
	eventData, err := json.Marshal(event)
	if err != nil {
		return event, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE worker_attempts SET record=?,updated_at=? WHERE attempt_id=?`, string(data), time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
		return event, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO worker_events(attempt_id,sequence,record) VALUES(?,?,?)`, id, event.Sequence, string(eventData)); err != nil {
		return event, err
	}
	return event, tx.Commit()
}

// Ack advances only confirmed receipt and drops acknowledged progress spool entries.
func (l *Ledger) Ack(ctx context.Context, id string, sequence int64) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var text string
	if err := tx.QueryRowContext(ctx, `SELECT record FROM worker_attempts WHERE attempt_id=?`, id).Scan(&text); err != nil {
		return err
	}
	var record Record
	if err := json.Unmarshal([]byte(text), &record); err != nil {
		return err
	}
	if sequence < 0 || sequence > record.Sequence {
		return control.ErrInvalid
	}
	record.Acknowledged = max(record.Acknowledged, sequence)
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE worker_attempts SET record=? WHERE attempt_id=?`, string(data), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM worker_events WHERE attempt_id=? AND sequence<=?`, id, record.Acknowledged); err != nil {
		return err
	}
	return tx.Commit()
}

// Purge removes only acknowledged terminal records. Uncertain work and unsent results survive retention.
func (l *Ledger) Purge(ctx context.Context, before time.Time) (int64, error) {
	result, err := l.db.ExecContext(ctx, `DELETE FROM worker_attempts WHERE updated_at<? AND json_extract(record,'$.stage') IN ('completed','failed','canceled') AND json_extract(record,'$.sequence')=json_extract(record,'$.acknowledged')`, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Unacked returns a bounded replay page, optionally after events already sent on this connection.
func (l *Ledger) Unacked(ctx context.Context, id string, after int64) ([]control.WorkerEvent, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT record FROM worker_events WHERE attempt_id=? AND sequence>? ORDER BY sequence LIMIT 64`, id, after)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var events []control.WorkerEvent
	for rows.Next() {
		var text string
		var event control.WorkerEvent
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(text), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

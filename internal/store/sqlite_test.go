package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// openTestStore returns a store backed by a fresh file in a temp directory.
func openTestStore(t *testing.T) *Store {
	t.Helper()

	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "brainslug.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return db
}

// save records an interaction and fails the test on error.
func save(t *testing.T, db *Store, interaction Interaction) {
	t.Helper()
	if _, err := db.SaveInteraction(t.Context(), interaction); err != nil {
		t.Fatalf("SaveInteraction() error = %v", err)
	}
}

func TestSQLiteSaveLoadLatestAndListRecent(t *testing.T) {
	db := openTestStore(t)

	oldTime := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)

	save(t, db, Interaction{
		CreatedAt:   oldTime,
		AlexaUserID: "user-1",
		SessionID:   "session-old",
		Provider:    "openai",
		Model:       "gpt-test",
		Prompt:      "old prompt",
		ShortAnswer: "old answer",
		FullAnswer:  "old full answer",
		Status:      "ok",
	})
	save(t, db, Interaction{
		CreatedAt:   newTime,
		AlexaUserID: "user-1",
		SessionID:   "session-new",
		Provider:    "openai",
		Model:       "gpt-test",
		Prompt:      "new prompt",
		ShortAnswer: "new answer",
		FullAnswer:  "new full answer",
		LatencyMS:   42,
		Status:      "ok",
	})

	latest, err := db.GetLatestResponse(t.Context(), "user-1")
	if err != nil {
		t.Fatalf("GetLatestResponse() error = %v", err)
	}
	if latest.ShortAnswer != "new answer" || latest.LatencyMS != 42 {
		t.Fatalf("latest = %+v", latest)
	}

	recent, err := db.ListRecent(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("len(recent) = %d", len(recent))
	}
	if recent[0].Prompt != "new prompt" {
		t.Fatalf("recent[0].Prompt = %q", recent[0].Prompt)
	}
}

// created_at is a TEXT column sorted as text, so a variable-width fractional
// second sorts wrongly: time.RFC3339Nano drops trailing zeros, and
// "...T12:00:00Z" compares greater than "...T12:00:00.1Z" because 'Z' sorts
// after '.'. A whole-second timestamp would then beat a later one, and
// GetLatestResponse would speak a stale answer.
//
// The sub-second gaps here are what a real burst of requests looks like.
func TestGetLatestResponseOrdersSubSecondTimestamps(t *testing.T) {
	db := openTestStore(t)

	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	steps := []struct {
		offset time.Duration
		answer string
	}{
		{0, "first"},                       // formats with no fraction at all
		{100 * time.Millisecond, "second"}, // one fractional digit
		{250 * time.Millisecond, "third"},
		{900 * time.Millisecond, "newest"},
	}

	for _, step := range steps {
		save(t, db, Interaction{
			CreatedAt:   base.Add(step.offset),
			AlexaUserID: "user-1",
			Provider:    "openai",
			Model:       "gpt-test",
			Prompt:      step.answer,
			ShortAnswer: step.answer,
			Status:      "ok",
		})
	}

	latest, err := db.GetLatestResponse(t.Context(), "user-1")
	if err != nil {
		t.Fatalf("GetLatestResponse() error = %v", err)
	}
	if latest.ShortAnswer != "newest" {
		t.Errorf("latest = %q, want %q", latest.ShortAnswer, "newest")
	}

	recent, err := db.ListRecent(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	want := []string{"newest", "third", "second", "first"}
	for i, expected := range want {
		if recent[i].ShortAnswer != expected {
			t.Errorf("recent[%d] = %q, want %q", i, recent[i].ShortAnswer, expected)
		}
	}
}

// Timestamps must survive the round trip through TEXT storage.
func TestTimestampRoundTrip(t *testing.T) {
	db := openTestStore(t)

	created := time.Date(2026, 8, 4, 12, 34, 56, 123456789, time.UTC)
	save(t, db, Interaction{
		CreatedAt:   created,
		AlexaUserID: "user-1",
		ShortAnswer: "answer",
		Status:      "ok",
	})

	latest, err := db.GetLatestResponse(t.Context(), "user-1")
	if err != nil {
		t.Fatalf("GetLatestResponse() error = %v", err)
	}
	if !latest.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", latest.CreatedAt, created)
	}
}

// One household member must not be able to replay another's answer.
func TestGetLatestResponseIsScopedByUser(t *testing.T) {
	db := openTestStore(t)

	save(t, db, Interaction{AlexaUserID: "user-1", ShortAnswer: "for user one", Status: "ok"})
	save(t, db, Interaction{AlexaUserID: "user-2", ShortAnswer: "for user two", Status: "ok"})

	latest, err := db.GetLatestResponse(t.Context(), "user-1")
	if err != nil {
		t.Fatalf("GetLatestResponse() error = %v", err)
	}
	if latest.ShortAnswer != "for user one" {
		t.Errorf("latest = %q", latest.ShortAnswer)
	}
}

// A failed interaction is recorded but must never be replayed as an answer.
func TestGetLatestResponseSkipsErrors(t *testing.T) {
	db := openTestStore(t)

	save(t, db, Interaction{
		AlexaUserID: "user-1",
		CreatedAt:   time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		ShortAnswer: "a good answer",
		Status:      "ok",
	})
	save(t, db, Interaction{
		AlexaUserID:  "user-1",
		CreatedAt:    time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC),
		Status:       "error",
		ErrorMessage: "provider exploded",
	})

	latest, err := db.GetLatestResponse(t.Context(), "user-1")
	if err != nil {
		t.Fatalf("GetLatestResponse() error = %v", err)
	}
	if latest.ShortAnswer != "a good answer" {
		t.Errorf("latest = %q", latest.ShortAnswer)
	}
}

func TestGetLatestResponseNotFound(t *testing.T) {
	db := openTestStore(t)

	if _, err := db.GetLatestResponse(t.Context(), "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestListRecentClampsLimit(t *testing.T) {
	db := openTestStore(t)
	for i := range 5 {
		save(t, db, Interaction{
			AlexaUserID: "user-1",
			CreatedAt:   time.Date(2026, 1, 1, 10, i, 0, 0, time.UTC),
			ShortAnswer: "answer",
			Status:      "ok",
		})
	}

	for _, limit := range []int{0, -1, maxListLimit + 1} {
		got, err := db.ListRecent(t.Context(), limit)
		if err != nil {
			t.Fatalf("ListRecent(%d) error = %v", limit, err)
		}
		if len(got) != 5 {
			t.Errorf("ListRecent(%d) returned %d rows", limit, len(got))
		}
	}
}

// Opening an existing database must be a no-op for already-applied
// migrations, so a restart does not re-run them.
func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brainslug.db")

	first, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	save(t, first, Interaction{AlexaUserID: "user-1", ShortAnswer: "answer", Status: "ok"})
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	second, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer func() {
		if err := second.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	latest, err := second.GetLatestResponse(t.Context(), "user-1")
	if err != nil {
		t.Fatalf("GetLatestResponse() error = %v", err)
	}
	if latest.ShortAnswer != "answer" {
		t.Errorf("data did not survive reopening: %+v", latest)
	}

	var applied int
	if err := second.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if applied != len(migrations) {
		t.Errorf("applied %d migrations, want %d", applied, len(migrations))
	}
}

// Migration 2 has to repair rows written by the old variable-width format,
// otherwise an existing database keeps sorting incorrectly forever.
func TestMigrationPadsLegacyTimestamps(t *testing.T) {
	db := openTestStore(t)

	// Write rows exactly as the previous release would have: RFC3339Nano,
	// with the trailing zeros dropped.
	legacy := []struct {
		createdAt string
		answer    string
	}{
		{"2026-08-04T12:00:00Z", "no fraction"},
		{"2026-08-04T12:00:00.1Z", "one digit"},
		{"2026-08-04T12:00:00.25Z", "two digits"},
	}
	for _, row := range legacy {
		if _, err := db.db.ExecContext(t.Context(),
			`INSERT INTO interactions (created_at, alexa_user_id, session_id, provider, model,
				prompt, short_answer, full_answer, latency_ms, status, error_message)
			 VALUES (?, 'user-1', '', 'openai', 'gpt-test', '', ?, '', 0, 'ok', '')`,
			row.createdAt, row.answer,
		); err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}

	// Re-run the migration set against a database that already has version 2
	// recorded, so force it by clearing the record first.
	if _, err := db.db.ExecContext(t.Context(), `DELETE FROM schema_migrations WHERE version = 2`); err != nil {
		t.Fatalf("reset migration record: %v", err)
	}
	if err := migrate(t.Context(), db.db); err != nil {
		t.Fatalf("migrate() error = %v", err)
	}

	rows, err := db.ListRecent(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d", len(rows))
	}
	if rows[0].ShortAnswer != "two digits" {
		t.Errorf("newest = %q, want %q", rows[0].ShortAnswer, "two digits")
	}
	if rows[2].ShortAnswer != "no fraction" {
		t.Errorf("oldest = %q, want %q", rows[2].ShortAnswer, "no fraction")
	}

	var widths []int
	scan, err := db.db.QueryContext(t.Context(), `SELECT length(created_at) FROM interactions`)
	if err != nil {
		t.Fatalf("query widths: %v", err)
	}
	defer func() { _ = scan.Close() }()
	for scan.Next() {
		var width int
		if err := scan.Scan(&width); err != nil {
			t.Fatalf("scan width: %v", err)
		}
		widths = append(widths, width)
	}
	if err := scan.Err(); err != nil {
		t.Fatalf("iterate widths: %v", err)
	}
	for i, width := range widths {
		if width != 30 {
			t.Errorf("row %d has created_at width %d, want 30", i, width)
		}
	}
}

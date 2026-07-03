package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteSaveLoadLatestAndListRecent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "brainslug.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	oldTime := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)

	if _, err := db.SaveInteraction(ctx, Interaction{
		CreatedAt:   oldTime,
		AlexaUserID: "user-1",
		SessionID:   "session-old",
		Provider:    "openai",
		Model:       "gpt-test",
		Prompt:      "old prompt",
		ShortAnswer: "old answer",
		FullAnswer:  "old full answer",
		Status:      "ok",
	}); err != nil {
		t.Fatalf("SaveInteraction(old) error = %v", err)
	}
	if _, err := db.SaveInteraction(ctx, Interaction{
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
	}); err != nil {
		t.Fatalf("SaveInteraction(new) error = %v", err)
	}

	latest, err := db.GetLatestResponse(ctx, "user-1")
	if err != nil {
		t.Fatalf("GetLatestResponse() error = %v", err)
	}
	if latest.ShortAnswer != "new answer" || latest.LatencyMS != 42 {
		t.Fatalf("latest = %+v", latest)
	}

	recent, err := db.ListRecent(ctx, 10)
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

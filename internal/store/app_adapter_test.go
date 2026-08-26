package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/app"
)

func TestAppRepositoryRoundTrip(t *testing.T) {
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "brainslug.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	repository := AsAppRepository(store)
	principal := app.Principal{Namespace: "alexa", Subject: "user-1"}
	_, err = repository.SaveInteraction(t.Context(), app.Interaction{
		Principal:      principal,
		ConversationID: "session-1",
		Provider:       "fake",
		Model:          "model",
		Prompt:         "question",
		ShortAnswer:    "short",
		FullAnswer:     "full",
		Latency:        25 * time.Millisecond,
		Status:         app.StatusOK,
	})
	if err != nil {
		t.Fatalf("SaveInteraction() error = %v", err)
	}

	interaction, err := repository.GetLatestResponse(t.Context(), principal)
	if err != nil {
		t.Fatalf("GetLatestResponse() error = %v", err)
	}
	if interaction.Principal != principal || interaction.ConversationID != "session-1" {
		t.Errorf("interaction scope = %+v", interaction)
	}
	if interaction.ShortAnswer != "short" || interaction.FullAnswer != "full" {
		t.Errorf("interaction = %+v", interaction)
	}
	if interaction.Latency != 25*time.Millisecond {
		t.Errorf("latency = %v", interaction.Latency)
	}
}

func TestAppRepositoryMapsNotFound(t *testing.T) {
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "brainslug.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	repository := AsAppRepository(store)
	_, err = repository.GetLatestResponse(t.Context(), app.Principal{Namespace: "alexa", Subject: "missing"})
	if !errors.Is(err, app.ErrRepositoryNotFound) {
		t.Errorf("GetLatestResponse() error = %v", err)
	}
}

func TestPrincipalKey(t *testing.T) {
	tests := []struct {
		name      string
		principal app.Principal
		want      string
	}{
		{"legacy Alexa key", app.Principal{Namespace: "alexa", Subject: "user-1"}, "user-1"},
		{"future namespace", app.Principal{Namespace: "mcp", Subject: "user-1"}, "mcp:user-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := principalKey(tt.principal); got != tt.want {
				t.Errorf("principalKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

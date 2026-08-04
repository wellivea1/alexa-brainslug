package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/store"
)

type fakeStore struct {
	interactions []store.Interaction
	err          error
	gotLimit     int
}

func (s *fakeStore) ListRecent(_ context.Context, limit int) ([]store.Interaction, error) {
	s.gotLimit = limit
	if s.err != nil {
		return nil, s.err
	}
	return s.interactions, nil
}

func sampleInteraction() store.Interaction {
	return store.Interaction{
		ID:          1,
		CreatedAt:   time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
		AlexaUserID: "user-1",
		Provider:    "openai",
		Model:       "gpt-test",
		Prompt:      "what is go",
		ShortAnswer: "A programming language.",
		FullAnswer:  "A statically typed programming language.",
		LatencyMS:   1234,
		Status:      "ok",
	}
}

func TestHandleIndexRendersInteractions(t *testing.T) {
	history := &fakeStore{interactions: []store.Interaction{sampleInteraction()}}
	dashboard := NewDashboard(history)

	rec := httptest.NewRecorder()
	dashboard.HandleIndex(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"what is go", "A programming language.", "gpt-test", "1234 ms"} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q", want)
		}
	}
}

// Everything on this page is attacker-influenced: it is whatever someone said
// to Alexa, plus whatever a model replied. html/template escapes it, and this
// test exists so a future switch to text/template or raw writing is caught.
func TestHandleIndexEscapesStoredText(t *testing.T) {
	hostile := sampleInteraction()
	hostile.Prompt = `<script>alert('xss')</script>`
	hostile.ShortAnswer = `<img src=x onerror="alert(1)">`
	history := &fakeStore{interactions: []store.Interaction{hostile}}
	dashboard := NewDashboard(history)

	rec := httptest.NewRecorder()
	dashboard.HandleIndex(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

	body := rec.Body.String()

	// The check is that no tag is opened. The literal text "onerror=" still
	// appears, but inside escaped angle brackets it is inert prose, so
	// asserting on it would fail for the wrong reason.
	if strings.Contains(body, "<script") {
		t.Error("a script tag was rendered unescaped")
	}
	if strings.Contains(body, "<img") {
		t.Error("an img tag was rendered unescaped")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected the prompt to appear escaped")
	}
	if !strings.Contains(body, "&lt;img") {
		t.Error("expected the answer to appear escaped")
	}
}

func TestHandleIndexReportsStoreFailure(t *testing.T) {
	dashboard := NewDashboard(&fakeStore{err: errors.New("disk gone")})

	rec := httptest.NewRecorder()
	dashboard.HandleIndex(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "disk gone") {
		t.Error("the underlying error should not be returned to the client")
	}
}

func TestHandleHistoryReturnsJSON(t *testing.T) {
	history := &fakeStore{interactions: []store.Interaction{sampleInteraction()}}
	dashboard := NewDashboard(history)

	rec := httptest.NewRecorder()
	dashboard.HandleHistory(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/history", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}

	var payload struct {
		Interactions []store.Interaction `json:"interactions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(payload.Interactions) != 1 || payload.Interactions[0].Prompt != "what is go" {
		t.Errorf("payload = %+v", payload.Interactions)
	}
}

func TestHandleHistoryLimitParsing(t *testing.T) {
	tests := []struct {
		query string
		want  int
	}{
		{"", defaultLimit},
		{"?limit=5", 5},
		{"?limit=notanumber", defaultLimit},
		{"?limit=-3", -3}, // passed through; the store clamps it
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			history := &fakeStore{}
			dashboard := NewDashboard(history)

			rec := httptest.NewRecorder()
			dashboard.HandleHistory(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/history"+tt.query, http.NoBody))

			if history.gotLimit != tt.want {
				t.Errorf("limit = %d, want %d", history.gotLimit, tt.want)
			}
		})
	}
}

func TestHandleIndexWithNoInteractions(t *testing.T) {
	dashboard := NewDashboard(&fakeStore{})

	rec := httptest.NewRecorder()
	dashboard.HandleIndex(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "No interactions") {
		t.Error("expected the empty state")
	}
}

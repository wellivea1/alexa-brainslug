package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/inference"
	"github.com/wellivea1/alexa-brainslug/internal/store"
)

type fakeStore struct {
	interactions []store.Interaction
	err          error
	gotLimit     int
}

type fakeInferenceController struct {
	snapshot inference.Snapshot
	update   inference.Update
	err      error
}

func (f *fakeInferenceController) Snapshot() inference.Snapshot { return f.snapshot }
func (f *fakeInferenceController) Update(update inference.Update) error {
	f.update = update
	return f.err
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
	dashboard.HandleIndex(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?section=activity", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"what is go", "A programming language.", "gpt-test", "1.2 s", "Overview", "Inference"} {
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
	dashboard.HandleIndex(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?section=activity", http.NoBody))

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
	if !strings.Contains(rec.Body.String(), "No stored interactions") {
		t.Error("expected the empty state")
	}
}

func TestHandleIndexRendersInferenceStateWithoutSecrets(t *testing.T) {
	controller := &fakeInferenceController{snapshot: inference.Snapshot{
		ActiveProvider: "openai",
		OpenAI: inference.ProviderState{
			ID: "openai", Name: "OpenAI / compatible", Active: true,
			Model: "gpt-5.6-terra", EffectiveBaseURL: "https://api.openai.com/v1",
			EndpointKind: "Official OpenAI Responses API", CredentialConfigured: true,
			CredentialSource: "Environment", ReasoningEffort: "low", TextVerbosity: "low",
			MaxOutputTokens: 25000, TimeoutSeconds: 6,
		},
	}}
	dashboard := NewDashboard(&fakeStore{}, Options{
		Inference: controller, CSRFToken: "test-token",
		AlexaVerifyRequests: true, HistoryEnabled: true,
		PublicBaseURLConfigured: true, SkillIDConfigured: true,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?section=inference", http.NoBody)
	dashboard.HandleIndex(rec, req)

	body := rec.Body.String()
	for _, want := range []string{"gpt-5.6-terra", "Official OpenAI Responses API", "Reasoning effort", "Credential ready", "Ready for Alexa"} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q", want)
		}
	}
	if strings.Contains(body, "secret-value") {
		t.Fatal("dashboard rendered a provider credential")
	}
	if strings.Contains(body, "Provider contract") || strings.Contains(body, "Simple by design") {
		t.Fatal("dashboard rendered documentation prose")
	}
}

func TestHandleIndexUsesOneTabbedProviderPanel(t *testing.T) {
	controller := &fakeInferenceController{snapshot: inference.Snapshot{
		ActiveProvider: "openai",
		OpenAI:         inference.ProviderState{ID: "openai", Name: "OpenAI / compatible", Active: true, Model: "gpt-test"},
		Anthropic:      inference.ProviderState{ID: "anthropic", Name: "Anthropic", Model: "claude-test"},
	}}
	dashboard := NewDashboard(&fakeStore{}, Options{Inference: controller, CSRFToken: "test-token"})
	rec := httptest.NewRecorder()
	dashboard.HandleIndex(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?section=inference&provider=anthropic", http.NoBody))

	body := rec.Body.String()
	if !strings.Contains(body, `class="provider-manager"`) || !strings.Contains(body, `id="anthropic-provider-title"`) {
		t.Fatal("Anthropic provider tab was not rendered in the integrated manager")
	}
	if strings.Contains(body, `id="openai-provider-title"`) {
		t.Fatal("multiple provider settings panels were rendered at once")
	}
}

func TestHandleProviderUpdateUsesCSRFAndController(t *testing.T) {
	controller := &fakeInferenceController{}
	dashboard := NewDashboard(&fakeStore{}, Options{Inference: controller, CSRFToken: "test-token"})
	form := url.Values{
		"csrf_token": {"test-token"}, "provider": {"openai"}, "model": {"gpt-5.6-luna"},
		"base_url": {""}, "max_output_tokens": {"4096"}, "timeout_seconds": {"4"},
		"reasoning_effort": {"none"}, "text_verbosity": {"low"},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/settings/provider", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	dashboard.HandleProviderUpdate(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/?section=inference&provider=openai&saved=1" {
		t.Fatalf("redirect = %q", got)
	}
	if controller.update.Model != "gpt-5.6-luna" || controller.update.ReasoningEffort != "none" || controller.update.TimeoutSeconds != 4 {
		t.Fatalf("update = %#v", controller.update)
	}

	bad := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/settings/provider", strings.NewReader("provider=openai"))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badRec := httptest.NewRecorder()
	dashboard.HandleProviderUpdate(badRec, bad)
	if badRec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", badRec.Code)
	}
}

func TestHandleStylesServesEmbeddedCSS(t *testing.T) {
	dashboard := NewDashboard(&fakeStore{})
	rec := httptest.NewRecorder()
	dashboard.HandleStyles(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/dashboard.css", http.NoBody))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), ".app-shell") {
		t.Fatalf("stylesheet response = %d %q", rec.Code, rec.Body.String())
	}
	for _, disallowed := range []string{"linear-gradient", "radial-gradient", "box-shadow:", "backdrop-filter", ".panel {"} {
		if strings.Contains(rec.Body.String(), disallowed) {
			t.Errorf("stylesheet still contains decorative card effect %q", disallowed)
		}
	}
}

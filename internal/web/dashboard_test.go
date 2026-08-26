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
	"github.com/wellivea1/alexa-brainslug/internal/openauth"
	"github.com/wellivea1/alexa-brainslug/internal/store"
)

type fakeStore struct {
	interactions []store.Interaction
	err          error
	gotLimit     int
}

type fakeInferenceController struct {
	snapshot    inference.Snapshot
	update      inference.Update
	authMode    string
	keyProvider string
	keyValue    string
	clearedKey  string
	err         error
}

func (f *fakeInferenceController) Snapshot() inference.Snapshot { return f.snapshot }
func (f *fakeInferenceController) Update(update inference.Update) error {
	f.update = update
	return f.err
}

func (f *fakeInferenceController) SetOpenAIAuthMode(mode string) error {
	f.authMode = mode
	return f.err
}

func (f *fakeInferenceController) SetAPIKey(provider, key string) error {
	f.keyProvider = provider
	f.keyValue = key
	return f.err
}

func (f *fakeInferenceController) ClearAPIKey(provider string) error {
	f.clearedKey = provider
	return f.err
}

type fakeOpenAIAuthController struct {
	snapshot  openauth.Snapshot
	returnURL string
	flow      openauth.DeviceFlow
	polledID  string
	cleared   bool
}

func (f *fakeOpenAIAuthController) Snapshot() openauth.Snapshot { return f.snapshot }
func (f *fakeOpenAIAuthController) StartBrowser(returnURL string) (string, error) {
	f.returnURL = returnURL
	return "https://auth.openai.com/oauth/authorize?test=1", nil
}

func (f *fakeOpenAIAuthController) StartDevice(context.Context) (openauth.DeviceFlow, error) {
	return f.flow, nil
}

func (f *fakeOpenAIAuthController) Device(id string) (openauth.DeviceFlow, bool) {
	return f.flow, id != "" && id == f.flow.ID
}

func (f *fakeOpenAIAuthController) PollDevice(_ context.Context, id string) (openauth.PollResult, error) {
	f.polledID = id
	return openauth.PollConnected, nil
}
func (f *fakeOpenAIAuthController) Clear() error { f.cleared = true; return nil }

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
			ID: "openai", Name: "OpenAI", Active: true,
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
		ActiveProvider: "openai-compatible",
		OpenAI:         inference.ProviderState{ID: "openai", Name: "OpenAI", Model: "gpt-test"},
		Compatible:     inference.ProviderState{ID: "openai-compatible", Name: "OpenAI-compatible", Active: true, Model: "local-model", BaseURL: "http://127.0.0.1:11434/v1"},
		Anthropic:      inference.ProviderState{ID: "anthropic", Name: "Anthropic", Model: "claude-test"},
	}}
	dashboard := NewDashboard(&fakeStore{}, Options{Inference: controller, CSRFToken: "test-token"})
	rec := httptest.NewRecorder()
	dashboard.HandleIndex(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?section=inference&provider=openai-compatible", http.NoBody))

	body := rec.Body.String()
	if !strings.Contains(body, `class="provider-manager"`) || !strings.Contains(body, `id="compatible-provider-title"`) {
		t.Fatal("compatible provider tab was not rendered in the integrated manager")
	}
	if strings.Contains(body, `id="openai-provider-title"`) || strings.Contains(body, `id="anthropic-provider-title"`) {
		t.Fatal("multiple provider settings panels were rendered at once")
	}
	for _, tab := range []string{`provider=openai"`, `provider=openai-compatible"`, `provider=anthropic"`} {
		if !strings.Contains(body, tab) {
			t.Errorf("provider manager is missing tab %q", tab)
		}
	}
	if !strings.Contains(body, "OAI Compat.") {
		t.Error("compatible provider tab does not use the compact OpenAI label")
	}
}

func TestHandleProviderUpdateUsesCSRFAndController(t *testing.T) {
	controller := &fakeInferenceController{}
	dashboard := NewDashboard(&fakeStore{}, Options{Inference: controller, CSRFToken: "test-token"})
	form := url.Values{
		"csrf_token": {"test-token"}, "provider": {"openai"}, "model": {"gpt-5.6-luna"},
		"base_url": {""}, "max_output_tokens": {"4096"}, "timeout_seconds": {"4"},
		"auth_mode": {"api_key"}, "reasoning_effort": {"none"}, "text_verbosity": {"low"},
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
	if controller.update.Model != "gpt-5.6-luna" || controller.update.AuthMode != "api_key" || controller.update.ReasoningEffort != "none" || controller.update.TimeoutSeconds != 4 {
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

func TestProviderAPIKeyControlsAreMaskedAndProtected(t *testing.T) {
	controller := &fakeInferenceController{snapshot: inference.Snapshot{
		ActiveProvider: "openai",
		OpenAI:         inference.ProviderState{ID: "openai", Name: "OpenAI", Model: "gpt-test"},
		Anthropic: inference.ProviderState{
			ID: "anthropic", Name: "Anthropic", Model: "claude-test", ManagedAPIKey: true,
			CredentialConfigured: true, CredentialSource: "Saved locally",
		},
	}}
	dashboard := NewDashboard(&fakeStore{}, Options{Inference: controller, CSRFToken: "test-token"})
	rec := httptest.NewRecorder()
	dashboard.HandleIndex(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?section=inference&provider=anthropic", http.NoBody))
	body := rec.Body.String()
	for _, want := range []string{`type="password"`, `action="/settings/provider"`, `formaction="/auth/provider/api-key"`, "Remove saved key", "Saved locally"} {
		if !strings.Contains(body, want) {
			t.Errorf("Anthropic credential UI is missing %q", want)
		}
	}
	if strings.Contains(body, "sk-ant-secret") {
		t.Fatal("dashboard rendered an API key")
	}

	form := url.Values{"csrf_token": {"test-token"}, "provider": {"anthropic"}, "api_key": {"sk-ant-secret"}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/provider/api-key", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://127.0.0.1:8081")
	req.Host = "127.0.0.1:8081"
	rec = httptest.NewRecorder()
	dashboard.HandleAPIKeySave(rec, req)
	if rec.Code != http.StatusSeeOther || controller.keyProvider != "anthropic" || controller.keyValue != "sk-ant-secret" {
		t.Fatalf("save key = status %d, provider %q", rec.Code, controller.keyProvider)
	}
	if strings.Contains(rec.Body.String(), "sk-ant-secret") || strings.Contains(rec.Header().Get("Location"), "sk-ant-secret") {
		t.Fatal("save response exposed the API key")
	}

	remove := url.Values{"csrf_token": {"test-token"}, "provider": {"anthropic"}}
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/provider/api-key/remove", strings.NewReader(remove.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	dashboard.HandleAPIKeyRemove(rec, req)
	if rec.Code != http.StatusSeeOther || controller.clearedKey != "anthropic" {
		t.Fatalf("remove key = status %d, provider %q", rec.Code, controller.clearedKey)
	}

	bad := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/provider/api-key", strings.NewReader("provider=anthropic&api_key=secret"))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badRec := httptest.NewRecorder()
	dashboard.HandleAPIKeySave(badRec, bad)
	if badRec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", badRec.Code)
	}
}

func TestOpenAIAccountControlsRenderAndMutationsRequireCSRF(t *testing.T) {
	controller := &fakeInferenceController{snapshot: inference.Snapshot{
		ActiveProvider: "openai",
		OpenAI:         inference.ProviderState{ID: "openai", Name: "OpenAI / compatible", Active: true, Model: "gpt-test", AuthMode: "chatgpt", ChatGPTConnected: true, CredentialConfigured: true},
	}}
	authentication := &fakeOpenAIAuthController{snapshot: openauth.Snapshot{Connected: true}}
	dashboard := NewDashboard(&fakeStore{}, Options{Inference: controller, OpenAIAuth: authentication, CSRFToken: "test-token"})
	rec := httptest.NewRecorder()
	dashboard.HandleIndex(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?section=inference&provider=openai", http.NoBody))
	body := rec.Body.String()
	for _, want := range []string{"ChatGPT account", "Reconnect", "Use code", "Disconnect", `value="chatgpt" checked`} {
		if !strings.Contains(body, want) {
			t.Errorf("account UI is missing %q", want)
		}
	}

	form := url.Values{"csrf_token": {"test-token"}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/openai/browser", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://127.0.0.1:8081")
	req.Host = "127.0.0.1:8081"
	rec = httptest.NewRecorder()
	dashboard.HandleOpenAIBrowserStart(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "https://auth.openai.com/") {
		t.Fatalf("browser login response = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if authentication.returnURL != "http://127.0.0.1:8081/?section=inference&provider=openai" {
		t.Fatalf("return URL = %q", authentication.returnURL)
	}

	bad := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/openai/browser", http.NoBody)
	badRec := httptest.NewRecorder()
	dashboard.HandleOpenAIBrowserStart(badRec, bad)
	if badRec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", badRec.Code)
	}
}

func TestOpenAIDeviceDialogAndDisconnect(t *testing.T) {
	controller := &fakeInferenceController{}
	authentication := &fakeOpenAIAuthController{
		snapshot: openauth.Snapshot{Connected: true},
		flow:     openauth.DeviceFlow{ID: "flow-1", UserCode: "ABCD-EFGH", VerificationURL: "https://auth.openai.com/codex/device", PollIntervalSeconds: 5},
	}
	dashboard := NewDashboard(&fakeStore{}, Options{Inference: controller, OpenAIAuth: authentication, CSRFToken: "test-token"})
	rec := httptest.NewRecorder()
	dashboard.HandleIndex(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?section=inference&provider=openai&device=flow-1", http.NoBody))
	if !strings.Contains(rec.Body.String(), "ABCD-EFGH") || !strings.Contains(rec.Body.String(), `role="dialog"`) {
		t.Fatal("device login dialog was not rendered")
	}

	form := url.Values{"csrf_token": {"test-token"}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/openai/disconnect", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	dashboard.HandleOpenAIDisconnect(rec, req)
	if rec.Code != http.StatusSeeOther || !authentication.cleared || controller.authMode != "api_key" {
		t.Fatalf("disconnect = status %d, cleared %v, mode %q", rec.Code, authentication.cleared, controller.authMode)
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
	if strings.Contains(rec.Body.String(), ".metric:first-child { padding-left: 0") || strings.Contains(rec.Body.String(), ".metric:nth-child(odd) { padding-left: 0") {
		t.Error("overview metric alignment still removes left padding")
	}
}

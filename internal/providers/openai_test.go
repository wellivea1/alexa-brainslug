package providers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/config"
)

// newOpenAITestServer returns a provider pointed at a stub that replies with
// status and body, plus a pointer to the decoded request for assertions.
func newOpenAITestServer(t *testing.T, status int, body string) (*OpenAI, *openAIRequest) {
	t.Helper()

	captured := &openAIRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if err := json.Unmarshal(raw, captured); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key-value" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.URL.Path; got != "/v1"+openAIResponsesPath {
			t.Errorf("path = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	provider := NewOpenAI(config.OpenAIConfig{
		APIKey:          "test-key-value",
		BaseURL:         server.URL + "/v1",
		Model:           "gpt-test",
		MaxOutputTokens: 256,
		Timeout:         5 * time.Second,
		ReasoningEffort: "low",
		TextVerbosity:   "low",
	}, server.Client())

	return provider, captured
}

func TestOpenAIAskUsesOutputText(t *testing.T) {
	provider, captured := newOpenAITestServer(t, http.StatusOK, `{
		"model": "gpt-test-2026",
		"output_text": "Go is a compiled language."
	}`)

	resp, err := provider.Ask(t.Context(), Request{
		UserID: "amzn1.ask.account.example",
		Prompt: "what is go",
	})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}

	if resp.FullAnswer != "Go is a compiled language." {
		t.Errorf("FullAnswer = %q", resp.FullAnswer)
	}
	if resp.ShortAnswer != "Go is a compiled language." {
		t.Errorf("ShortAnswer = %q", resp.ShortAnswer)
	}
	// The provider reports the resolved model, which can differ from the
	// requested alias. History should record what actually answered.
	if resp.Model != "gpt-test-2026" {
		t.Errorf("Model = %q, want the model the provider reported", resp.Model)
	}
	if resp.ProviderName != openAIName {
		t.Errorf("ProviderName = %q", resp.ProviderName)
	}
	if resp.Latency <= 0 {
		t.Error("Latency should be measured")
	}
	if captured.Model != "gpt-test" {
		t.Errorf("requested model = %q", captured.Model)
	}
	if captured.MaxOutputTokens != 256 {
		t.Errorf("max_output_tokens = %d", captured.MaxOutputTokens)
	}
	if captured.Reasoning == nil || captured.Reasoning.Effort != "low" {
		t.Errorf("reasoning = %#v", captured.Reasoning)
	}
	if captured.Text == nil || captured.Text.Verbosity != "low" {
		t.Errorf("text = %#v", captured.Text)
	}
}

func TestOpenAIAskOmitsOptionalResponseControlsForCompatibleProviders(t *testing.T) {
	provider, captured := newOpenAITestServer(t, http.StatusOK, `{"output_text": "answer"}`)
	provider.reasoningEffort = ""
	provider.textVerbosity = ""

	if _, err := provider.Ask(t.Context(), Request{Prompt: "q"}); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if captured.Reasoning != nil || captured.Text != nil {
		t.Fatalf("optional controls should be omitted, got reasoning=%#v text=%#v", captured.Reasoning, captured.Text)
	}
}

// The Alexa user ID must never reach a provider in the clear.
func TestOpenAIAskHashesTheUserID(t *testing.T) {
	provider, captured := newOpenAITestServer(t, http.StatusOK, `{"output_text": "answer"}`)

	const userID = "amzn1.ask.account.example"
	if _, err := provider.Ask(t.Context(), Request{UserID: userID, Prompt: "q"}); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}

	if captured.SafetyID == "" {
		t.Fatal("expected an end-user identifier to be sent")
	}
	if strings.Contains(captured.SafetyID, userID) {
		t.Errorf("the raw Alexa user ID was sent: %q", captured.SafetyID)
	}
	if len(captured.SafetyID) != 64 {
		t.Errorf("expected a SHA-256 hex digest, got %q", captured.SafetyID)
	}
}

// output_text is absent on some response shapes, so the nested output array
// is the fallback path.
func TestOpenAIAskFallsBackToNestedOutput(t *testing.T) {
	provider, _ := newOpenAITestServer(t, http.StatusOK, `{
		"model": "gpt-test",
		"output": [{
			"type": "message",
			"content": [
				{"type": "output_text", "text": "First part."},
				{"type": "output_text", "text": "Second part."}
			]
		}]
	}`)

	resp, err := provider.Ask(t.Context(), Request{Prompt: "q"})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if !strings.Contains(resp.FullAnswer, "First part.") || !strings.Contains(resp.FullAnswer, "Second part.") {
		t.Errorf("FullAnswer = %q", resp.FullAnswer)
	}
}

// A refusal is an answer the user should hear, not an error to swallow.
func TestOpenAIAskSurfacesRefusals(t *testing.T) {
	provider, _ := newOpenAITestServer(t, http.StatusOK, `{
		"output": [{
			"type": "message",
			"content": [{"type": "refusal", "refusal": "I cannot help with that."}]
		}]
	}`)

	resp, err := provider.Ask(t.Context(), Request{Prompt: "q"})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if resp.FullAnswer != "I cannot help with that." {
		t.Errorf("FullAnswer = %q", resp.FullAnswer)
	}
}

// A response carrying only reasoning output is the reasoning-budget
// exhaustion case, and must be diagnosable rather than a generic failure.
func TestOpenAIAskReportsEmptyOutput(t *testing.T) {
	provider, _ := newOpenAITestServer(t, http.StatusOK, `{
		"model": "gpt-test",
		"output": [{"type": "reasoning", "content": []}]
	}`)

	_, err := provider.Ask(t.Context(), Request{Prompt: "q"})
	if !errors.Is(err, ErrEmptyResponse) {
		t.Fatalf("error = %v, want ErrEmptyResponse", err)
	}
}

func TestOpenAIAskReturnsAPIError(t *testing.T) {
	provider, _ := newOpenAITestServer(t, http.StatusTooManyRequests, `{
		"error": {"message": "Rate limit reached", "type": "rate_limit_error"}
	}`)

	_, err := provider.Ask(t.Context(), Request{Prompt: "q"})

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusTooManyRequests {
		t.Errorf("Status = %d", apiErr.Status)
	}
	if !apiErr.Retryable() {
		t.Error("429 should be retryable")
	}
	if !strings.Contains(apiErr.Message, "Rate limit reached") {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestOpenAIAskRedactsAPIKeyInError(t *testing.T) {
	provider, _ := newOpenAITestServer(t, http.StatusUnauthorized, `{
		"error": {"message": "Invalid key test-key-value supplied"}
	}`)

	_, err := provider.Ask(t.Context(), Request{Prompt: "q"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "test-key-value") {
		t.Fatalf("the API key leaked into the error: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("expected a redaction marker, got %v", err)
	}
}

func TestOpenAIAskRejectsMissingConfiguration(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.OpenAIConfig
		req  Request
		want error
	}{
		{
			"no API key",
			config.OpenAIConfig{Model: "gpt-test", Timeout: time.Second},
			Request{Prompt: "q"},
			ErrNotConfigured,
		},
		{
			"no model",
			config.OpenAIConfig{APIKey: "key-value", Timeout: time.Second},
			Request{Prompt: "q"},
			ErrNotConfigured,
		},
		{
			"empty prompt",
			config.OpenAIConfig{APIKey: "key-value", Model: "gpt-test", Timeout: time.Second},
			Request{Prompt: "   "},
			ErrEmptyPrompt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewOpenAI(tt.cfg, http.DefaultClient)
			if _, err := provider.Ask(t.Context(), tt.req); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

// A long answer is shortened for speech, but the full text is preserved for
// storage.
func TestOpenAIAskShortensLongAnswersForSpeech(t *testing.T) {
	long := strings.Repeat("word ", 400)
	payload, err := json.Marshal(map[string]string{"output_text": long})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	provider, _ := newOpenAITestServer(t, http.StatusOK, string(payload))

	resp, err := provider.Ask(t.Context(), Request{Prompt: "q"})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}

	if len(strings.Fields(resp.ShortAnswer)) > spokenWordLimit+10 {
		t.Errorf("short answer is %d words, want roughly %d", len(strings.Fields(resp.ShortAnswer)), spokenWordLimit)
	}
	if len(strings.Fields(resp.FullAnswer)) != 400 {
		t.Errorf("full answer was truncated: %d words", len(strings.Fields(resp.FullAnswer)))
	}
}

func TestNewConfiguredSelectsProvider(t *testing.T) {
	tests := []struct {
		provider string
		wantName string
		wantErr  bool
	}{
		{config.ProviderOpenAI, openAIName, false},
		{config.ProviderOpenAICompatible, openAICompatibleName, false},
		{config.ProviderAnthropic, anthropicName, false},
		{"opencode", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			cfg := config.Config{
				Provider:   tt.provider,
				OpenAI:     config.OpenAIConfig{Model: "m", APIKey: "k"},
				Compatible: config.OpenAIConfig{Model: "m", APIKey: "k", BaseURL: "https://compatible.example/v1"},
				Anthropic:  config.AnthropicConfig{Model: "m", APIKey: "k"},
			}
			got, err := NewConfigured(&cfg, http.DefaultClient)

			if tt.wantErr {
				if !errors.Is(err, ErrNotConfigured) {
					t.Fatalf("error = %v, want ErrNotConfigured", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewConfigured() error = %v", err)
			}
			if got.Name() != tt.wantName {
				t.Errorf("Name() = %q, want %q", got.Name(), tt.wantName)
			}
		})
	}
}

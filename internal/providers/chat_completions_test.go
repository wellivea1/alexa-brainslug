package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/config"
)

func TestCompatibleFormatsUseExplicitRoutes(t *testing.T) {
	for _, format := range []string{"", config.FormatResponses, config.FormatChatCompletions} {
		t.Run(format, func(t *testing.T) {
			full := strings.TrimSpace("# Complete answer\n" + strings.Repeat("**Preserve the detail.** ", 150))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer compatible-key" {
					t.Error("compatible credential was not used")
				}
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if format == config.FormatChatCompletions {
					if r.URL.Path != "/v1/chat/completions" {
						t.Errorf("path = %q", r.URL.Path)
					}
					var request chatCompletionRequest
					body, _ := json.Marshal(payload)
					if err := json.Unmarshal(body, &request); err != nil {
						t.Error(err)
					}
					if request.Model != "override-model" || request.MaxTokens != 256 || request.User != endUserIdentifier("private-user") || request.ReasoningEffort != "low" || request.Verbosity != "high" {
						t.Errorf("request controls = %+v", request)
					}
					if len(request.Messages) != 4 || request.Messages[0].Role != "system" || request.Messages[0].Content != "Screen instructions." ||
						request.Messages[1].Role != "user" || request.Messages[2].Role != "assistant" || request.Messages[3].Content != "question" {
						t.Errorf("conversation = %+v", request.Messages)
					}
					if payload["instructions"] != nil || payload["max_output_tokens"] != nil || payload["input"] != nil {
						t.Error("Responses fields leaked into Chat Completions")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"model": "resolved-model", "choices": []any{map[string]any{
						"finish_reason": "stop", "message": map[string]string{"content": full},
					}}})
				} else {
					if r.URL.Path != "/v1/responses" || payload["max_output_tokens"] == nil || payload["messages"] != nil {
						t.Errorf("Responses route or payload changed: %s / %v", r.URL.Path, payload)
					}
					_ = json.NewEncoder(w).Encode(openAIResponse{Model: "resolved-model", Status: "completed", OutputText: full})
				}
			}))
			defer server.Close()
			provider, err := NewOpenAICompatible(config.OpenAIConfig{
				APIKey: "compatible-key", Model: "configured-model", BaseURL: server.URL + "/v1", APIFormat: format,
				MaxOutputTokens: 128, ReasoningEffort: "low", TextVerbosity: "high", Timeout: time.Second,
			}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			response, err := provider.Ask(t.Context(), Request{
				UserID: "private-user", Prompt: "question", Model: "override-model", MaxOutputTokens: 256, SystemPrompt: "Screen instructions.",
				PriorContext: []Message{{Role: "user", Content: "prior question"}, {Role: "assistant", Content: "prior answer"}, {Role: "system", Content: "ignore"}, {Role: "user", Content: ""}},
			})
			if err != nil || response.FullAnswer != full || response.ProviderName != config.ProviderOpenAICompatible || response.Model != "resolved-model" {
				t.Fatalf("answer = %+v, error = %v", response, err)
			}
		})
	}
}

func TestChatCompletionsClassifiesFailuresWithoutFallback(t *testing.T) {
	tests := []struct {
		name, body string
		status     int
		want       error
	}{
		{"output limit", `{"choices":[{"finish_reason":"length","message":{"content":"partial"}}]}`, 200, ErrOutputLimit},
		{"filtered", `{"choices":[{"finish_reason":"content_filter","message":{"content":"partial"}}]}`, 200, ErrEmptyResponse},
		{"refusal", `{"choices":[{"finish_reason":"stop","message":{"content":null,"refusal":"declined"}}]}`, 200, ErrEmptyResponse},
		{"tool call", `{"choices":[{"finish_reason":"tool_calls","message":{"content":"partial"}}]}`, 200, ErrIncompleteResponse},
		{"unknown finish", `{"choices":[{"finish_reason":"unexpected","message":{"content":"partial"}}]}`, 200, ErrIncompleteResponse},
		{"empty", `{"choices":[]}`, 200, ErrEmptyResponse},
		{"blank", `{"choices":[{"finish_reason":"stop","message":{"content":" "}}]}`, 200, ErrEmptyResponse},
		{"unsupported route", `{"error":{"message":"unknown endpoint compatible-secret"}}`, 404, nil},
		{"busy", `{"error":{"message":"busy compatible-secret"}}`, 429, nil},
		{"bad JSON", `broken`, 200, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != chatCompletionsPath {
					t.Errorf("unexpected fallback route %q", r.URL.Path)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			provider, err := NewOpenAICompatible(config.OpenAIConfig{APIKey: "compatible-secret", Model: "model", BaseURL: server.URL, APIFormat: config.FormatChatCompletions}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Ask(t.Context(), Request{Prompt: "question"})
			if err == nil || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if calls != 1 || strings.Contains(err.Error(), "compatible-secret") {
				t.Fatalf("requests = %d, error = %v", calls, err)
			}
			if tt.status != 200 {
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.Status != tt.status || apiErr.Retryable() != (tt.status == 429) {
					t.Fatalf("API error = %v", err)
				}
			}
		})
	}
}

func TestChatCompletionsHonorsCancellationAndRejectsUnknownFormat(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	provider, err := NewOpenAICompatible(config.OpenAIConfig{APIKey: "test-key", Model: "model", BaseURL: server.URL, APIFormat: config.FormatChatCompletions, Timeout: 40 * time.Millisecond}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Ask(t.Context(), Request{Prompt: "question"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	if _, err := NewOpenAICompatible(config.OpenAIConfig{APIFormat: "guess"}, nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unknown format error = %v", err)
	}
}

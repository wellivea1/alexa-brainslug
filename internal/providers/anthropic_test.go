package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/config"
)

func TestAnthropicAsk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("missing API key header")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Fatalf("missing anthropic-version header")
		}
		var req anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("request decode error = %v", err)
		}
		if req.Model != "claude-test" || req.MaxTokens != 128 {
			t.Fatalf("request = %+v", req)
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "hello" {
			t.Fatalf("messages = %+v", req.Messages)
		}
		_ = json.NewEncoder(w).Encode(anthropicResponse{
			Model: "claude-test",
			Content: []anthropicContent{
				{Type: "text", Text: "Hello from Claude."},
			},
		})
	}))
	defer server.Close()

	provider := NewAnthropic(config.AnthropicConfig{
		APIKey:          "test-key",
		Model:           "claude-test",
		BaseURL:         server.URL,
		MaxOutputTokens: 128,
		Timeout:         time.Second,
	}, server.Client())

	resp, err := provider.Ask(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if resp.ProviderName != "anthropic" {
		t.Fatalf("ProviderName = %q", resp.ProviderName)
	}
	if resp.FullAnswer != "Hello from Claude." {
		t.Fatalf("response = %+v", resp)
	}
}

func TestAnthropicAskRedactsAPIKeyInError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(anthropicResponse{
			Error: anthropicError{Message: "bad key test-key"},
		})
	}))
	defer server.Close()

	provider := NewAnthropic(config.AnthropicConfig{
		APIKey:          "test-key",
		Model:           "claude-test",
		BaseURL:         server.URL,
		MaxOutputTokens: 128,
		Timeout:         time.Second,
	}, server.Client())

	_, err := provider.Ask(context.Background(), Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Fatalf("error leaked API key: %v", err)
	}
}

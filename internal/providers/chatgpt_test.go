package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/config"
)

type fakeChatGPTCredentials struct {
	connected bool
	access    string
	account   string
}

func (f fakeChatGPTCredentials) Connected() bool { return f.connected }
func (f fakeChatGPTCredentials) Token(context.Context) (accessToken, accountID string, err error) {
	return f.access, f.account, nil
}

func TestChatGPTAskUsesAccountTransportAndNormalizesSSE(t *testing.T) {
	var captured chatGPTRequest
	var rawPayload map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-secret" || request.Header.Get("ChatGPT-Account-Id") != "account-1" {
			t.Errorf("account headers = %#v", request.Header)
		}
		if request.Header.Get("Originator") != "brainslug" || request.Header.Get("Session-Id") == "" {
			t.Errorf("connector headers = %#v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &rawPayload); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"A useful answer.\"}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-resolved\"}}\n\n"))
	}))
	defer server.Close()

	provider := NewChatGPT(config.OpenAIConfig{Model: "gpt-test", MaxOutputTokens: 1024, Timeout: 2 * time.Second}, fakeChatGPTCredentials{true, "access-secret", "account-1"}, server.Client())
	provider.endpoint = server.URL
	response, err := provider.Ask(t.Context(), Request{Prompt: "question"})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if response.FullAnswer != "A useful answer." || response.Model != "gpt-resolved" {
		t.Fatalf("response = %#v", response)
	}
	if !captured.Stream || captured.Store || captured.Model != "gpt-test" || len(captured.Input) != 1 {
		t.Fatalf("request = %#v", captured)
	}
	if _, exists := rawPayload["max_output_tokens"]; exists {
		t.Fatal("ChatGPT account request included unsupported max_output_tokens")
	}
}

func TestChatGPTAskRequiresConnectionAndRedactsToken(t *testing.T) {
	provider := NewChatGPT(config.OpenAIConfig{Model: "gpt-test", MaxOutputTokens: 1024, Timeout: time.Second}, fakeChatGPTCredentials{}, nil)
	if _, err := provider.Ask(t.Context(), Request{Prompt: "question"}); err == nil || !strings.Contains(err.Error(), "connect a ChatGPT account") {
		t.Fatalf("missing connection error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"rejected access-secret"}}`))
	}))
	defer server.Close()
	provider = NewChatGPT(config.OpenAIConfig{Model: "gpt-test", MaxOutputTokens: 1024, Timeout: time.Second}, fakeChatGPTCredentials{true, "access-secret", "account-1"}, server.Client())
	provider.endpoint = server.URL
	_, err := provider.Ask(t.Context(), Request{Prompt: "question"})
	if err == nil || strings.Contains(err.Error(), "access-secret") {
		t.Fatalf("provider error was not redacted: %v", err)
	}
}

package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wellivea1/alexa-brainslug/internal/config"
)

func TestProvidersPreservePresentation(t *testing.T) {
	full := "# Detailed answer\n\n" + strings.Repeat("**Preserve this detail.** ", 150)
	for _, name := range []string{"openai", "openai-compatible", "anthropic", "chatgpt"} {
		for _, instructions := range []string{"", "Write a detailed Markdown answer."} {
			t.Run(name+"/"+instructions, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					field := "instructions"
					if name == "anthropic" {
						field = "system"
					}
					var got string
					if raw, ok := request[field]; ok {
						if err := json.Unmarshal(raw, &got); err != nil {
							t.Error(err)
						}
					}
					if got != instructions {
						t.Errorf("instructions = %q, want %q", got, instructions)
					}
					if name == "anthropic" {
						_ = json.NewEncoder(w).Encode(anthropicResponse{Content: []anthropicContent{{Type: "text", Text: full}}})
					} else {
						_ = json.NewEncoder(w).Encode(openAIResponse{Status: "completed", OutputText: full})
					}
				}))
				defer server.Close()
				cfg := config.OpenAIConfig{APIKey: "test-key", Model: "test-model", BaseURL: server.URL}
				var provider Provider
				switch name {
				case "openai":
					provider = NewOpenAI(cfg, server.Client())
				case "openai-compatible":
					var err error
					provider, err = NewOpenAICompatible(cfg, server.Client())
					if err != nil {
						t.Fatal(err)
					}
				case "anthropic":
					provider = NewAnthropic(config.AnthropicConfig{APIKey: cfg.APIKey, Model: cfg.Model, BaseURL: cfg.BaseURL}, server.Client())
				case "chatgpt":
					connector := NewChatGPT(cfg, fakeChatGPTCredentials{true, "access-secret", "account-1"}, server.Client())
					connector.endpoint = server.URL
					provider = connector
				}
				answer, err := provider.Ask(t.Context(), Request{Prompt: "question", SystemPrompt: instructions})
				if err != nil {
					t.Fatal(err)
				}
				if answer.FullAnswer != strings.TrimSpace(full) {
					t.Fatal("provider altered the full answer's length or Markdown")
				}
			})
		}
	}
}

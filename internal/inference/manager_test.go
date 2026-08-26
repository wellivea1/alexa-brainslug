package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/app"
	"github.com/wellivea1/alexa-brainslug/internal/config"
)

type stubProvider struct{}

type memoryAPIKeyStore struct{ keys map[string]string }

func (s *memoryAPIKeyStore) Get(provider string) (string, bool) {
	key, ok := s.keys[provider]
	return key, ok
}

func (s *memoryAPIKeyStore) Set(provider, key string) error {
	s.keys[provider] = key
	return nil
}

func (s *memoryAPIKeyStore) Remove(provider string) error {
	delete(s.keys, provider)
	return nil
}

type stubChatGPTCredentials struct{ connected bool }

func (s stubChatGPTCredentials) Connected() bool { return s.connected }
func (s stubChatGPTCredentials) Token(context.Context) (accessToken, accountID string, err error) {
	return "access", "account", nil
}

func (stubProvider) Name() string { return "stub" }
func (stubProvider) Ask(_ context.Context, _ app.ProviderRequest) (app.ProviderResponse, error) {
	return app.ProviderResponse{}, nil
}

func TestManagerUpdatePersistsSeparateCompatibleSettings(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "brainslug.settings.json")
	cfg := testConfig(settingsPath)
	service := app.NewService(app.Options{Model: cfg.OpenAI.Model}, stubProvider{}, nil)
	manager, err := NewManager(&cfg, service)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	err = manager.Update(Update{
		Provider:        config.ProviderOpenAICompatible,
		Model:           "gpt-5.6-luna",
		BaseURL:         "https://gateway.example/v1",
		MaxOutputTokens: 4096,
		TimeoutSeconds:  4,
		ReasoningEffort: "none",
		TextVerbosity:   "low",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	snapshot := manager.Snapshot()
	if snapshot.Compatible.Model != "gpt-5.6-luna" || snapshot.Compatible.EndpointKind != "Compatible Responses API" {
		t.Fatalf("snapshot = %#v", snapshot.Compatible)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "secret-value") || strings.Contains(text, "compatible-secret") {
		t.Fatal("runtime settings persisted a credential")
	}
	if !strings.Contains(text, "gpt-5.6-luna") || !strings.Contains(text, "\"reasoning_effort\": \"none\"") {
		t.Fatalf("runtime settings = %s", text)
	}
}

func TestManagerRejectsProviderWithoutCredential(t *testing.T) {
	cfg := testConfig(filepath.Join(t.TempDir(), "settings.json"))
	cfg.Anthropic.APIKey = ""
	service := app.NewService(app.Options{Model: cfg.OpenAI.Model}, stubProvider{}, nil)
	manager, err := NewManager(&cfg, service)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	err = manager.Update(Update{
		Provider: config.ProviderAnthropic, Model: "claude-test",
		MaxOutputTokens: 1024, TimeoutSeconds: 5,
	})
	if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("Update() error = %v", err)
	}
}

func TestSnapshotNeverExposesCredential(t *testing.T) {
	cfg := testConfig(filepath.Join(t.TempDir(), "settings.json"))
	service := app.NewService(app.Options{Model: cfg.OpenAI.Model}, stubProvider{}, nil)
	manager, err := NewManager(&cfg, service)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := manager.Snapshot()
	if !snapshot.OpenAI.CredentialConfigured || snapshot.OpenAI.CredentialSource != "Environment" {
		t.Fatalf("credential state = %#v", snapshot.OpenAI)
	}
	if strings.Contains(snapshot.OpenAI.EffectiveBaseURL, "secret-value") {
		t.Fatal("snapshot leaked a credential")
	}
}

func TestManagerSelectsOnlyConnectedChatGPTAccount(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	cfg := testConfig(settingsPath)
	service := app.NewService(app.Options{Model: cfg.OpenAI.Model}, stubProvider{}, nil)
	manager, err := NewManager(&cfg, service, stubChatGPTCredentials{connected: true})
	if err != nil {
		t.Fatal(err)
	}
	err = manager.Update(Update{
		Provider: config.ProviderOpenAI, AuthMode: config.OpenAIAuthModeChatGPT,
		Model: "gpt-5.6-terra", MaxOutputTokens: 4096, TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	snapshot := manager.Snapshot().OpenAI
	if snapshot.AuthMode != config.OpenAIAuthModeChatGPT || !snapshot.ChatGPTConnected || snapshot.CredentialSource != "ChatGPT account" || snapshot.EndpointKind != "ChatGPT Codex Responses" {
		t.Fatalf("ChatGPT snapshot = %#v", snapshot)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"auth_mode": "chatgpt"`) || strings.Contains(string(data), "access") {
		t.Fatalf("runtime settings = %s", data)
	}

	disconnected, err := NewManager(&cfg, service, stubChatGPTCredentials{})
	if err != nil {
		t.Fatal(err)
	}
	err = disconnected.Update(Update{
		Provider: config.ProviderOpenAI, AuthMode: config.OpenAIAuthModeChatGPT,
		Model: "gpt-5.6-terra", MaxOutputTokens: 4096, TimeoutSeconds: 5,
	})
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("disconnected Update() error = %v", err)
	}
}

func TestManagedAnthropicAPIKeyReachesOfficialMessagesTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("Anthropic path = %q", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "sk-ant-managed" {
			t.Errorf("Anthropic API key header was not supplied")
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("Anthropic version = %q", r.Header.Get("anthropic-version"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "claude-test", "content": []map[string]string{{"type": "text", "text": "Managed key works."}},
		})
	}))
	t.Cleanup(server.Close)

	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	cfg := testConfig(settingsPath)
	cfg.Anthropic.APIKey = ""
	service := app.NewService(app.Options{Model: cfg.OpenAI.Model}, stubProvider{}, nil)
	manager, err := NewManager(&cfg, service)
	if err != nil {
		t.Fatal(err)
	}
	credentials := &memoryAPIKeyStore{keys: make(map[string]string)}
	if err := manager.AttachAPIKeyStore(credentials); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetAPIKey(config.ProviderAnthropic, "sk-ant-managed"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Update(Update{
		Provider: config.ProviderAnthropic, Model: "claude-test", BaseURL: server.URL + "/v1",
		MaxOutputTokens: 128, TimeoutSeconds: 5,
	}); err != nil {
		t.Fatal(err)
	}

	answer, err := service.Ask(t.Context(), app.AskRequest{
		Principal: app.Principal{Namespace: "test", Subject: "managed-key"}, Prompt: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer.FullAnswer != "Managed key works." {
		t.Fatalf("answer = %#v", answer)
	}
	snapshot := manager.Snapshot().Anthropic
	if !snapshot.ManagedAPIKey || snapshot.CredentialSource != "Saved locally" {
		t.Fatalf("managed credential state = %#v", snapshot)
	}

	if err := manager.ClearAPIKey(config.ProviderAnthropic); err != nil {
		t.Fatal(err)
	}
	if state := manager.Snapshot().Anthropic; state.CredentialConfigured || state.ManagedAPIKey {
		t.Fatalf("cleared credential state = %#v", state)
	}
}

func TestCompatibleRouteKeepsItsOwnCredentialAndProviderIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("compatible path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer compatible-managed" {
			t.Errorf("compatible authorization header was not supplied")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "local-model", "status": "completed", "output_text": "Compatible route works.",
		})
	}))
	t.Cleanup(server.Close)

	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	cfg := testConfig(settingsPath)
	cfg.Compatible.APIKey = ""
	service := app.NewService(app.Options{Model: cfg.OpenAI.Model}, stubProvider{}, nil)
	manager, err := NewManager(&cfg, service)
	if err != nil {
		t.Fatal(err)
	}
	credentials := &memoryAPIKeyStore{keys: make(map[string]string)}
	if err := manager.AttachAPIKeyStore(credentials); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetAPIKey(config.ProviderOpenAICompatible, "compatible-managed"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Update(Update{
		Provider: config.ProviderOpenAICompatible, Model: "local-model", BaseURL: server.URL + "/v1",
		MaxOutputTokens: 128, TimeoutSeconds: 5,
	}); err != nil {
		t.Fatal(err)
	}

	answer, err := service.Ask(t.Context(), app.AskRequest{
		Principal: app.Principal{Namespace: "test", Subject: "compatible-route"}, Prompt: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer.FullAnswer != "Compatible route works." || answer.Provider != config.ProviderOpenAICompatible {
		t.Fatalf("answer = %#v", answer)
	}
	snapshot := manager.Snapshot()
	if !snapshot.Compatible.Active || snapshot.OpenAI.Active || snapshot.Compatible.CredentialSource != "Saved locally" {
		t.Fatalf("separate compatible state = %#v", snapshot)
	}
}

func testConfig(settingsPath string) config.Config {
	return config.Config{
		ServerAddr:          "127.0.0.1:8080",
		RuntimeSettingsPath: settingsPath,
		SQLitePath:          filepath.Join(filepath.Dir(settingsPath), "brainslug.db"),
		Provider:            config.ProviderOpenAI,
		AlexaVerifyRequests: false,
		SaveHistory:         true,
		OpenAI: config.OpenAIConfig{
			APIKey: "secret-value", Model: "gpt-5.6-terra", MaxOutputTokens: 25000,
			Timeout: 6 * time.Second, ReasoningEffort: "low", TextVerbosity: "low",
		},
		Compatible: config.OpenAIConfig{
			APIKey: "compatible-secret", Model: "compatible-model", BaseURL: "http://127.0.0.1:11434/v1", MaxOutputTokens: 4096,
			Timeout: 6 * time.Second,
		},
		Anthropic: config.AnthropicConfig{
			APIKey: "anthropic-secret", Model: "claude-test", MaxOutputTokens: 4096,
			Timeout: 6 * time.Second,
		},
	}
}

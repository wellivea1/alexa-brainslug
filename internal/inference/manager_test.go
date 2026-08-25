package inference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/app"
	"github.com/wellivea1/alexa-brainslug/internal/config"
)

type stubProvider struct{}

type stubChatGPTCredentials struct{ connected bool }

func (s stubChatGPTCredentials) Connected() bool { return s.connected }
func (s stubChatGPTCredentials) Token(context.Context) (accessToken, accountID string, err error) {
	return "access", "account", nil
}

func (stubProvider) Name() string { return "stub" }
func (stubProvider) Ask(_ context.Context, _ app.ProviderRequest) (app.ProviderResponse, error) {
	return app.ProviderResponse{}, nil
}

func TestManagerUpdatePersistsNonSecretSettings(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "brainslug.settings.json")
	cfg := testConfig(settingsPath)
	service := app.NewService(app.Options{Model: cfg.OpenAI.Model}, stubProvider{}, nil)
	manager, err := NewManager(&cfg, service)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	err = manager.Update(Update{
		Provider:        config.ProviderOpenAI,
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
	if snapshot.OpenAI.Model != "gpt-5.6-luna" || snapshot.OpenAI.EndpointKind != "OpenAI-compatible Responses API" {
		t.Fatalf("snapshot = %#v", snapshot.OpenAI)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "secret-value") {
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
		Anthropic: config.AnthropicConfig{
			APIKey: "anthropic-secret", Model: "claude-test", MaxOutputTokens: 4096,
			Timeout: 6 * time.Second,
		},
	}
}

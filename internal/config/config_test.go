package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadRequiresExplicitModel(t *testing.T) {
	t.Setenv("AI_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_API_KEY_FILE", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected missing model error")
	}
}

func TestLoadConfigFromEnvironment(t *testing.T) {
	t.Setenv("SERVER_ADDR", ":9090")
	t.Setenv("PUBLIC_BASE_URL", "https://brain-slug.example.test")
	t.Setenv("AI_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_API_KEY_FILE", "")
	t.Setenv("OPENAI_MODEL", "gpt-test")
	t.Setenv("OPENAI_BASE_URL", "https://api.example.test/v1")
	t.Setenv("OPENAI_MAX_OUTPUT_TOKENS", "128")
	t.Setenv("OPENAI_TIMEOUT_SECONDS", "3")
	t.Setenv("SQLITE_PATH", "test.db")
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")
	t.Setenv("ALEXA_ALLOWED_SKILL_ID", "amzn1.ask.skill.test")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("SAVE_HISTORY", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ServerAddr != ":9090" {
		t.Fatalf("ServerAddr = %q", cfg.ServerAddr)
	}
	if cfg.OpenAI.Model != "gpt-test" {
		t.Fatalf("OpenAI.Model = %q", cfg.OpenAI.Model)
	}
	if cfg.Provider != "openai" {
		t.Fatalf("Provider = %q", cfg.Provider)
	}
	if cfg.SelectedModel() != "gpt-test" {
		t.Fatalf("SelectedModel = %q", cfg.SelectedModel())
	}
	if cfg.OpenAI.MaxOutputTokens != 128 {
		t.Fatalf("MaxOutputTokens = %d", cfg.OpenAI.MaxOutputTokens)
	}
	if cfg.OpenAI.Timeout != 3*time.Second {
		t.Fatalf("Timeout = %s", cfg.OpenAI.Timeout)
	}
	if cfg.AlexaVerifyRequests {
		t.Fatal("AlexaVerifyRequests should be false")
	}
	if cfg.SaveHistory {
		t.Fatal("SaveHistory should be false")
	}
}

func TestLoadDotEnvDoesNotOverrideExistingEnv(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "existing-model")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("OPENAI_MODEL=from-file\nSERVER_ADDR=:7070\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv() error = %v", err)
	}

	if got := os.Getenv("OPENAI_MODEL"); got != "existing-model" {
		t.Fatalf("OPENAI_MODEL = %q", got)
	}
	if got := os.Getenv("SERVER_ADDR"); got != ":7070" {
		t.Fatalf("SERVER_ADDR = %q", got)
	}
}

func TestReadSecretFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openai.key")
	if err := os.WriteFile(path, []byte("# comment\nOPENAI_API_KEY=sk-test-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ReadSecretFile(path)
	if err != nil {
		t.Fatalf("ReadSecretFile() error = %v", err)
	}
	if got != "sk-test-value" {
		t.Fatalf("secret = %q", got)
	}
}

func TestLoadReadsOpenAIAPIKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openai.key")
	if err := os.WriteFile(path, []byte("sk-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	t.Setenv("OPENAI_API_KEY_FILE", path)
	t.Setenv("OPENAI_MODEL", "gpt-test")
	t.Setenv("AI_PROVIDER", "openai")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.OpenAI.APIKey != "sk-from-file" {
		t.Fatalf("APIKey = %q", cfg.OpenAI.APIKey)
	}
}

func TestLoadAnthropicProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anthropic.key")
	if err := os.WriteFile(path, []byte("anthropic-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AI_PROVIDER", "anthropic")
	t.Setenv("ANTHROPIC_API_KEY_FILE", path)
	t.Setenv("ANTHROPIC_MODEL", "claude-test")
	t.Setenv("ANTHROPIC_MAX_OUTPUT_TOKENS", "128")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Provider != "anthropic" {
		t.Fatalf("Provider = %q", cfg.Provider)
	}
	if cfg.Anthropic.APIKey != "anthropic-from-file" {
		t.Fatalf("Anthropic APIKey = %q", cfg.Anthropic.APIKey)
	}
	if cfg.SelectedModel() != "claude-test" {
		t.Fatalf("SelectedModel = %q", cfg.SelectedModel())
	}
	if cfg.SelectedMaxOutputTokens() != 128 {
		t.Fatalf("SelectedMaxOutputTokens = %d", cfg.SelectedMaxOutputTokens())
	}
}

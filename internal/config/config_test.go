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
	t.Setenv("OPENAI_REASONING_EFFORT", "high")
	t.Setenv("OPENAI_TEXT_VERBOSITY", "medium")
	t.Setenv("SQLITE_PATH", "test.db")
	t.Setenv("RUNTIME_SETTINGS_PATH", filepath.Join(t.TempDir(), "missing-settings.json"))
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
	if cfg.OpenAI.ReasoningEffort != "high" || cfg.OpenAI.TextVerbosity != "medium" {
		t.Fatalf("OpenAI response controls = %q/%q", cfg.OpenAI.ReasoningEffort, cfg.OpenAI.TextVerbosity)
	}
	if cfg.AlexaVerifyRequests {
		t.Fatal("AlexaVerifyRequests should be false")
	}
	if cfg.SaveHistory {
		t.Fatal("SaveHistory should be false")
	}
}

func TestRuntimeSettingsOverrideNonSecretInferenceConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brainslug.settings.json")
	settings := RuntimeSettings{
		Provider: "anthropic",
		OpenAI: OpenAIRuntimeSettings{
			ProviderRuntimeSettings: ProviderRuntimeSettings{
				Model: "gpt-5.6-luna", MaxOutputTokens: 4096, TimeoutSeconds: 4,
			},
			ReasoningEffort: "none",
			TextVerbosity:   "low",
		},
		Anthropic: ProviderRuntimeSettings{
			Model: "claude-runtime", BaseURL: "https://anthropic.example/v1", MaxOutputTokens: 2048, TimeoutSeconds: 5,
		},
	}
	if err := SaveRuntimeSettings(path, settings); err != nil {
		t.Fatalf("SaveRuntimeSettings() error = %v", err)
	}

	t.Setenv("RUNTIME_SETTINGS_PATH", path)
	t.Setenv("AI_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "gpt-environment")
	t.Setenv("ANTHROPIC_MODEL", "claude-environment")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Provider != ProviderAnthropic || cfg.Anthropic.Model != "claude-runtime" {
		t.Fatalf("runtime provider/model = %q/%q", cfg.Provider, cfg.Anthropic.Model)
	}
	if cfg.Anthropic.BaseURL != "https://anthropic.example/v1" || cfg.Anthropic.Timeout != 5*time.Second {
		t.Fatalf("runtime Anthropic settings = %#v", cfg.Anthropic)
	}
	if cfg.OpenAI.APIKey != "" {
		t.Fatal("runtime settings unexpectedly supplied a credential")
	}
}

func TestRuntimeSettingsCanRestoreProviderResponseControlDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brainslug.settings.json")
	settings := RuntimeSettings{
		Provider: ProviderOpenAI,
		OpenAI: OpenAIRuntimeSettings{ProviderRuntimeSettings: ProviderRuntimeSettings{
			Model: "compatible-model", MaxOutputTokens: 256, TimeoutSeconds: 6,
		}},
	}
	if err := SaveRuntimeSettings(path, settings); err != nil {
		t.Fatalf("SaveRuntimeSettings() error = %v", err)
	}

	t.Setenv("RUNTIME_SETTINGS_PATH", path)
	t.Setenv("AI_PROVIDER", ProviderOpenAI)
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_MODEL", "environment-model")
	t.Setenv("OPENAI_REASONING_EFFORT", "high")
	t.Setenv("OPENAI_TEXT_VERBOSITY", "high")
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.OpenAI.ReasoningEffort != "" || cfg.OpenAI.TextVerbosity != "" {
		t.Fatalf("response controls = %q/%q, want provider defaults", cfg.OpenAI.ReasoningEffort, cfg.OpenAI.TextVerbosity)
	}
}

func TestValidateRejectsUnknownOpenAIResponseControls(t *testing.T) {
	cfg := Config{
		ServerAddr:          "127.0.0.1:8080",
		Provider:            ProviderOpenAI,
		SQLitePath:          "test.db",
		AlexaVerifyRequests: false,
		OpenAI: OpenAIConfig{
			Model: "gpt-test", MaxOutputTokens: 128, Timeout: time.Second,
			ReasoningEffort: "turbo", TextVerbosity: "low",
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted an unsupported reasoning effort")
	}
	cfg.OpenAI.ReasoningEffort = "low"
	cfg.OpenAI.TextVerbosity = "verbose"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted an unsupported text verbosity")
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
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")

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
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")

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

func TestValidateRequiresAllowedSkillIDInVerifiedMode(t *testing.T) {
	cfg := Config{
		ServerAddr:          "127.0.0.1:8080",
		Provider:            ProviderOpenAI,
		SQLitePath:          "test.db",
		OpenAI:              OpenAIConfig{Model: "gpt-test", MaxOutputTokens: 128, Timeout: time.Second},
		AlexaVerifyRequests: true,
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() accepted verified mode without ALEXA_ALLOWED_SKILL_ID")
	}

	cfg.AlexaVerifyRequests = false
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected controlled verification-disabled mode: %v", err)
	}
}

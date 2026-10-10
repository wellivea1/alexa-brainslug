package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	t.Setenv("ADMIN_SETUP_TRUST_PROXY", "true")
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
	if cfg.Compatible.Model != "gpt-test" {
		t.Fatalf("Compatible.Model = %q", cfg.Compatible.Model)
	}
	if cfg.Provider != ProviderOpenAICompatible {
		t.Fatalf("Provider = %q", cfg.Provider)
	}
	if cfg.SelectedModel() != "gpt-test" {
		t.Fatalf("SelectedModel = %q", cfg.SelectedModel())
	}
	if cfg.Compatible.MaxOutputTokens != 128 {
		t.Fatalf("MaxOutputTokens = %d", cfg.Compatible.MaxOutputTokens)
	}
	if cfg.Compatible.Timeout != 3*time.Second {
		t.Fatalf("Timeout = %s", cfg.Compatible.Timeout)
	}
	if cfg.Compatible.ReasoningEffort != "high" || cfg.Compatible.TextVerbosity != "medium" {
		t.Fatalf("compatible response controls = %q/%q", cfg.Compatible.ReasoningEffort, cfg.Compatible.TextVerbosity)
	}
	if cfg.AlexaVerifyRequests {
		t.Fatal("AlexaVerifyRequests should be false")
	}
	if cfg.SaveHistory {
		t.Fatal("SaveHistory should be false")
	}
	if !cfg.AdminSetupTrustProxy {
		t.Fatal("AdminSetupTrustProxy should be true")
	}
}

func TestLoadExplicitCompatibleProvider(t *testing.T) {
	t.Setenv("AI_PROVIDER", ProviderOpenAICompatible)
	t.Setenv("OPENAI_MODEL", "gpt-official")
	t.Setenv("OPENAI_COMPATIBLE_API_KEY", "compatible-key")
	t.Setenv("OPENAI_COMPATIBLE_MODEL", "local-model")
	t.Setenv("OPENAI_COMPATIBLE_BASE_URL", "http://127.0.0.1:11434/v1")
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != ProviderOpenAICompatible || cfg.SelectedModel() != "local-model" {
		t.Fatalf("selected route = %q/%q", cfg.Provider, cfg.SelectedModel())
	}
	if cfg.OpenAI.Model != "gpt-official" || cfg.Compatible.BaseURL != "http://127.0.0.1:11434/v1" {
		t.Fatalf("separate provider settings = %#v / %#v", cfg.OpenAI, cfg.Compatible)
	}
}

func TestLegacyRuntimeSettingsMoveCustomEndpointToCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.settings.json")
	legacy := `{
  "version": 1,
  "provider": "openai",
  "openai": {
    "model": "legacy-local-model",
    "base_url": "http://127.0.0.1:11434/v1",
    "max_output_tokens": 256,
    "timeout_seconds": 6,
    "reasoning_effort": "",
    "text_verbosity": ""
  },
  "anthropic": {"model":"", "max_output_tokens":4096, "timeout_seconds":6}
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNTIME_SETTINGS_PATH", path)
	t.Setenv("OPENAI_MODEL", "gpt-official")
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != ProviderOpenAICompatible || cfg.Compatible.Model != "legacy-local-model" {
		t.Fatalf("legacy route = %q/%#v", cfg.Provider, cfg.Compatible)
	}
	if cfg.OpenAI.Model != "gpt-official" {
		t.Fatalf("official OpenAI model was overwritten: %q", cfg.OpenAI.Model)
	}
}

// writeSettingsFile stores a settings document exactly as it would appear on
// disk.
func writeSettingsFile(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brainslug.settings.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRuntimeSettingsOverrideNonSecretInferenceConfig(t *testing.T) {
	path := writeSettingsFile(t, `{
  "version": 2,
  "provider": "anthropic",
  "openai": {"model": "gpt-5.6-luna", "max_output_tokens": 4096, "timeout_seconds": 4, "reasoning_effort": "none", "text_verbosity": "low"},
  "anthropic": {"model": "claude-runtime", "base_url": "https://anthropic.example/v1", "max_output_tokens": 2048, "timeout_seconds": 5}
}`)

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

// A present empty value is a deliberate dashboard choice to restore the
// provider default, unlike an absent one.
func TestRuntimeSettingsCanRestoreProviderResponseControlDefaults(t *testing.T) {
	path := writeSettingsFile(t, `{
  "version": 2,
  "provider": "openai",
  "openai": {"model": "compatible-model", "max_output_tokens": 256, "timeout_seconds": 6, "reasoning_effort": "", "text_verbosity": ""}
}`)

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

func TestRuntimeSettingsSelectChatGPTAndDerivePrivateCredentialPath(t *testing.T) {
	settingsPath := writeSettingsFile(t, `{
  "version": 2,
  "provider": "openai",
  "openai": {"model": "gpt-5.6-terra", "max_output_tokens": 1024, "timeout_seconds": 6, "auth_mode": "chatgpt"}
}`)
	directory := filepath.Dir(settingsPath)
	databasePath := filepath.Join(directory, "brainslug.db")
	t.Setenv("RUNTIME_SETTINGS_PATH", settingsPath)
	t.Setenv("SQLITE_PATH", databasePath)
	t.Setenv("OPENAI_MODEL", "gpt-environment")
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OpenAI.AuthMode != OpenAIAuthModeChatGPT {
		t.Fatalf("AuthMode = %q", cfg.OpenAI.AuthMode)
	}
	wantAuthPath := filepath.Join(directory, "brainslug.openai-auth.json")
	if cfg.OpenAI.ChatGPTAuthFile != wantAuthPath {
		t.Fatalf("ChatGPTAuthFile = %q, want %q", cfg.OpenAI.ChatGPTAuthFile, wantAuthPath)
	}
	wantProviderAuthPath := filepath.Join(directory, "brainslug.provider-auth.json")
	if cfg.ProviderAuthFile != wantProviderAuthPath {
		t.Fatalf("ProviderAuthFile = %q, want %q", cfg.ProviderAuthFile, wantProviderAuthPath)
	}
	wantAdminAuthPath := filepath.Join(directory, "brainslug.admin-auth.json")
	if cfg.AdminAuthFile != wantAdminAuthPath {
		t.Fatalf("AdminAuthFile = %q, want %q", cfg.AdminAuthFile, wantAdminAuthPath)
	}
}

// A settings file written for one change must not erase environment values it
// never mentioned. Absent fields used to be read as empty, which silently
// replaced an operator's proxy base URL and reasoning effort.
func TestRuntimeSettingsKeepEnvironmentForAbsentFields(t *testing.T) {
	path := writeSettingsFile(t, `{
  "version": 2,
  "provider": "anthropic",
  "openai": {"model": "gpt-dashboard"},
  "anthropic": {"model": "claude-dashboard"}
}`)
	t.Setenv("RUNTIME_SETTINGS_PATH", path)
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")
	t.Setenv("AI_PROVIDER", ProviderAnthropic)
	t.Setenv("ANTHROPIC_MODEL", "claude-environment")
	t.Setenv("ANTHROPIC_BASE_URL", "https://proxy.example/v1")
	t.Setenv("OPENAI_REASONING_EFFORT", "high")
	t.Setenv("OPENAI_TEXT_VERBOSITY", "low")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Anthropic.BaseURL != "https://proxy.example/v1" {
		t.Errorf("Anthropic base URL = %q, want the environment value", cfg.Anthropic.BaseURL)
	}
	if cfg.OpenAI.ReasoningEffort != "high" || cfg.OpenAI.TextVerbosity != "low" {
		t.Errorf("OpenAI controls = %q/%q, want the environment values", cfg.OpenAI.ReasoningEffort, cfg.OpenAI.TextVerbosity)
	}
	if cfg.Anthropic.Model != "claude-dashboard" || cfg.OpenAI.Model != "gpt-dashboard" {
		t.Errorf("models = %q/%q, want the dashboard values", cfg.Anthropic.Model, cfg.OpenAI.Model)
	}
}

// The dashboard stores only what differs from the environment, so later
// edits to .env still take effect for everything else, and choosing the
// environment's value again removes the override.
func TestRuntimeSettingsRecordOnlyChangesFromEnvironment(t *testing.T) {
	t.Setenv("RUNTIME_SETTINGS_PATH", filepath.Join(t.TempDir(), "brainslug.settings.json"))
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")
	t.Setenv("AI_PROVIDER", ProviderOpenAI)
	t.Setenv("OPENAI_MODEL", "gpt-environment")
	t.Setenv("ANTHROPIC_MODEL", "claude-environment")
	t.Setenv("ANTHROPIC_BASE_URL", "https://proxy.example/v1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings := RuntimeSettingsFromConfig(&cfg); !reflect.DeepEqual(settings, RuntimeSettings{Version: runtimeSettingsVersion}) {
		t.Fatalf("unchanged configuration produced settings %+v", settings)
	}

	cfg.Provider = ProviderAnthropic
	cfg.Anthropic.Model = "claude-dashboard"
	if err := SaveRuntimeSettings(cfg.RuntimeSettingsPath, RuntimeSettingsFromConfig(&cfg)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfg.RuntimeSettingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"version":   float64(runtimeSettingsVersion),
		"provider":  ProviderAnthropic,
		"providers": map[string]any{ProviderAnthropic: map[string]any{"model": "claude-dashboard"}},
	}
	if !reflect.DeepEqual(saved, want) {
		t.Fatalf("saved settings = %s, want only the changed values", data)
	}

	cfg.Anthropic.Model = "claude-environment"
	if settings := RuntimeSettingsFromConfig(&cfg); settings.Providers[ProviderAnthropic] != nil {
		t.Errorf("restoring the environment model kept an override: %+v", *settings.Providers[ProviderAnthropic])
	}
}

func TestCompatibleFormatSurvivesSettingsReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("RUNTIME_SETTINGS_PATH", path)
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")
	t.Setenv("AI_PROVIDER", ProviderOpenAICompatible)
	t.Setenv("OPENAI_COMPATIBLE_MODEL", "local-model")
	t.Setenv("OPENAI_COMPATIBLE_BASE_URL", "http://127.0.0.1:11434/v1")
	t.Setenv("OPENAI_COMPATIBLE_API_FORMAT", FormatResponses)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Compatible.APIFormat = FormatChatCompletions
	if err := SaveRuntimeSettings(path, RuntimeSettingsFromConfig(&cfg)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved RuntimeSettings
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	settings := saved.Providers[ProviderOpenAICompatible]
	if saved.Version != 3 || settings == nil || settings.APIFormat == nil || *settings.APIFormat != FormatChatCompletions || settings.Model != nil {
		t.Fatalf("saved settings = %s", data)
	}
	reloaded, err := Load()
	if err != nil || reloaded.Compatible.APIFormat != FormatChatCompletions || reloaded.Compatible.BaseURL != cfg.Compatible.BaseURL {
		t.Fatalf("reloaded settings = %+v, error = %v", reloaded.Compatible, err)
	}
	overrides := reloaded.Overrides()
	if len(overrides) != 1 || overrides[0].Variable != "OPENAI_COMPATIBLE_API_FORMAT" {
		t.Fatalf("format override is not visible: %+v", overrides)
	}
}

func TestCompatibleFormatDefaultsAndValidation(t *testing.T) {
	for _, format := range []string{"", FormatResponses, FormatChatCompletions, "guess"} {
		t.Run(format, func(t *testing.T) {
			t.Setenv("RUNTIME_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))
			t.Setenv("ALEXA_VERIFY_REQUESTS", "false")
			t.Setenv("AI_PROVIDER", ProviderOpenAICompatible)
			t.Setenv("OPENAI_COMPATIBLE_MODEL", "model")
			t.Setenv("OPENAI_COMPATIBLE_BASE_URL", "http://127.0.0.1:11434/v1")
			t.Setenv("OPENAI_COMPATIBLE_API_FORMAT", format)
			cfg, err := Load()
			if format == "guess" {
				if err == nil || !strings.Contains(err.Error(), "OPENAI_COMPATIBLE_API_FORMAT") {
					t.Fatalf("invalid format error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := format
			if want == "" {
				want = FormatResponses
			}
			if cfg.Compatible.APIFormat != want {
				t.Fatalf("format = %q, want %q", cfg.Compatible.APIFormat, want)
			}
		})
	}
}

func TestKeyedSettingsRejectUnknownRoutesAndSecrets(t *testing.T) {
	for _, block := range []string{`"unknown":{"model":"model"}`, `"openai":{"api_key":"private-value"}`} {
		t.Run(block, func(t *testing.T) {
			t.Setenv("RUNTIME_SETTINGS_PATH", writeSettingsFile(t, `{"version":3,"providers":{`+block+`}}`))
			t.Setenv("ALEXA_VERIFY_REQUESTS", "false")
			t.Setenv("AI_PROVIDER", ProviderOpenAI)
			t.Setenv("OPENAI_MODEL", "model")
			if _, err := Load(); err == nil {
				t.Fatal("unknown routes and secrets must not enter runtime settings")
			}
		})
	}
}

func TestOverridesReportReplacedEnvironmentVariables(t *testing.T) {
	path := writeSettingsFile(t, `{
  "version": 2,
  "openai": {"model": "gpt-dashboard", "timeout_seconds": 6}
}`)
	t.Setenv("RUNTIME_SETTINGS_PATH", path)
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")
	t.Setenv("AI_PROVIDER", ProviderOpenAI)
	t.Setenv("OPENAI_MODEL", "gpt-environment")
	t.Setenv("OPENAI_TIMEOUT_SECONDS", "6")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []Override{{Variable: "OPENAI_MODEL", Environment: "gpt-environment", InEffect: "gpt-dashboard"}}
	if got := cfg.Overrides(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Overrides() = %+v, want %+v", got, want)
	}

	cfg.OpenAI.Model = "gpt-environment"
	if got := cfg.Overrides(); len(got) != 0 {
		t.Errorf("Overrides() after restoring the environment value = %+v", got)
	}
}

func TestLoadRejectsProviderTimeoutBeyondVoiceLimit(t *testing.T) {
	t.Setenv("RUNTIME_SETTINGS_PATH", filepath.Join(t.TempDir(), "missing-settings.json"))
	t.Setenv("AI_PROVIDER", ProviderOpenAI)
	t.Setenv("OPENAI_MODEL", "gpt-test")
	t.Setenv("OPENAI_TIMEOUT_SECONDS", "7")
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "OPENAI_TIMEOUT_SECONDS") {
		t.Fatalf("Load() error = %v, want a timeout error naming OPENAI_TIMEOUT_SECONDS", err)
	}
}

// Earlier dashboards accepted timeouts up to 60 seconds. A saved value above
// the voice limit is reduced with a warning instead of failing startup.
func TestRuntimeSettingsReduceTimeoutBeyondVoiceLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brainslug.settings.json")
	saved := `{
  "version": 2,
  "provider": "openai",
  "openai": {"model": "gpt-test", "max_output_tokens": 1024, "timeout_seconds": 30, "reasoning_effort": "", "text_verbosity": ""},
  "anthropic": {"model": "", "max_output_tokens": 4096, "timeout_seconds": 6}
}`
	if err := os.WriteFile(path, []byte(saved), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNTIME_SETTINGS_PATH", path)
	t.Setenv("AI_PROVIDER", ProviderOpenAI)
	t.Setenv("OPENAI_MODEL", "gpt-test")
	t.Setenv("ALEXA_VERIFY_REQUESTS", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if want := MaxProviderTimeoutSeconds * time.Second; cfg.OpenAI.Timeout != want {
		t.Errorf("OpenAI timeout = %v, want %v", cfg.OpenAI.Timeout, want)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "timeout_seconds 30") {
		t.Errorf("Warnings = %q, want one timeout warning", cfg.Warnings)
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
	cfg.OpenAI.TextVerbosity = "low"
	cfg.OpenAI.AuthMode = "password"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted an unsupported OpenAI authentication mode")
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

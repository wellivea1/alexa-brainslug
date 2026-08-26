package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	runtimeSettingsVersion = 2
	legacySettingsVersion  = 1
	inMemorySQLitePath     = ":memory:"
)

// RuntimeSettings is the non-secret inference configuration managed by the
// local dashboard. API keys are deliberately absent: they live in the
// dedicated provider auth file, environment variables, or explicit secret
// files.
type RuntimeSettings struct {
	Version    int                     `json:"version"`
	Provider   string                  `json:"provider"`
	OpenAI     OpenAIRuntimeSettings   `json:"openai"`
	Compatible OpenAIRuntimeSettings   `json:"openai_compatible,omitempty"`
	Anthropic  ProviderRuntimeSettings `json:"anthropic"`
}

// ProviderRuntimeSettings contains settings shared by inference providers.
type ProviderRuntimeSettings struct {
	Model           string `json:"model"`
	BaseURL         string `json:"base_url,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
}

// OpenAIRuntimeSettings extends the shared settings with current Responses
// API controls supported by the GPT-5.6 model family.
type OpenAIRuntimeSettings struct {
	ProviderRuntimeSettings
	AuthMode        string `json:"auth_mode,omitempty"`
	ReasoningEffort string `json:"reasoning_effort"`
	TextVerbosity   string `json:"text_verbosity"`
}

// RuntimeSettingsFromConfig returns the persistable subset of cfg.
func RuntimeSettingsFromConfig(cfg *Config) RuntimeSettings {
	return RuntimeSettings{
		Version:  runtimeSettingsVersion,
		Provider: cfg.Provider,
		OpenAI: OpenAIRuntimeSettings{
			ProviderRuntimeSettings: ProviderRuntimeSettings{
				Model:           cfg.OpenAI.Model,
				BaseURL:         cfg.OpenAI.BaseURL,
				MaxOutputTokens: cfg.OpenAI.MaxOutputTokens,
				TimeoutSeconds:  durationSeconds(cfg.OpenAI.Timeout),
			},
			AuthMode:        cfg.OpenAI.AuthMode,
			ReasoningEffort: cfg.OpenAI.ReasoningEffort,
			TextVerbosity:   cfg.OpenAI.TextVerbosity,
		},
		Compatible: OpenAIRuntimeSettings{
			ProviderRuntimeSettings: ProviderRuntimeSettings{
				Model:           cfg.Compatible.Model,
				BaseURL:         cfg.Compatible.BaseURL,
				MaxOutputTokens: cfg.Compatible.MaxOutputTokens,
				TimeoutSeconds:  durationSeconds(cfg.Compatible.Timeout),
			},
			AuthMode:        OpenAIAuthModeAPIKey,
			ReasoningEffort: cfg.Compatible.ReasoningEffort,
			TextVerbosity:   cfg.Compatible.TextVerbosity,
		},
		Anthropic: ProviderRuntimeSettings{
			Model:           cfg.Anthropic.Model,
			BaseURL:         cfg.Anthropic.BaseURL,
			MaxOutputTokens: cfg.Anthropic.MaxOutputTokens,
			TimeoutSeconds:  durationSeconds(cfg.Anthropic.Timeout),
		},
	}
}

// SaveRuntimeSettings persists only non-secret settings. The parent directory
// is the same private directory used for SQLite in normal deployments.
func SaveRuntimeSettings(path string, settings RuntimeSettings) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("runtime settings path is unavailable")
	}
	settings.Version = runtimeSettingsVersion
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime settings: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create runtime settings directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write runtime settings: %w", err)
	}
	return nil
}

func (c *Config) applyRuntimeSettingsFile() error {
	c.RuntimeSettingsPath = resolveRuntimeSettingsPath(c.RuntimeSettingsPath, c.SQLitePath)
	if c.RuntimeSettingsPath == "" {
		return nil
	}

	data, err := os.ReadFile(c.RuntimeSettingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read runtime settings: %w", err)
	}

	var settings RuntimeSettings
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return fmt.Errorf("decode runtime settings: %w", err)
	}
	if settings.Version != legacySettingsVersion && settings.Version != runtimeSettingsVersion {
		return fmt.Errorf("runtime settings version must be %d or %d", legacySettingsVersion, runtimeSettingsVersion)
	}
	if settings.Version == legacySettingsVersion {
		c.applyLegacyRuntimeSettings(settings)
	} else {
		c.applyRuntimeSettings(settings)
	}
	return nil
}

func (c *Config) applyRuntimeSettings(settings RuntimeSettings) {
	if provider := strings.ToLower(strings.TrimSpace(settings.Provider)); provider != "" {
		c.Provider = provider
	}
	applyProviderRuntime(&c.OpenAI.Model, &c.OpenAI.BaseURL, &c.OpenAI.MaxOutputTokens, &c.OpenAI.Timeout, settings.OpenAI.ProviderRuntimeSettings)
	if authMode := strings.ToLower(strings.TrimSpace(settings.OpenAI.AuthMode)); authMode != "" {
		c.OpenAI.AuthMode = authMode
	}
	// Empty values intentionally restore the provider's own defaults. This is
	// especially important for OpenAI-compatible servers that do not implement
	// OpenAI's reasoning or text-verbosity request extensions.
	c.OpenAI.ReasoningEffort = strings.ToLower(strings.TrimSpace(settings.OpenAI.ReasoningEffort))
	c.OpenAI.TextVerbosity = strings.ToLower(strings.TrimSpace(settings.OpenAI.TextVerbosity))
	applyProviderRuntime(&c.Compatible.Model, &c.Compatible.BaseURL, &c.Compatible.MaxOutputTokens, &c.Compatible.Timeout, settings.Compatible.ProviderRuntimeSettings)
	c.Compatible.AuthMode = OpenAIAuthModeAPIKey
	c.Compatible.ReasoningEffort = strings.ToLower(strings.TrimSpace(settings.Compatible.ReasoningEffort))
	c.Compatible.TextVerbosity = strings.ToLower(strings.TrimSpace(settings.Compatible.TextVerbosity))
	applyProviderRuntime(&c.Anthropic.Model, &c.Anthropic.BaseURL, &c.Anthropic.MaxOutputTokens, &c.Anthropic.Timeout, settings.Anthropic)
}

func (c *Config) applyLegacyRuntimeSettings(settings RuntimeSettings) {
	provider := strings.ToLower(strings.TrimSpace(settings.Provider))
	if provider == ProviderOpenAI && isCustomOpenAIBaseURL(settings.OpenAI.BaseURL) {
		c.Provider = ProviderOpenAICompatible
		applyProviderRuntime(&c.Compatible.Model, &c.Compatible.BaseURL, &c.Compatible.MaxOutputTokens, &c.Compatible.Timeout, settings.OpenAI.ProviderRuntimeSettings)
		c.Compatible.AuthMode = OpenAIAuthModeAPIKey
		c.Compatible.ReasoningEffort = strings.ToLower(strings.TrimSpace(settings.OpenAI.ReasoningEffort))
		c.Compatible.TextVerbosity = strings.ToLower(strings.TrimSpace(settings.OpenAI.TextVerbosity))
	} else {
		if provider != "" {
			c.Provider = provider
		}
		applyProviderRuntime(&c.OpenAI.Model, &c.OpenAI.BaseURL, &c.OpenAI.MaxOutputTokens, &c.OpenAI.Timeout, settings.OpenAI.ProviderRuntimeSettings)
		if authMode := strings.ToLower(strings.TrimSpace(settings.OpenAI.AuthMode)); authMode != "" {
			c.OpenAI.AuthMode = authMode
		}
		c.OpenAI.ReasoningEffort = strings.ToLower(strings.TrimSpace(settings.OpenAI.ReasoningEffort))
		c.OpenAI.TextVerbosity = strings.ToLower(strings.TrimSpace(settings.OpenAI.TextVerbosity))
	}
	applyProviderRuntime(&c.Anthropic.Model, &c.Anthropic.BaseURL, &c.Anthropic.MaxOutputTokens, &c.Anthropic.Timeout, settings.Anthropic)
}

func applyProviderRuntime(model, baseURL *string, maxOutputTokens *int, timeout *time.Duration, settings ProviderRuntimeSettings) {
	if value := strings.TrimSpace(settings.Model); value != "" {
		*model = value
	}
	// An explicitly empty base URL restores the provider default.
	*baseURL = strings.TrimSpace(settings.BaseURL)
	if settings.MaxOutputTokens > 0 {
		*maxOutputTokens = settings.MaxOutputTokens
	}
	if settings.TimeoutSeconds > 0 {
		*timeout = time.Duration(settings.TimeoutSeconds) * time.Second
	}
}

func resolveRuntimeSettingsPath(configured, sqlitePath string) string {
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured
	}
	sqlitePath = strings.TrimSpace(sqlitePath)
	if sqlitePath == "" || sqlitePath == inMemorySQLitePath || strings.HasPrefix(sqlitePath, "file:") {
		return ""
	}
	extension := filepath.Ext(sqlitePath)
	base := strings.TrimSuffix(sqlitePath, extension)
	return base + ".settings.json"
}

func resolveOpenAIAuthPath(configured, sqlitePath string) string {
	if path := strings.TrimSpace(configured); path != "" {
		return path
	}
	databasePath := strings.TrimSpace(sqlitePath)
	if databasePath == "" || databasePath == inMemorySQLitePath || strings.HasPrefix(databasePath, "file:") {
		return ""
	}
	extension := filepath.Ext(databasePath)
	base := strings.TrimSuffix(databasePath, extension)
	if base == "" {
		base = databasePath
	}
	return base + ".openai-auth.json"
}

func resolveProviderAuthPath(configured, sqlitePath string) string {
	if path := strings.TrimSpace(configured); path != "" {
		return path
	}
	databasePath := strings.TrimSpace(sqlitePath)
	if databasePath == "" || databasePath == inMemorySQLitePath || strings.HasPrefix(databasePath, "file:") {
		return ""
	}
	extension := filepath.Ext(databasePath)
	base := strings.TrimSuffix(databasePath, extension)
	if base == "" {
		base = databasePath
	}
	return base + ".provider-auth.json"
}

func durationSeconds(value time.Duration) int {
	if value <= 0 {
		return 0
	}
	return int(value / time.Second)
}

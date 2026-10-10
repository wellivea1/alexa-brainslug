package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/securefile"
)

const (
	runtimeSettingsVersion = 3
	legacySettingsVersion  = 1
	inMemorySQLitePath     = ":memory:"
)

// RuntimeSettings is the non-secret inference configuration managed by the
// local dashboard. API keys are deliberately absent: they live in the
// dedicated provider auth file, environment variables, or explicit secret
// files.
//
// Every field is optional and records an explicit dashboard choice. An absent
// field leaves the environment's value in place, so editing .env keeps working
// for anything the dashboard has not changed, and the dashboard writes only
// values that differ from the environment. A present empty base URL, reasoning
// effort, or text verbosity deliberately restores the provider default.
type RuntimeSettings struct {
	Version   int                                 `json:"version"`
	Provider  *string                             `json:"provider,omitempty"`
	Providers map[string]*RuntimeProviderSettings `json:"providers,omitempty"`
	Codex     *RuntimeCodexSettings               `json:"codex,omitempty"`
}

type legacyRuntimeSettings struct {
	Version    int                      `json:"version"`
	Provider   *string                  `json:"provider,omitempty"`
	OpenAI     *RuntimeProviderSettings `json:"openai,omitempty"`
	Compatible *RuntimeProviderSettings `json:"openai_compatible,omitempty"`
	Anthropic  *ProviderRuntimeSettings `json:"anthropic,omitempty"`
}

// ProviderRuntimeSettings contains settings shared by inference providers.
type ProviderRuntimeSettings struct {
	Model           *string `json:"model,omitempty"`
	BaseURL         *string `json:"base_url,omitempty"`
	MaxOutputTokens *int    `json:"max_output_tokens,omitempty"`
	TimeoutSeconds  *int    `json:"timeout_seconds,omitempty"`
}

// RuntimeProviderSettings records optional controls for one provider. Its
// definition determines which controls are available.
type RuntimeProviderSettings struct {
	ProviderRuntimeSettings
	AuthMode        *string `json:"auth_mode,omitempty"`
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
	TextVerbosity   *string `json:"text_verbosity,omitempty"`
	APIFormat       *string `json:"api_format,omitempty"`
}

// Override is a dashboard-saved value in effect instead of an environment
// variable the operator set explicitly.
type Override struct {
	Variable    string
	Environment string
	InEffect    string
}

// environmentBaseline is the configuration as the environment and built-in
// defaults alone describe it, before the settings file was applied.
type environmentBaseline struct {
	config Config

	// explicit holds the variables that were set to a nonempty value.
	explicit map[string]bool
}

// RuntimeSettingsFromConfig returns the dashboard-managed values in cfg that
// differ from the environment cfg was loaded from. A configuration built
// without an environment, as in tests, records every value that is set.
func RuntimeSettingsFromConfig(cfg *Config) RuntimeSettings {
	var base Config
	if cfg.environment != nil {
		base = cfg.environment.config
	}
	settings := RuntimeSettings{Version: runtimeSettingsVersion, Provider: changed(cfg.Provider, base.Provider)}
	settings.Codex = codexChanges(cfg.Codex, base.Codex)
	for _, definition := range ProviderDefinitions() {
		current, previous := definition.Settings(cfg), definition.Settings(&base)
		changes := RuntimeProviderSettings{
			ProviderRuntimeSettings: sharedChanges(
				current.Model, current.BaseURL, current.MaxOutputTokens, current.Timeout,
				previous.Model, previous.BaseURL, previous.MaxOutputTokens, previous.Timeout),
		}
		if definition.AccountAuth {
			changes.AuthMode = changed(current.AuthMode, previous.AuthMode)
		}
		if definition.ResponseControls {
			changes.ReasoningEffort = changed(current.ReasoningEffort, previous.ReasoningEffort)
			changes.TextVerbosity = changed(current.TextVerbosity, previous.TextVerbosity)
		}
		if definition.SelectableFormat {
			changes.APIFormat = changed(definition.WireFormat(current), definition.WireFormat(previous))
		}
		if changes != (RuntimeProviderSettings{}) {
			if settings.Providers == nil {
				settings.Providers = make(map[string]*RuntimeProviderSettings)
			}
			settings.Providers[definition.ID] = &changes
		}
	}
	return settings
}

func sharedChanges(model, baseURL string, maxOutputTokens int, timeout time.Duration, baseModel, baseBaseURL string, baseMaxOutputTokens int, baseTimeout time.Duration) ProviderRuntimeSettings {
	return ProviderRuntimeSettings{
		Model:           changed(model, baseModel),
		BaseURL:         changed(baseURL, baseBaseURL),
		MaxOutputTokens: changed(maxOutputTokens, baseMaxOutputTokens),
		TimeoutSeconds:  changed(durationSeconds(timeout), durationSeconds(baseTimeout)),
	}
}

// changed returns value when it differs from base, and nil when there is
// nothing for the settings file to record.
func changed[T comparable](value, base T) *T {
	if value == base {
		return nil
	}
	return &value
}

// Overrides lists explicitly set environment variables whose values the
// dashboard settings currently replace. Saving the environment's value from
// the dashboard removes the override, because only differences from the
// environment are stored.
func (c *Config) Overrides() []Override {
	if c.environment == nil {
		return nil
	}
	base := &c.environment.config
	var overrides []Override
	check := func(variable, environment, current string) {
		if c.environment.explicit[variable] && environment != current {
			overrides = append(overrides, Override{Variable: variable, Environment: environment, InEffect: current})
		}
	}
	check("AI_PROVIDER", base.Provider, c.Provider)
	for _, definition := range ProviderDefinitions() {
		current, previous := definition.Settings(c), definition.Settings(base)
		prefix := definition.EnvPrefix
		check(prefix+"_MODEL", previous.Model, current.Model)
		check(prefix+"_BASE_URL", previous.BaseURL, current.BaseURL)
		check(prefix+"_MAX_OUTPUT_TOKENS", strconv.Itoa(previous.MaxOutputTokens), strconv.Itoa(current.MaxOutputTokens))
		check(prefix+"_TIMEOUT_SECONDS", strconv.Itoa(durationSeconds(previous.Timeout)), strconv.Itoa(durationSeconds(current.Timeout)))
		if definition.AccountAuth {
			check(prefix+"_AUTH_MODE", previous.AuthMode, current.AuthMode)
		}
		if definition.ResponseControls {
			check(prefix+"_REASONING_EFFORT", previous.ReasoningEffort, current.ReasoningEffort)
			check(prefix+"_TEXT_VERBOSITY", previous.TextVerbosity, current.TextVerbosity)
		}
		if definition.SelectableFormat {
			check(prefix+"_API_FORMAT", definition.WireFormat(previous), definition.WireFormat(current))
		}
	}
	return overrides
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
	if err := securefile.Write(path, append(data, '\n')); err != nil {
		return fmt.Errorf("save runtime settings: %w", err)
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

	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return fmt.Errorf("decode runtime settings: %w", err)
	}
	var settings RuntimeSettings
	switch header.Version {
	case runtimeSettingsVersion:
		if err := securefile.DecodeJSON(data, &settings); err != nil {
			return fmt.Errorf("decode runtime settings: %w", err)
		}
	case legacySettingsVersion, 2:
		var legacy legacyRuntimeSettings
		if err := securefile.DecodeJSON(data, &legacy); err != nil {
			return fmt.Errorf("decode runtime settings: %w", err)
		}
		settings = RuntimeSettings{Version: runtimeSettingsVersion, Provider: legacy.Provider, Providers: make(map[string]*RuntimeProviderSettings)}
		if legacy.OpenAI != nil {
			settings.Providers[ProviderOpenAI] = legacy.OpenAI
		}
		if legacy.Compatible != nil {
			settings.Providers[ProviderOpenAICompatible] = legacy.Compatible
		}
		if legacy.Anthropic != nil {
			settings.Providers[ProviderAnthropic] = &RuntimeProviderSettings{ProviderRuntimeSettings: *legacy.Anthropic}
		}
		if header.Version == legacySettingsVersion && lowered(legacy.Provider) == ProviderOpenAI && legacy.OpenAI != nil &&
			legacy.OpenAI.BaseURL != nil && isCustomOpenAIBaseURL(*legacy.OpenAI.BaseURL) {
			provider := ProviderOpenAICompatible
			settings.Provider = &provider
			settings.Providers[provider] = legacy.OpenAI
			delete(settings.Providers, ProviderOpenAI)
		}
	default:
		return fmt.Errorf("runtime settings version must be 1, 2, or %d", runtimeSettingsVersion)
	}
	return c.applyRuntimeSettings(settings)
}

func (c *Config) applyRuntimeSettings(settings RuntimeSettings) error {
	c.Codex.apply(settings.Codex)
	for id := range settings.Providers {
		if _, ok := LookupProvider(id); !ok {
			return fmt.Errorf("runtime settings contains unknown provider %q", id)
		}
	}
	if provider := lowered(settings.Provider); provider != "" {
		c.Provider = provider
	}
	for _, definition := range ProviderDefinitions() {
		saved := settings.Providers[definition.ID]
		if saved == nil {
			continue
		}
		current := definition.Settings(c)
		c.applyProviderRuntime(definition.ID, &current.Model, &current.BaseURL, &current.MaxOutputTokens, &current.Timeout, saved.ProviderRuntimeSettings)
		if definition.AccountAuth {
			if mode := lowered(saved.AuthMode); mode != "" {
				current.AuthMode = mode
			}
		}
		if definition.ResponseControls {
			applyOptional(&current.ReasoningEffort, saved.ReasoningEffort)
			applyOptional(&current.TextVerbosity, saved.TextVerbosity)
		}
		if definition.SelectableFormat {
			applyOptional(&current.APIFormat, saved.APIFormat)
		}
	}
	return nil
}

// applyProviderRuntime overlays one provider block. section names the block in
// the settings file for operator-facing warnings. A model, token cap, or
// timeout cannot be cleared from the dashboard, so an empty or zero value
// there is no override at all.
func (c *Config) applyProviderRuntime(section string, model, baseURL *string, maxOutputTokens *int, timeout *time.Duration, settings ProviderRuntimeSettings) {
	if settings.Model != nil {
		if value := strings.TrimSpace(*settings.Model); value != "" {
			*model = value
		}
	}
	// A present empty base URL restores the provider default; an absent one
	// keeps the environment's value.
	if settings.BaseURL != nil {
		*baseURL = strings.TrimSpace(*settings.BaseURL)
	}
	if settings.MaxOutputTokens != nil && *settings.MaxOutputTokens > 0 {
		*maxOutputTokens = *settings.MaxOutputTokens
	}
	if settings.TimeoutSeconds != nil && *settings.TimeoutSeconds > 0 {
		seconds := *settings.TimeoutSeconds
		// Earlier dashboards allowed up to 60 seconds. Such a value was never
		// usable for a voice request, so it is reduced rather than failing a
		// startup the operator did not cause by editing anything.
		if seconds > MaxProviderTimeoutSeconds {
			c.Warnings = append(c.Warnings, fmt.Sprintf(
				"%s: %s timeout_seconds %d exceeds the %d-second voice limit; using %d",
				c.RuntimeSettingsPath, section, seconds, MaxProviderTimeoutSeconds, MaxProviderTimeoutSeconds))
			seconds = MaxProviderTimeoutSeconds
		}
		*timeout = time.Duration(seconds) * time.Second
	}
}

// applyOptional applies a present value, including an empty one, which
// restores the provider default. An absent value leaves target unchanged.
func applyOptional(target, value *string) {
	if value != nil {
		*target = strings.ToLower(strings.TrimSpace(*value))
	}
}

// lowered returns a trimmed lower-case optional value, or "" when absent.
func lowered(value *string) string {
	if value == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(*value))
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

func resolveAdminAuthPath(configured, sqlitePath string) string {
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
	return base + ".admin-auth.json"
}

func durationSeconds(value time.Duration) int {
	if value <= 0 {
		return 0
	}
	return int(value / time.Second)
}

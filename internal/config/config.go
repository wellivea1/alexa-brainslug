// Package config loads and validates operator configuration from the
// environment.
//
// Two rules shape this package. Configuration is explicit: there is no
// inferred model, no provider guessed from which key happens to be set, and
// no silently substituted default for a value the operator supplied. And
// configuration is validated once, at startup, so that a misconfiguration is
// a startup failure with a named variable rather than a failed voice request
// an hour later.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Supported values for AI_PROVIDER.
const (
	ProviderOpenAI           = "openai"
	ProviderOpenAICompatible = "openai-compatible"
	ProviderAnthropic        = "anthropic"

	OpenAIAuthModeAPIKey  = "api_key"
	OpenAIAuthModeChatGPT = "chatgpt"
)

// Defaults applied when a variable is unset. A variable that is set but
// unparseable is an error, never a silent fall back to these.
const (
	defaultServerAddr                = "127.0.0.1:8080"
	defaultSQLitePath                = "./brainslug.db"
	defaultOpenAIMaxOutputTokens     = 25000
	defaultCompatibleMaxOutputTokens = 4096
	defaultOpenAIReasoningEffort     = ""
	defaultOpenAITextVerbosity       = ""
	defaultAnthropicMaxOutputTokens  = 4096
	defaultTimeoutSeconds            = 6
	defaultLogLevel                  = "info"

	// minOutputTokens is the floor below which a response cannot hold a
	// usable sentence.
	minOutputTokens = 16
)

// Config is the validated runtime configuration.
type Config struct {
	// ServerAddr is the public ASK bind address. It defaults to host loopback;
	// container deployments override it inside the container.
	ServerAddr string

	// PublicBaseURL is the externally reachable origin. It is reference
	// information for operators and controls whether HSTS is emitted; the
	// service never dials it.
	PublicBaseURL string

	// Provider selects the AI backend.
	Provider string

	// Provider settings are independent so switching routes in the dashboard
	// never overwrites another provider's model or endpoint.
	OpenAI     OpenAIConfig
	Compatible OpenAIConfig
	Anthropic  AnthropicConfig

	// SQLitePath is the local history database.
	SQLitePath string

	// RuntimeSettingsPath stores dashboard-managed, non-secret inference
	// settings. Credentials are never written here.
	RuntimeSettingsPath string

	// ProviderAuthFile is the private local file used for API keys entered in
	// the loopback dashboard. Environment variables and explicit *_FILE
	// secrets are still supported independently.
	ProviderAuthFile string

	// AlexaVerifyRequests gates Alexa request signature verification. It
	// defaults to true and fails closed.
	AlexaVerifyRequests bool

	// AlexaAllowedSkillID, when set, restricts the endpoint to one skill.
	AlexaAllowedSkillID string

	// LogLevel is debug, info, warn, or error.
	LogLevel string

	// SaveHistory controls whether interactions are persisted.
	SaveHistory bool
}

// OpenAIConfig holds OpenAI provider settings.
type OpenAIConfig struct {
	APIKey          string
	APIKeyFile      string
	AuthMode        string
	ChatGPTAuthFile string
	Model           string
	BaseURL         string
	MaxOutputTokens int
	Timeout         time.Duration
	ReasoningEffort string
	TextVerbosity   string
}

// AnthropicConfig holds Anthropic provider settings.
type AnthropicConfig struct {
	APIKey          string
	APIKeyFile      string
	Model           string
	BaseURL         string
	MaxOutputTokens int
	Timeout         time.Duration
}

// LoadDotEnv reads path and sets any variable that is not already present in
// the process environment. A missing file is not an error.
//
// It mutates the process environment, so it must run before Load and should
// not run concurrently with anything reading the environment. Variables
// already set win, which lets a shell or container override the file.
func LoadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() {
		// Read-only handle; a close failure cannot lose data.
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, lineNumber)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("%s:%d: empty key", path, lineNumber)
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, parseDotEnvValue(value)); err != nil {
			return fmt.Errorf("%s:%d: set %s: %w", path, lineNumber, key, err)
		}
	}
	return scanner.Err()
}

// Load reads configuration from the environment and validates it.
//
// Every parse failure is collected and reported together, so an operator
// fixing a broken .env sees every problem in one run rather than one per
// restart.
func Load() (Config, error) {
	env := &envReader{}

	cfg := Config{
		ServerAddr:    env.str("SERVER_ADDR", defaultServerAddr),
		PublicBaseURL: env.str("PUBLIC_BASE_URL", ""),
		Provider:      strings.ToLower(env.str("AI_PROVIDER", ProviderOpenAI)),
		OpenAI: OpenAIConfig{
			APIKey:          env.str("OPENAI_API_KEY", ""),
			APIKeyFile:      env.str("OPENAI_API_KEY_FILE", ""),
			AuthMode:        strings.ToLower(env.str("OPENAI_AUTH_MODE", OpenAIAuthModeAPIKey)),
			ChatGPTAuthFile: env.str("OPENAI_CHATGPT_AUTH_FILE", ""),
			Model:           env.str("OPENAI_MODEL", ""),
			BaseURL:         env.str("OPENAI_BASE_URL", ""),
			MaxOutputTokens: env.intVal("OPENAI_MAX_OUTPUT_TOKENS", defaultOpenAIMaxOutputTokens),
			Timeout:         env.seconds("OPENAI_TIMEOUT_SECONDS", defaultTimeoutSeconds),
			ReasoningEffort: strings.ToLower(env.str("OPENAI_REASONING_EFFORT", defaultOpenAIReasoningEffort)),
			TextVerbosity:   strings.ToLower(env.str("OPENAI_TEXT_VERBOSITY", defaultOpenAITextVerbosity)),
		},
		Compatible: OpenAIConfig{
			APIKey:          env.str("OPENAI_COMPATIBLE_API_KEY", ""),
			APIKeyFile:      env.str("OPENAI_COMPATIBLE_API_KEY_FILE", ""),
			AuthMode:        OpenAIAuthModeAPIKey,
			Model:           env.str("OPENAI_COMPATIBLE_MODEL", ""),
			BaseURL:         env.str("OPENAI_COMPATIBLE_BASE_URL", ""),
			MaxOutputTokens: env.intVal("OPENAI_COMPATIBLE_MAX_OUTPUT_TOKENS", defaultCompatibleMaxOutputTokens),
			Timeout:         env.seconds("OPENAI_COMPATIBLE_TIMEOUT_SECONDS", defaultTimeoutSeconds),
			ReasoningEffort: strings.ToLower(env.str("OPENAI_COMPATIBLE_REASONING_EFFORT", "")),
			TextVerbosity:   strings.ToLower(env.str("OPENAI_COMPATIBLE_TEXT_VERBOSITY", "")),
		},
		Anthropic: AnthropicConfig{
			APIKey:          env.str("ANTHROPIC_API_KEY", ""),
			APIKeyFile:      env.str("ANTHROPIC_API_KEY_FILE", ""),
			Model:           env.str("ANTHROPIC_MODEL", ""),
			BaseURL:         env.str("ANTHROPIC_BASE_URL", ""),
			MaxOutputTokens: env.intVal("ANTHROPIC_MAX_OUTPUT_TOKENS", defaultAnthropicMaxOutputTokens),
			Timeout:         env.seconds("ANTHROPIC_TIMEOUT_SECONDS", defaultTimeoutSeconds),
		},
		SQLitePath:          env.str("SQLITE_PATH", defaultSQLitePath),
		RuntimeSettingsPath: env.str("RUNTIME_SETTINGS_PATH", ""),
		ProviderAuthFile:    env.str("PROVIDER_AUTH_FILE", ""),
		AlexaVerifyRequests: env.boolVal("ALEXA_VERIFY_REQUESTS", true),
		AlexaAllowedSkillID: env.str("ALEXA_ALLOWED_SKILL_ID", ""),
		LogLevel:            env.str("LOG_LEVEL", defaultLogLevel),
		SaveHistory:         env.boolVal("SAVE_HISTORY", true),
	}

	if err := env.err(); err != nil {
		return Config{}, err
	}
	cfg.migrateLegacyCompatibleEnvironment()
	if err := cfg.applyRuntimeSettingsFile(); err != nil {
		return Config{}, err
	}
	cfg.OpenAI.ChatGPTAuthFile = resolveOpenAIAuthPath(cfg.OpenAI.ChatGPTAuthFile, cfg.SQLitePath)
	cfg.ProviderAuthFile = resolveProviderAuthPath(cfg.ProviderAuthFile, cfg.SQLitePath)

	if err := cfg.loadSecretFiles(); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// loadSecretFiles replaces inline credentials with file contents where a
// *_FILE variable points at one. The file wins, so a stale inline value
// cannot shadow a mounted secret.
func (c *Config) loadSecretFiles() error {
	sources := []struct {
		variable string
		path     string
		target   *string
	}{
		{"OPENAI_API_KEY_FILE", c.OpenAI.APIKeyFile, &c.OpenAI.APIKey},
		{"OPENAI_COMPATIBLE_API_KEY_FILE", c.Compatible.APIKeyFile, &c.Compatible.APIKey},
		{"ANTHROPIC_API_KEY_FILE", c.Anthropic.APIKeyFile, &c.Anthropic.APIKey},
	}

	for _, source := range sources {
		if strings.TrimSpace(source.path) == "" {
			continue
		}
		secret, err := ReadSecretFile(source.path)
		if err != nil {
			return fmt.Errorf("%s: %w", source.variable, err)
		}
		*source.target = secret
	}
	return nil
}

// Validate checks the settings the selected provider needs. Settings for the
// other provider are left alone.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("configuration is required")
	}
	if strings.TrimSpace(c.ServerAddr) == "" {
		return errors.New("SERVER_ADDR must not be empty")
	}
	if strings.TrimSpace(c.SQLitePath) == "" {
		return errors.New("SQLITE_PATH must not be empty")
	}
	if c.AlexaVerifyRequests && strings.TrimSpace(c.AlexaAllowedSkillID) == "" {
		return errors.New("ALEXA_ALLOWED_SKILL_ID must be set when ALEXA_VERIFY_REQUESTS=true")
	}

	switch c.Provider {
	case ProviderOpenAI:
		if isCustomOpenAIBaseURL(c.OpenAI.BaseURL) {
			return errors.New("OPENAI_BASE_URL is a compatible endpoint; use AI_PROVIDER=openai-compatible and OPENAI_COMPATIBLE_BASE_URL")
		}
		if strings.TrimSpace(c.OpenAI.AuthMode) == "" {
			c.OpenAI.AuthMode = OpenAIAuthModeAPIKey
		}
		if err := validateProvider("OPENAI", c.OpenAI.Model, c.OpenAI.MaxOutputTokens, c.OpenAI.Timeout); err != nil {
			return err
		}
		if strings.TrimSpace(c.OpenAI.ReasoningEffort) != "" && !oneOf(c.OpenAI.ReasoningEffort, "none", "low", "medium", "high", "xhigh", "max") {
			return errors.New("OPENAI_REASONING_EFFORT must be one of none, low, medium, high, xhigh, max")
		}
		if strings.TrimSpace(c.OpenAI.TextVerbosity) != "" && !oneOf(c.OpenAI.TextVerbosity, "low", "medium", "high") {
			return errors.New("OPENAI_TEXT_VERBOSITY must be one of low, medium, high")
		}
		if !oneOf(c.OpenAI.AuthMode, OpenAIAuthModeAPIKey, OpenAIAuthModeChatGPT) {
			return errors.New("OPENAI_AUTH_MODE must be one of api_key, chatgpt")
		}
		return nil
	case ProviderOpenAICompatible:
		if err := validateProvider("OPENAI_COMPATIBLE", c.Compatible.Model, c.Compatible.MaxOutputTokens, c.Compatible.Timeout); err != nil {
			return err
		}
		if strings.TrimSpace(c.Compatible.BaseURL) == "" {
			return errors.New("OPENAI_COMPATIBLE_BASE_URL must be set explicitly")
		}
		if strings.TrimSpace(c.Compatible.ReasoningEffort) != "" && !oneOf(c.Compatible.ReasoningEffort, "none", "low", "medium", "high", "xhigh", "max") {
			return errors.New("OPENAI_COMPATIBLE_REASONING_EFFORT must be one of none, low, medium, high, xhigh, max")
		}
		if strings.TrimSpace(c.Compatible.TextVerbosity) != "" && !oneOf(c.Compatible.TextVerbosity, "low", "medium", "high") {
			return errors.New("OPENAI_COMPATIBLE_TEXT_VERBOSITY must be one of low, medium, high")
		}
		c.Compatible.AuthMode = OpenAIAuthModeAPIKey
		return nil
	case ProviderAnthropic:
		return validateProvider("ANTHROPIC", c.Anthropic.Model, c.Anthropic.MaxOutputTokens, c.Anthropic.Timeout)
	default:
		return fmt.Errorf("AI_PROVIDER must be one of %s, %s, %s", ProviderOpenAI, ProviderOpenAICompatible, ProviderAnthropic)
	}
}

func oneOf(value string, allowed ...string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

// validateProvider applies the same rules to every provider block, named by
// the operator-facing variable prefix.
func validateProvider(prefix, model string, maxOutputTokens int, timeout time.Duration) error {
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("%s_MODEL must be set explicitly", prefix)
	}
	if maxOutputTokens < minOutputTokens {
		return fmt.Errorf("%s_MAX_OUTPUT_TOKENS must be at least %d", prefix, minOutputTokens)
	}
	if timeout <= 0 {
		return fmt.Errorf("%s_TIMEOUT_SECONDS must be positive", prefix)
	}
	return nil
}

// SelectedModel reports the model the selected provider will use.
func (c *Config) SelectedModel() string {
	switch c.Provider {
	case ProviderOpenAICompatible:
		return c.Compatible.Model
	case ProviderAnthropic:
		return c.Anthropic.Model
	default:
		return c.OpenAI.Model
	}
}

// SelectedMaxOutputTokens reports the output cap for the selected provider.
func (c *Config) SelectedMaxOutputTokens() int {
	switch c.Provider {
	case ProviderOpenAICompatible:
		return c.Compatible.MaxOutputTokens
	case ProviderAnthropic:
		return c.Anthropic.MaxOutputTokens
	default:
		return c.OpenAI.MaxOutputTokens
	}
}

// SelectedTimeout reports the provider HTTP timeout for the selected
// provider.
func (c *Config) SelectedTimeout() time.Duration {
	switch c.Provider {
	case ProviderOpenAICompatible:
		return c.Compatible.Timeout
	case ProviderAnthropic:
		return c.Anthropic.Timeout
	default:
		return c.OpenAI.Timeout
	}
}

// SecretValues returns every credential in the configuration, for redaction
// before anything is logged or stored. Blank entries are harmless: redaction
// ignores short values.
func (c *Config) SecretValues() []string {
	return []string{c.OpenAI.APIKey, c.Compatible.APIKey, c.Anthropic.APIKey}
}

// migrateLegacyCompatibleEnvironment preserves the original configuration
// contract where OPENAI_* plus a custom base URL meant an OpenAI-compatible
// server. New deployments use the independent OPENAI_COMPATIBLE_* block.
func (c *Config) migrateLegacyCompatibleEnvironment() {
	if c.Provider != ProviderOpenAI || c.OpenAI.AuthMode == OpenAIAuthModeChatGPT || !isCustomOpenAIBaseURL(c.OpenAI.BaseURL) {
		return
	}
	if strings.TrimSpace(c.Compatible.Model) != "" || strings.TrimSpace(c.Compatible.BaseURL) != "" {
		return
	}
	legacy := c.OpenAI
	chatGPTAuthFile := legacy.ChatGPTAuthFile
	legacy.AuthMode = OpenAIAuthModeAPIKey
	legacy.ChatGPTAuthFile = ""
	c.Compatible = legacy
	c.Provider = ProviderOpenAICompatible
	c.OpenAI = OpenAIConfig{
		AuthMode:        OpenAIAuthModeAPIKey,
		ChatGPTAuthFile: chatGPTAuthFile,
		MaxOutputTokens: defaultOpenAIMaxOutputTokens,
		Timeout:         time.Duration(defaultTimeoutSeconds) * time.Second,
	}
}

func isCustomOpenAIBaseURL(value string) bool {
	normalized := strings.TrimRight(strings.ToLower(strings.TrimSpace(value)), "/")
	return normalized != "" && normalized != "https://api.openai.com/v1"
}

// envReader reads environment variables and accumulates parse failures so
// that Load can report all of them at once.
type envReader struct {
	errs []error
}

func (r *envReader) str(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func (r *envReader) intVal(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s must be a whole number, got %q", key, raw))
		return fallback
	}
	return parsed
}

func (r *envReader) boolVal(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s must be true or false, got %q", key, raw))
		return fallback
	}
	return parsed
}

func (r *envReader) seconds(key string, fallback int) time.Duration {
	return time.Duration(r.intVal(key, fallback)) * time.Second
}

func (r *envReader) err() error {
	return errors.Join(r.errs...)
}

// parseDotEnvValue unquotes a .env value, honouring Go-style escapes inside
// double quotes and treating single quotes as literal.
func parseDotEnvValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 2 {
		return value
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
		return strings.TrimSuffix(strings.TrimPrefix(value, "'"), "'")
	}
	return value
}

// ReadSecretFile reads exactly one secret from path.
//
// It tolerates the shapes secret files arrive in: a bare value, a KEY=value
// line, or a KEY: value line, with comments and blank lines ignored. More
// than one value is an error rather than a guess, because picking the wrong
// line would authenticate as the wrong identity.
func ReadSecretFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		// Read-only handle; a close failure cannot lose data.
		_ = file.Close()
	}()

	var values []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if value := secretLineValue(line); value != "" {
			values = append(values, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if len(values) == 0 {
		return "", errors.New("file contains no secret value")
	}
	if len(values) > 1 {
		return "", errors.New("file must contain exactly one non-comment secret value")
	}
	return values[0], nil
}

// secretLineValue extracts the value from a secret file line, stripping a
// leading KEY= or KEY: prefix when one is present.
func secretLineValue(line string) string {
	for _, delimiter := range []string{"=", ":"} {
		if idx := strings.Index(line, delimiter); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			if isConfigLikeKey(key) {
				return strings.Trim(strings.TrimSpace(line[idx+1:]), `"`)
			}
		}
	}
	return strings.Trim(strings.TrimSpace(line), `"`)
}

// isConfigLikeKey reports whether value looks like a configuration key rather
// than part of a secret that happens to contain a delimiter.
func isConfigLikeKey(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

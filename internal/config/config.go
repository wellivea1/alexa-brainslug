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
	defaultAnthropicMaxOutputTokens  = 4096
	defaultTimeoutSeconds            = 6
	defaultLogLevel                  = "info"

	// minOutputTokens is the floor below which a response cannot hold a
	// usable sentence.
	minOutputTokens = 16
)

// MaxProviderTimeoutSeconds caps the provider call. Alexa abandons a skill
// response after eight seconds, and the ASK response budget also covers
// request verification and saving the outcome, so a longer provider timeout
// could never be used: the request would be cut off first.
const MaxProviderTimeoutSeconds = 6

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
	Codex      CodexConfig

	// SQLitePath is the local history database.
	SQLitePath string

	// RuntimeSettingsPath stores dashboard-managed, non-secret inference
	// settings. Credentials are never written here.
	RuntimeSettingsPath string

	// ProviderAuthFile is the private local file used for API keys entered in
	// the loopback dashboard. Environment variables and explicit *_FILE
	// secrets are still supported independently.
	ProviderAuthFile string

	// AdminAuthFile stores the first-run management owner password hash. It is
	// separate from provider credentials and never contains a session token.
	AdminAuthFile string

	// AdminSetupTrustProxy permits a trusted local container proxy to relay
	// first-run setup while preserving a loopback Host header.
	AdminSetupTrustProxy bool

	// AlexaVerifyRequests gates Alexa request signature verification. It
	// defaults to true and fails closed.
	AlexaVerifyRequests bool

	// AlexaAllowedSkillID, when set, restricts the endpoint to one skill.
	AlexaAllowedSkillID string

	// LogLevel is debug, info, warn, or error.
	LogLevel string

	// SaveHistory controls whether interactions are persisted.
	SaveHistory bool

	// Warnings describes configuration that was accepted but adjusted, for
	// the caller to log once logging is configured.
	Warnings []string

	// environment is the configuration before dashboard settings were
	// applied. Saving settings records only differences from it. It is
	// shared, never modified, by copies of this Config.
	environment *environmentBaseline
}

// ProviderConfig holds the settings shared by outbound model transports.
type ProviderConfig struct {
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
	APIFormat       string
}

// OpenAIConfig retains the constructor configuration name used by OpenAI clients.
type OpenAIConfig = ProviderConfig

// AnthropicConfig retains the constructor configuration name used by Anthropic clients.
type AnthropicConfig = ProviderConfig

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
		Codex:         CodexConfig{Enabled: env.boolVal("CODEX_ENABLED", false), Mode: env.str("CODEX_ROUTING_MODE", "manual"), URL: env.str("CODEX_CONTROLLER_URL", ""), Certificate: env.str("CODEX_CLIENT_CERT", ""), Key: env.str("CODEX_CLIENT_KEY", ""), CA: env.str("CODEX_CA_FILE", ""), ServerName: env.str("CODEX_SERVER_NAME", "")},

		SQLitePath:           env.str("SQLITE_PATH", defaultSQLitePath),
		RuntimeSettingsPath:  env.str("RUNTIME_SETTINGS_PATH", ""),
		ProviderAuthFile:     env.str("PROVIDER_AUTH_FILE", ""),
		AdminAuthFile:        env.str("ADMIN_AUTH_FILE", ""),
		AdminSetupTrustProxy: env.boolVal("ADMIN_SETUP_TRUST_PROXY", false),
		AlexaVerifyRequests:  env.boolVal("ALEXA_VERIFY_REQUESTS", true),
		AlexaAllowedSkillID:  env.str("ALEXA_ALLOWED_SKILL_ID", ""),
		LogLevel:             env.str("LOG_LEVEL", defaultLogLevel),
		SaveHistory:          env.boolVal("SAVE_HISTORY", true),
	}

	for _, definition := range ProviderDefinitions() {
		settings := definition.Settings(&cfg)
		prefix := definition.EnvPrefix
		*settings = ProviderConfig{
			APIKey:          env.str(prefix+"_API_KEY", ""),
			APIKeyFile:      env.str(prefix+"_API_KEY_FILE", ""),
			Model:           env.str(prefix+"_MODEL", ""),
			BaseURL:         env.str(prefix+"_BASE_URL", ""),
			MaxOutputTokens: env.intVal(prefix+"_MAX_OUTPUT_TOKENS", definition.DefaultOutputTokens),
			Timeout:         env.seconds(prefix+"_TIMEOUT_SECONDS", defaultTimeoutSeconds),
			AuthMode:        OpenAIAuthModeAPIKey,
		}
		if definition.AccountAuth {
			settings.AuthMode = strings.ToLower(env.str(prefix+"_AUTH_MODE", OpenAIAuthModeAPIKey))
			settings.ChatGPTAuthFile = env.str(prefix+"_CHATGPT_AUTH_FILE", "")
		}
		if definition.ResponseControls {
			settings.ReasoningEffort = strings.ToLower(env.str(prefix+"_REASONING_EFFORT", ""))
			settings.TextVerbosity = strings.ToLower(env.str(prefix+"_TEXT_VERBOSITY", ""))
		}
		if definition.SelectableFormat {
			settings.APIFormat = strings.ToLower(env.str(prefix+"_API_FORMAT", definition.Format))
		}
	}

	if err := env.err(); err != nil {
		return Config{}, err
	}
	cfg.migrateLegacyCompatibleEnvironment()
	cfg.environment = &environmentBaseline{config: cfg, explicit: env.set}
	if err := cfg.applyRuntimeSettingsFile(); err != nil {
		return Config{}, err
	}
	cfg.OpenAI.ChatGPTAuthFile = resolveOpenAIAuthPath(cfg.OpenAI.ChatGPTAuthFile, cfg.SQLitePath)
	cfg.ProviderAuthFile = resolveProviderAuthPath(cfg.ProviderAuthFile, cfg.SQLitePath)
	cfg.AdminAuthFile = resolveAdminAuthPath(cfg.AdminAuthFile, cfg.SQLitePath)

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
	for _, definition := range ProviderDefinitions() {
		settings := definition.Settings(c)
		if strings.TrimSpace(settings.APIKeyFile) == "" {
			continue
		}
		secret, err := ReadSecretFile(settings.APIKeyFile)
		if err != nil {
			return fmt.Errorf("%s_API_KEY_FILE: %w", definition.EnvPrefix, err)
		}
		settings.APIKey = secret
	}
	return nil
}

// Validate checks the settings the selected provider needs. Settings for the
// other provider are left alone.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("configuration is required")
	}
	if err := c.Codex.Validate(); err != nil {
		return err
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

	definition, ok := LookupProvider(c.Provider)
	if !ok {
		return fmt.Errorf("AI_PROVIDER must be one of %s", ProviderIDs())
	}
	settings := definition.Settings(c)
	prefix := definition.EnvPrefix
	if err := validateProvider(prefix, settings.Model, settings.MaxOutputTokens, settings.Timeout); err != nil {
		return err
	}
	if definition.AccountAuth && isCustomOpenAIBaseURL(settings.BaseURL) {
		return errors.New("OPENAI_BASE_URL is a compatible endpoint; use AI_PROVIDER=openai-compatible and OPENAI_COMPATIBLE_BASE_URL")
	}
	if definition.RequireBaseURL && strings.TrimSpace(settings.BaseURL) == "" {
		return fmt.Errorf("%s_BASE_URL must be set explicitly", prefix)
	}
	if definition.ResponseControls {
		if settings.ReasoningEffort != "" && !oneOf(settings.ReasoningEffort, "none", "low", "medium", "high", "xhigh", "max") {
			return fmt.Errorf("%s_REASONING_EFFORT must be one of none, low, medium, high, xhigh, max", prefix)
		}
		if settings.TextVerbosity != "" && !oneOf(settings.TextVerbosity, "low", "medium", "high") {
			return fmt.Errorf("%s_TEXT_VERBOSITY must be one of low, medium, high", prefix)
		}
	}
	if definition.SelectableFormat && !oneOf(definition.WireFormat(settings), FormatResponses, FormatChatCompletions) {
		return fmt.Errorf("%s_API_FORMAT must be one of %s, %s", prefix, FormatResponses, FormatChatCompletions)
	}
	if settings.AuthMode == "" || !definition.AccountAuth {
		settings.AuthMode = OpenAIAuthModeAPIKey
	}
	if definition.AccountAuth && !oneOf(settings.AuthMode, OpenAIAuthModeAPIKey, OpenAIAuthModeChatGPT) {
		return fmt.Errorf("%s_AUTH_MODE must be one of api_key, chatgpt", prefix)
	}
	return nil
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
	if timeout < time.Second || timeout > MaxProviderTimeoutSeconds*time.Second {
		return fmt.Errorf("%s_TIMEOUT_SECONDS must be between 1 and %d: Alexa abandons a response after 8 seconds", prefix, MaxProviderTimeoutSeconds)
	}
	return nil
}

// SelectedModel reports the model the selected provider will use.
func (c *Config) SelectedModel() string {
	if settings := c.ProviderSettings(c.Provider); settings != nil {
		return settings.Model
	}
	return ""
}

// SelectedMaxOutputTokens reports the output cap for the selected provider.
func (c *Config) SelectedMaxOutputTokens() int {
	if settings := c.ProviderSettings(c.Provider); settings != nil {
		return settings.MaxOutputTokens
	}
	return 0
}

// SecretValues returns every credential in the configuration, for redaction
// before anything is logged or stored. Blank entries are harmless: redaction
// ignores short values.
func (c *Config) SecretValues() []string {
	secrets := make([]string, 0, len(providerDefinitions))
	for _, definition := range ProviderDefinitions() {
		secrets = append(secrets, definition.Settings(c).APIKey)
	}
	return secrets
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
// that Load can report all of them at once. It also records which variables
// were set, so a dashboard value that replaces one can be reported.
type envReader struct {
	errs []error
	set  map[string]bool
}

// lookup returns the trimmed value of key and records a nonempty one.
func (r *envReader) lookup(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value != "" {
		if r.set == nil {
			r.set = make(map[string]bool)
		}
		r.set[key] = true
	}
	return value
}

func (r *envReader) str(key, fallback string) string {
	value := r.lookup(key)
	if value == "" {
		return fallback
	}
	return value
}

func (r *envReader) intVal(key string, fallback int) int {
	raw := r.lookup(key)
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
	raw := r.lookup(key)
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

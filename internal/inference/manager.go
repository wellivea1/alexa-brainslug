// Package inference manages the active model provider for the running
// gateway. It persists non-secret settings separately from provider
// credentials and never exposes keys through its snapshots.
package inference

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/app"
	"github.com/wellivea1/alexa-brainslug/internal/config"
	"github.com/wellivea1/alexa-brainslug/internal/providers"
)

const (
	openAIDefaultBaseURL    = "https://api.openai.com/v1"
	anthropicDefaultBaseURL = "https://api.anthropic.com/v1"
	maxManagedTimeout       = 60
	maxManagedOutputTokens  = 128000
	maxManagedAPIKeyBytes   = 16384
)

// APIKeyStore is the narrow credential persistence boundary used by the
// inference manager. Implementations must never log or expose returned keys.
type APIKeyStore interface {
	Get(provider string) (string, bool)
	Set(provider, key string) error
	Remove(provider string) error
}

type apiKeyFallback struct {
	key  string
	file string
}

// Manager owns the effective inference configuration and atomically swaps the
// application provider after a valid dashboard update.
type Manager struct {
	mu          sync.RWMutex
	cfg         config.Config
	application *app.Service
	chatGPT     providers.ChatGPTCredentialSource
	apiKeys     APIKeyStore
	managedKeys map[string]bool
	fallbacks   map[string]apiKeyFallback
}

// Update is one operator-requested provider configuration.
type Update struct {
	Provider        string
	AuthMode        string
	Model           string
	BaseURL         string
	MaxOutputTokens int
	TimeoutSeconds  int
	ReasoningEffort string
	TextVerbosity   string
}

// Snapshot is a secret-free view suitable for health responses and the local
// dashboard.
type Snapshot struct {
	ActiveProvider string
	SettingsPath   string
	OpenAI         ProviderState
	Compatible     ProviderState
	Anthropic      ProviderState
}

// ProviderState describes one configured provider without exposing its key.
type ProviderState struct {
	ID                   string
	Name                 string
	Active               bool
	Model                string
	BaseURL              string
	EffectiveBaseURL     string
	EndpointKind         string
	MaxOutputTokens      int
	TimeoutSeconds       int
	CredentialConfigured bool
	CredentialSource     string
	AuthMode             string
	APIKeyConfigured     bool
	APIKeySource         string
	ChatGPTConnected     bool
	ManagedAPIKey        bool
	ReasoningEffort      string
	TextVerbosity        string
}

// NewManager builds a runtime manager around an already-configured service.
func NewManager(cfg *config.Config, application *app.Service, chatGPT ...providers.ChatGPTCredentialSource) (*Manager, error) {
	if application == nil {
		return nil, errors.New("application service is required")
	}
	if cfg == nil {
		return nil, errors.New("configuration is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var credentialSource providers.ChatGPTCredentialSource
	if len(chatGPT) > 0 {
		credentialSource = chatGPT[0]
	}
	return &Manager{
		cfg:         *cfg,
		application: application,
		chatGPT:     credentialSource,
		managedKeys: make(map[string]bool),
		fallbacks:   make(map[string]apiKeyFallback),
	}, nil
}

// AttachAPIKeyStore overlays locally saved provider keys on the environment
// configuration and activates the effective provider before listeners start.
// The original environment or *_FILE values remain available as fallbacks if
// a managed key is removed later.
func (m *Manager) AttachAPIKeyStore(store APIKeyStore) error {
	if store == nil {
		return errors.New("provider credential store is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	candidate := m.cfg
	fallbacks := map[string]apiKeyFallback{
		config.ProviderOpenAI:           {key: candidate.OpenAI.APIKey, file: candidate.OpenAI.APIKeyFile},
		config.ProviderOpenAICompatible: {key: candidate.Compatible.APIKey, file: candidate.Compatible.APIKeyFile},
		config.ProviderAnthropic:        {key: candidate.Anthropic.APIKey, file: candidate.Anthropic.APIKeyFile},
	}
	managed := make(map[string]bool, 2)
	if key, ok := store.Get(config.ProviderOpenAI); ok {
		if err := validateManagedAPIKey(key); err != nil {
			return fmt.Errorf("stored OpenAI credential: %w", err)
		}
		candidate.OpenAI.APIKey = strings.TrimSpace(key)
		managed[config.ProviderOpenAI] = true
	}
	if key, ok := store.Get(config.ProviderOpenAICompatible); ok {
		if err := validateManagedAPIKey(key); err != nil {
			return fmt.Errorf("stored OpenAI-compatible credential: %w", err)
		}
		candidate.Compatible.APIKey = strings.TrimSpace(key)
		managed[config.ProviderOpenAICompatible] = true
	}
	if key, ok := store.Get(config.ProviderAnthropic); ok {
		if err := validateManagedAPIKey(key); err != nil {
			return fmt.Errorf("stored Anthropic credential: %w", err)
		}
		candidate.Anthropic.APIKey = strings.TrimSpace(key)
		managed[config.ProviderAnthropic] = true
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	if len(managed) > 0 {
		if err := m.reconfigureLocked(&candidate); err != nil {
			return err
		}
	}
	m.cfg = candidate
	m.apiKeys = store
	m.managedKeys = managed
	m.fallbacks = fallbacks
	return nil
}

// Snapshot returns the current secret-free provider state.
func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshotFromConfig()
}

// Update validates, persists, and activates one provider configuration. The
// new provider is constructed before any state changes, and the settings file
// is written before the live service is swapped.
func (m *Manager) Update(update Update) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	candidate := m.cfg
	providerID := strings.ToLower(strings.TrimSpace(update.Provider))
	model := strings.TrimSpace(update.Model)
	baseURL := strings.TrimSpace(update.BaseURL)
	if !supportedProvider(providerID) {
		return errors.New("provider must be OpenAI, OpenAI-compatible, or Anthropic")
	}
	if model == "" {
		return errors.New("model is required")
	}
	if err := validateBaseURL(baseURL); err != nil {
		return err
	}
	if update.MaxOutputTokens < 16 || update.MaxOutputTokens > maxManagedOutputTokens {
		return fmt.Errorf("maximum output tokens must be between 16 and %d", maxManagedOutputTokens)
	}
	if update.TimeoutSeconds < 1 || update.TimeoutSeconds > maxManagedTimeout {
		return fmt.Errorf("timeout must be between 1 and %d seconds", maxManagedTimeout)
	}

	candidate.Provider = providerID
	switch providerID {
	case config.ProviderOpenAI:
		authMode := strings.ToLower(strings.TrimSpace(update.AuthMode))
		if authMode == "" {
			authMode = candidate.OpenAI.AuthMode
		}
		if authMode != config.OpenAIAuthModeAPIKey && authMode != config.OpenAIAuthModeChatGPT {
			return errors.New("OpenAI authentication must be API key or ChatGPT account")
		}
		candidate.OpenAI.AuthMode = authMode
		candidate.OpenAI.Model = model
		candidate.OpenAI.BaseURL = baseURL
		candidate.OpenAI.MaxOutputTokens = update.MaxOutputTokens
		candidate.OpenAI.Timeout = time.Duration(update.TimeoutSeconds) * time.Second
		candidate.OpenAI.ReasoningEffort = strings.ToLower(strings.TrimSpace(update.ReasoningEffort))
		candidate.OpenAI.TextVerbosity = strings.ToLower(strings.TrimSpace(update.TextVerbosity))
		if authMode == config.OpenAIAuthModeAPIKey && strings.TrimSpace(candidate.OpenAI.APIKey) == "" {
			return errors.New("OpenAI credential is not configured; set OPENAI_API_KEY or OPENAI_API_KEY_FILE")
		}
		if authMode == config.OpenAIAuthModeChatGPT && (m.chatGPT == nil || !m.chatGPT.Connected()) {
			return errors.New("ChatGPT account is not connected; connect it before selecting this authentication method")
		}
	case config.ProviderOpenAICompatible:
		candidate.Compatible.AuthMode = config.OpenAIAuthModeAPIKey
		candidate.Compatible.Model = model
		candidate.Compatible.BaseURL = baseURL
		candidate.Compatible.MaxOutputTokens = update.MaxOutputTokens
		candidate.Compatible.Timeout = time.Duration(update.TimeoutSeconds) * time.Second
		candidate.Compatible.ReasoningEffort = strings.ToLower(strings.TrimSpace(update.ReasoningEffort))
		candidate.Compatible.TextVerbosity = strings.ToLower(strings.TrimSpace(update.TextVerbosity))
		if baseURL == "" {
			return errors.New("OpenAI-compatible base URL is required")
		}
		if strings.TrimSpace(candidate.Compatible.APIKey) == "" {
			return errors.New("OpenAI-compatible credential is not configured; save an API key here or set OPENAI_COMPATIBLE_API_KEY or OPENAI_COMPATIBLE_API_KEY_FILE")
		}
	case config.ProviderAnthropic:
		candidate.Anthropic.Model = model
		candidate.Anthropic.BaseURL = baseURL
		candidate.Anthropic.MaxOutputTokens = update.MaxOutputTokens
		candidate.Anthropic.Timeout = time.Duration(update.TimeoutSeconds) * time.Second
		if strings.TrimSpace(candidate.Anthropic.APIKey) == "" {
			return errors.New("anthropic credential is not configured; save an API key here or set ANTHROPIC_API_KEY or ANTHROPIC_API_KEY_FILE")
		}
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	provider, err := providers.NewConfigured(&candidate, nil, m.chatGPT)
	if err != nil {
		return err
	}
	if err := config.SaveRuntimeSettings(candidate.RuntimeSettingsPath, config.RuntimeSettingsFromConfig(&candidate)); err != nil {
		return err
	}
	if err := m.application.Reconfigure(app.Options{
		Model:           candidate.SelectedModel(),
		MaxOutputTokens: candidate.SelectedMaxOutputTokens(),
		SaveHistory:     candidate.SaveHistory,
		Secrets:         candidate.SecretValues(),
	}, providers.AsAppProvider(provider)); err != nil {
		return err
	}
	m.cfg = candidate
	return nil
}

// SetAPIKey saves and immediately applies a dashboard-managed provider API
// key. Saving a key does not implicitly switch the active provider or OpenAI
// authentication mode.
func (m *Manager) SetAPIKey(providerID, key string) error {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	key = strings.TrimSpace(key)
	if !supportedProvider(providerID) {
		return errors.New("provider must be OpenAI, OpenAI-compatible, or Anthropic")
	}
	if err := validateManagedAPIKey(key); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.apiKeys == nil {
		return errors.New("provider credential storage is unavailable")
	}
	candidate := m.cfg
	switch providerID {
	case config.ProviderOpenAI:
		candidate.OpenAI.APIKey = key
	case config.ProviderOpenAICompatible:
		candidate.Compatible.APIKey = key
	case config.ProviderAnthropic:
		candidate.Anthropic.APIKey = key
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	provider, err := providers.NewConfigured(&candidate, nil, m.chatGPT)
	if err != nil {
		return err
	}
	previous, hadPrevious := m.apiKeys.Get(providerID)
	if err := m.apiKeys.Set(providerID, key); err != nil {
		return err
	}
	if err := m.application.Reconfigure(app.Options{
		Model:           candidate.SelectedModel(),
		MaxOutputTokens: candidate.SelectedMaxOutputTokens(),
		SaveHistory:     candidate.SaveHistory,
		Secrets:         candidate.SecretValues(),
	}, providers.AsAppProvider(provider)); err != nil {
		return errors.Join(err, m.restoreStoredKey(providerID, previous, hadPrevious))
	}
	m.cfg = candidate
	m.managedKeys[providerID] = true
	return nil
}

// ClearAPIKey removes only a dashboard-managed key. Any environment or
// explicit secret-file value present at startup becomes effective again.
func (m *Manager) ClearAPIKey(providerID string) error {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if !supportedProvider(providerID) {
		return errors.New("provider must be OpenAI, OpenAI-compatible, or Anthropic")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.apiKeys == nil {
		return errors.New("provider credential storage is unavailable")
	}
	if !m.managedKeys[providerID] {
		return errors.New("this provider does not have a dashboard-managed API key")
	}
	previous, _ := m.apiKeys.Get(providerID)
	fallback := m.fallbacks[providerID]
	candidate := m.cfg
	switch providerID {
	case config.ProviderOpenAI:
		candidate.OpenAI.APIKey = fallback.key
		candidate.OpenAI.APIKeyFile = fallback.file
	case config.ProviderOpenAICompatible:
		candidate.Compatible.APIKey = fallback.key
		candidate.Compatible.APIKeyFile = fallback.file
	case config.ProviderAnthropic:
		candidate.Anthropic.APIKey = fallback.key
		candidate.Anthropic.APIKeyFile = fallback.file
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	provider, err := providers.NewConfigured(&candidate, nil, m.chatGPT)
	if err != nil {
		return err
	}
	if err := m.apiKeys.Remove(providerID); err != nil {
		return err
	}
	if err := m.application.Reconfigure(app.Options{
		Model:           candidate.SelectedModel(),
		MaxOutputTokens: candidate.SelectedMaxOutputTokens(),
		SaveHistory:     candidate.SaveHistory,
		Secrets:         candidate.SecretValues(),
	}, providers.AsAppProvider(provider)); err != nil {
		return errors.Join(err, m.apiKeys.Set(providerID, previous))
	}
	m.cfg = candidate
	delete(m.managedKeys, providerID)
	return nil
}

// SetOpenAIAuthMode switches only the OpenAI credential path after a login or
// disconnect. Existing model settings and the selected provider are retained.
func (m *Manager) SetOpenAIAuthMode(authMode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	authMode = strings.ToLower(strings.TrimSpace(authMode))
	if authMode != config.OpenAIAuthModeAPIKey && authMode != config.OpenAIAuthModeChatGPT {
		return errors.New("OpenAI authentication must be API key or ChatGPT account")
	}
	if authMode == config.OpenAIAuthModeChatGPT && (m.chatGPT == nil || !m.chatGPT.Connected()) {
		return errors.New("ChatGPT account is not connected")
	}
	candidate := m.cfg
	candidate.OpenAI.AuthMode = authMode
	if err := candidate.Validate(); err != nil {
		return err
	}
	if err := config.SaveRuntimeSettings(candidate.RuntimeSettingsPath, config.RuntimeSettingsFromConfig(&candidate)); err != nil {
		return err
	}
	if candidate.Provider == config.ProviderOpenAI {
		provider, err := providers.NewConfigured(&candidate, nil, m.chatGPT)
		if err != nil {
			return err
		}
		if err := m.application.Reconfigure(app.Options{
			Model:           candidate.SelectedModel(),
			MaxOutputTokens: candidate.SelectedMaxOutputTokens(),
			SaveHistory:     candidate.SaveHistory,
			Secrets:         candidate.SecretValues(),
		}, providers.AsAppProvider(provider)); err != nil {
			return err
		}
	}
	m.cfg = candidate
	return nil
}

func (m *Manager) snapshotFromConfig() Snapshot {
	cfg := &m.cfg
	chatGPTConnected := m.chatGPT != nil && m.chatGPT.Connected()
	openAIAuthMode := cfg.OpenAI.AuthMode
	if openAIAuthMode == "" {
		openAIAuthMode = config.OpenAIAuthModeAPIKey
	}
	openAICredentialConfigured := strings.TrimSpace(cfg.OpenAI.APIKey) != ""
	openAICredentialSource := m.credentialSource(config.ProviderOpenAI, cfg.OpenAI.APIKey, cfg.OpenAI.APIKeyFile)
	openAIEndpoint := "Official OpenAI Responses API"
	effectiveOpenAIEndpoint := openAIDefaultBaseURL
	if openAIAuthMode == config.OpenAIAuthModeChatGPT {
		openAICredentialConfigured = chatGPTConnected
		if chatGPTConnected {
			openAICredentialSource = "ChatGPT account" //nolint:gosec // G101: operator-facing source label, not a credential.
		} else {
			openAICredentialSource = "Not connected"
		}
		openAIEndpoint = "ChatGPT Codex Responses"
		effectiveOpenAIEndpoint = "https://chatgpt.com/backend-api/codex/responses"
	}
	return Snapshot{
		ActiveProvider: cfg.Provider,
		SettingsPath:   cfg.RuntimeSettingsPath,
		OpenAI: ProviderState{
			ID:                   config.ProviderOpenAI,
			Name:                 "OpenAI",
			Active:               cfg.Provider == config.ProviderOpenAI,
			Model:                cfg.OpenAI.Model,
			BaseURL:              cfg.OpenAI.BaseURL,
			EffectiveBaseURL:     effectiveOpenAIEndpoint,
			EndpointKind:         openAIEndpoint,
			MaxOutputTokens:      cfg.OpenAI.MaxOutputTokens,
			TimeoutSeconds:       durationSeconds(cfg.OpenAI.Timeout),
			CredentialConfigured: openAICredentialConfigured,
			CredentialSource:     openAICredentialSource,
			AuthMode:             openAIAuthMode,
			APIKeyConfigured:     strings.TrimSpace(cfg.OpenAI.APIKey) != "",
			APIKeySource:         m.credentialSource(config.ProviderOpenAI, cfg.OpenAI.APIKey, cfg.OpenAI.APIKeyFile),
			ChatGPTConnected:     chatGPTConnected,
			ManagedAPIKey:        m.managedKeys[config.ProviderOpenAI],
			ReasoningEffort:      cfg.OpenAI.ReasoningEffort,
			TextVerbosity:        cfg.OpenAI.TextVerbosity,
		},
		Compatible: ProviderState{
			ID:                   config.ProviderOpenAICompatible,
			Name:                 "OpenAI-compatible",
			Active:               cfg.Provider == config.ProviderOpenAICompatible,
			Model:                cfg.Compatible.Model,
			BaseURL:              cfg.Compatible.BaseURL,
			EffectiveBaseURL:     cfg.Compatible.BaseURL,
			EndpointKind:         "Compatible Responses API",
			MaxOutputTokens:      cfg.Compatible.MaxOutputTokens,
			TimeoutSeconds:       durationSeconds(cfg.Compatible.Timeout),
			CredentialConfigured: strings.TrimSpace(cfg.Compatible.APIKey) != "",
			CredentialSource:     m.credentialSource(config.ProviderOpenAICompatible, cfg.Compatible.APIKey, cfg.Compatible.APIKeyFile),
			AuthMode:             config.OpenAIAuthModeAPIKey,
			APIKeyConfigured:     strings.TrimSpace(cfg.Compatible.APIKey) != "",
			APIKeySource:         m.credentialSource(config.ProviderOpenAICompatible, cfg.Compatible.APIKey, cfg.Compatible.APIKeyFile),
			ManagedAPIKey:        m.managedKeys[config.ProviderOpenAICompatible],
			ReasoningEffort:      cfg.Compatible.ReasoningEffort,
			TextVerbosity:        cfg.Compatible.TextVerbosity,
		},
		Anthropic: ProviderState{
			ID:                   config.ProviderAnthropic,
			Name:                 "Anthropic",
			Active:               cfg.Provider == config.ProviderAnthropic,
			Model:                cfg.Anthropic.Model,
			BaseURL:              cfg.Anthropic.BaseURL,
			EffectiveBaseURL:     firstNonEmpty(cfg.Anthropic.BaseURL, anthropicDefaultBaseURL),
			EndpointKind:         "Anthropic Messages API",
			MaxOutputTokens:      cfg.Anthropic.MaxOutputTokens,
			TimeoutSeconds:       durationSeconds(cfg.Anthropic.Timeout),
			CredentialConfigured: strings.TrimSpace(cfg.Anthropic.APIKey) != "",
			CredentialSource:     m.credentialSource(config.ProviderAnthropic, cfg.Anthropic.APIKey, cfg.Anthropic.APIKeyFile),
			ManagedAPIKey:        m.managedKeys[config.ProviderAnthropic],
		},
	}
}

func (m *Manager) reconfigureLocked(candidate *config.Config) error {
	provider, err := providers.NewConfigured(candidate, nil, m.chatGPT)
	if err != nil {
		return err
	}
	return m.application.Reconfigure(app.Options{
		Model:           candidate.SelectedModel(),
		MaxOutputTokens: candidate.SelectedMaxOutputTokens(),
		SaveHistory:     candidate.SaveHistory,
		Secrets:         candidate.SecretValues(),
	}, providers.AsAppProvider(provider))
}

func (m *Manager) restoreStoredKey(providerID, previous string, existed bool) error {
	if existed {
		return m.apiKeys.Set(providerID, previous)
	}
	return m.apiKeys.Remove(providerID)
}

func (m *Manager) credentialSource(providerID, apiKey, apiKeyFile string) string {
	if m.managedKeys[providerID] {
		return "Saved locally"
	}
	return credentialSource(apiKey, apiKeyFile)
}

func validateManagedAPIKey(key string) error {
	length := len(strings.TrimSpace(key))
	if length == 0 {
		return errors.New("API key is required")
	}
	if length > maxManagedAPIKeyBytes {
		return fmt.Errorf("API key must be at most %d bytes", maxManagedAPIKeyBytes)
	}
	return nil
}

func validateBaseURL(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("base URL must be an absolute http or https URL")
	}
	if parsed.User != nil {
		return errors.New("base URL must not contain credentials")
	}
	return nil
}

func supportedProvider(providerID string) bool {
	return providerID == config.ProviderOpenAI || providerID == config.ProviderOpenAICompatible || providerID == config.ProviderAnthropic
}

func credentialSource(apiKey, apiKeyFile string) string {
	if strings.TrimSpace(apiKeyFile) != "" {
		return "Secret file"
	}
	if strings.TrimSpace(apiKey) != "" {
		return "Environment"
	}
	return "Not configured"
}

func durationSeconds(value time.Duration) int {
	if value <= 0 {
		return 0
	}
	return int(value / time.Second)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

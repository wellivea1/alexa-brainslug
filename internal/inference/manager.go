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
	maxManagedOutputTokens = 128000
	maxManagedAPIKeyBytes  = 16384
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
	APIFormat       string
}

// Snapshot is a secret-free view suitable for health responses and the local
// dashboard.
type Snapshot struct {
	ActiveProvider string
	SettingsPath   string
	Providers      []ProviderState

	// Overrides lists environment variables that dashboard settings replace,
	// so an operator editing .env can see why a change had no effect.
	Overrides []config.Override
}

// ProviderState describes one configured provider without exposing its key.
type ProviderState struct {
	Definition           *config.ProviderDefinition
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
	APIFormat            string
}

// Provider returns a route's secret-free state, or an empty state if absent.
func (s Snapshot) Provider(id string) ProviderState {
	for i := range s.Providers {
		if s.Providers[i].ID == id {
			return s.Providers[i]
		}
	}
	return ProviderState{}
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
	fallbacks := make(map[string]apiKeyFallback)
	managed := make(map[string]bool)
	for _, definition := range config.ProviderDefinitions() {
		settings := definition.Settings(&candidate)
		fallbacks[definition.ID] = apiKeyFallback{key: settings.APIKey, file: settings.APIKeyFile}
		if key, ok := store.Get(definition.ID); ok {
			if err := validateManagedAPIKey(key); err != nil {
				return fmt.Errorf("stored %s credential: %w", definition.Name, err)
			}
			settings.APIKey = strings.TrimSpace(key)
			managed[definition.ID] = true
		}
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

// SetCodex persists gateway control settings through the same serialized writer
// as provider settings, so one dashboard cannot overwrite the other's choices.
func (m *Manager) SetCodex(settings config.CodexConfig) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	candidate := m.cfg
	candidate.Codex = settings
	if err := config.SaveRuntimeSettings(candidate.RuntimeSettingsPath, config.RuntimeSettingsFromConfig(&candidate)); err != nil {
		return err
	}
	m.cfg = candidate
	return nil
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
		return fmt.Errorf("provider must be one of %s", config.ProviderIDs())
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
	if update.TimeoutSeconds < 1 || update.TimeoutSeconds > config.MaxProviderTimeoutSeconds {
		return fmt.Errorf("timeout must be between 1 and %d seconds: Alexa abandons a response after 8 seconds", config.MaxProviderTimeoutSeconds)
	}

	candidate.Provider = providerID
	definition, _ := config.LookupProvider(providerID)
	settings := definition.Settings(&candidate)
	settings.Model = model
	settings.BaseURL = baseURL
	settings.MaxOutputTokens = update.MaxOutputTokens
	settings.Timeout = time.Duration(update.TimeoutSeconds) * time.Second
	if definition.ResponseControls {
		settings.ReasoningEffort = strings.ToLower(strings.TrimSpace(update.ReasoningEffort))
		settings.TextVerbosity = strings.ToLower(strings.TrimSpace(update.TextVerbosity))
	}
	if definition.SelectableFormat && strings.TrimSpace(update.APIFormat) != "" {
		settings.APIFormat = strings.ToLower(strings.TrimSpace(update.APIFormat))
	}
	if definition.AccountAuth {
		authMode := strings.ToLower(strings.TrimSpace(update.AuthMode))
		if authMode == "" {
			authMode = settings.AuthMode
		}
		if authMode != config.OpenAIAuthModeAPIKey && authMode != config.OpenAIAuthModeChatGPT {
			return errors.New("OpenAI authentication must be API key or ChatGPT account")
		}
		settings.AuthMode = authMode
		if authMode == config.OpenAIAuthModeChatGPT && (m.chatGPT == nil || !m.chatGPT.Connected()) {
			return errors.New("ChatGPT account is not connected; connect it before selecting this authentication method")
		}
	} else {
		settings.AuthMode = config.OpenAIAuthModeAPIKey
	}
	if definition.RequireBaseURL && baseURL == "" {
		return fmt.Errorf("%s base URL is required", definition.Name)
	}
	if settings.AuthMode != config.OpenAIAuthModeChatGPT && strings.TrimSpace(settings.APIKey) == "" {
		return fmt.Errorf("%s credential is not configured; save an API key here or set %s_API_KEY or %s_API_KEY_FILE", definition.Name, definition.EnvPrefix, definition.EnvPrefix)
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
	if err := m.activateProviderLocked(&candidate, provider); err != nil {
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
		return fmt.Errorf("provider must be one of %s", config.ProviderIDs())
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
	candidate.ProviderSettings(providerID).APIKey = key

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
	if err := m.activateProviderLocked(&candidate, provider); err != nil {
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
		return fmt.Errorf("provider must be one of %s", config.ProviderIDs())
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
	settings := candidate.ProviderSettings(providerID)
	settings.APIKey = fallback.key
	settings.APIKeyFile = fallback.file

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
	if err := m.activateProviderLocked(&candidate, provider); err != nil {
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
		if err := m.activateProviderLocked(&candidate, provider); err != nil {
			return err
		}
	}
	m.cfg = candidate
	return nil
}

func (m *Manager) snapshotFromConfig() Snapshot {
	cfg := &m.cfg
	snapshot := Snapshot{ActiveProvider: cfg.Provider, SettingsPath: cfg.RuntimeSettingsPath, Overrides: cfg.Overrides()}
	for _, definition := range config.ProviderDefinitions() {
		settings := definition.Settings(cfg)
		source := m.credentialSource(definition.ID, settings.APIKey, settings.APIKeyFile)
		state := ProviderState{
			Definition: definition, ID: definition.ID, Name: definition.Name, Active: cfg.Provider == definition.ID,
			Model: settings.Model, BaseURL: settings.BaseURL, EffectiveBaseURL: firstNonEmpty(settings.BaseURL, definition.DefaultBaseURL),
			EndpointKind: definition.EndpointKind, MaxOutputTokens: settings.MaxOutputTokens, TimeoutSeconds: durationSeconds(settings.Timeout),
			CredentialConfigured: strings.TrimSpace(settings.APIKey) != "", CredentialSource: source,
			APIKeyConfigured: strings.TrimSpace(settings.APIKey) != "", APIKeySource: source,
			AuthMode: firstNonEmpty(settings.AuthMode, config.OpenAIAuthModeAPIKey), ManagedAPIKey: m.managedKeys[definition.ID],
			ReasoningEffort: settings.ReasoningEffort, TextVerbosity: settings.TextVerbosity, APIFormat: definition.WireFormat(settings),
		}
		if definition.SelectableFormat && state.APIFormat == config.FormatChatCompletions {
			state.EndpointKind = "Compatible Chat Completions API"
		}
		if definition.AccountAuth {
			state.ChatGPTConnected = m.chatGPT != nil && m.chatGPT.Connected()
			if state.AuthMode == config.OpenAIAuthModeChatGPT {
				state.CredentialConfigured = state.ChatGPTConnected
				state.CredentialSource = "Not connected"
				if state.ChatGPTConnected {
					state.CredentialSource = "ChatGPT account"
				}
				state.EndpointKind = "ChatGPT Codex Responses"
				state.EffectiveBaseURL = "https://chatgpt.com/backend-api/codex/responses"
			}
		}
		snapshot.Providers = append(snapshot.Providers, state)
	}
	return snapshot
}

func (m *Manager) reconfigureLocked(candidate *config.Config) error {
	provider, err := providers.NewConfigured(candidate, nil, m.chatGPT)
	if err != nil {
		return err
	}
	return m.activateProviderLocked(candidate, provider)
}

func (m *Manager) activateProviderLocked(candidate *config.Config, provider providers.Provider) error {
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
	_, ok := config.LookupProvider(providerID)
	return ok
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

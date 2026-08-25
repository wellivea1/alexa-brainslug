// Package inference manages the active model provider for the running
// gateway. It persists only non-secret settings; credentials remain owned by
// environment variables or secret files.
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
)

// Manager owns the effective inference configuration and atomically swaps the
// application provider after a valid dashboard update.
type Manager struct {
	mu          sync.RWMutex
	cfg         config.Config
	application *app.Service
	chatGPT     providers.ChatGPTCredentialSource
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
	return &Manager{cfg: *cfg, application: application, chatGPT: credentialSource}, nil
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
	if providerID != config.ProviderOpenAI && providerID != config.ProviderAnthropic {
		return errors.New("provider must be OpenAI or Anthropic")
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
	case config.ProviderAnthropic:
		candidate.Anthropic.Model = model
		candidate.Anthropic.BaseURL = baseURL
		candidate.Anthropic.MaxOutputTokens = update.MaxOutputTokens
		candidate.Anthropic.Timeout = time.Duration(update.TimeoutSeconds) * time.Second
		if strings.TrimSpace(candidate.Anthropic.APIKey) == "" {
			return errors.New("anthropic credential is not configured; set ANTHROPIC_API_KEY or ANTHROPIC_API_KEY_FILE")
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
	openAICredentialSource := credentialSource(cfg.OpenAI.APIKey, cfg.OpenAI.APIKeyFile)
	openAIEndpoint := openAIEndpointKind(cfg.OpenAI.BaseURL)
	effectiveOpenAIEndpoint := firstNonEmpty(cfg.OpenAI.BaseURL, openAIDefaultBaseURL)
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
			Name:                 "OpenAI / compatible",
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
			APIKeySource:         credentialSource(cfg.OpenAI.APIKey, cfg.OpenAI.APIKeyFile),
			ChatGPTConnected:     chatGPTConnected,
			ReasoningEffort:      cfg.OpenAI.ReasoningEffort,
			TextVerbosity:        cfg.OpenAI.TextVerbosity,
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
			CredentialSource:     credentialSource(cfg.Anthropic.APIKey, cfg.Anthropic.APIKeyFile),
		},
	}
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

func openAIEndpointKind(baseURL string) string {
	baseURL = strings.TrimRight(strings.ToLower(strings.TrimSpace(baseURL)), "/")
	if baseURL == "" || baseURL == openAIDefaultBaseURL {
		return "Official OpenAI Responses API"
	}
	return "OpenAI-compatible Responses API"
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

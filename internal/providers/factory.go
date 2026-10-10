package providers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/wellivea1/alexa-brainslug/internal/config"
)

// NewConfigured builds the provider named by AI_PROVIDER.
//
// Passing a nil client lets each provider build its own with the configured
// timeout; tests pass a client pointed at a local test server.
func NewConfigured(cfg *config.Config, client *http.Client, chatGPT ...ChatGPTCredentialSource) (Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: configuration is required", ErrNotConfigured)
	}
	definition, ok := config.LookupProvider(cfg.Provider)
	if !ok {
		return nil, fmt.Errorf("%w: unsupported AI_PROVIDER %q", ErrNotConfigured, cfg.Provider)
	}
	var credentials ChatGPTCredentialSource
	if len(chatGPT) > 0 {
		credentials = chatGPT[0]
	}
	return newConfiguredProvider(definition, *definition.Settings(cfg), client, credentials)
}

func newConfiguredProvider(definition *config.ProviderDefinition, settings config.ProviderConfig, client *http.Client, credentials ChatGPTCredentialSource) (Provider, error) {
	if definition.AccountAuth && settings.AuthMode == config.OpenAIAuthModeChatGPT {
		return NewChatGPT(settings, credentials, client), nil
	}
	switch definition.WireFormat(&settings) {
	case config.FormatResponses:
		return newOpenAI(settings, definition.ID, definition.EnvPrefix+"_API_KEY", definition.EnvPrefix+"_MODEL", client), nil
	case config.FormatChatCompletions:
		return newChatCompletions(settings, definition, client), nil
	case config.FormatMessages:
		return NewAnthropic(settings, client), nil
	default:
		return nil, fmt.Errorf("%w: unsupported %s_API_FORMAT %q", ErrNotConfigured, definition.EnvPrefix, settings.APIFormat)
	}
}

// firstNonEmpty returns the first value that is not blank.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

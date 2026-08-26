package providers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/wellivea1/alexa-brainslug/internal/config"
)

// spokenWordLimit caps how many words Alexa speaks. It matches the limit the
// system prompt asks the model to respect, so truncation is a backstop for
// when the model overruns rather than the normal path.
const spokenWordLimit = 120

// NewConfigured builds the provider named by AI_PROVIDER.
//
// Passing a nil client lets each provider build its own with the configured
// timeout; tests pass a client pointed at a local test server.
func NewConfigured(cfg *config.Config, client *http.Client, chatGPT ...ChatGPTCredentialSource) (Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: configuration is required", ErrNotConfigured)
	}
	switch cfg.Provider {
	case config.ProviderOpenAI:
		if cfg.OpenAI.AuthMode == config.OpenAIAuthModeChatGPT {
			var credentials ChatGPTCredentialSource
			if len(chatGPT) > 0 {
				credentials = chatGPT[0]
			}
			return NewChatGPT(cfg.OpenAI, credentials, client), nil
		}
		return NewOpenAI(cfg.OpenAI, client), nil
	case config.ProviderOpenAICompatible:
		return NewOpenAICompatible(cfg.Compatible, client), nil
	case config.ProviderAnthropic:
		return NewAnthropic(cfg.Anthropic, client), nil
	default:
		return nil, fmt.Errorf("%w: unsupported AI_PROVIDER %q", ErrNotConfigured, cfg.Provider)
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

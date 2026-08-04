package providers

import (
	"fmt"
	"net/http"

	"alexa-brainslug/internal/config"
)

func NewConfigured(cfg config.Config, client *http.Client) (Provider, error) {
	switch cfg.Provider {
	case "openai":
		return NewOpenAI(cfg.OpenAI, client), nil
	case "anthropic":
		return NewAnthropic(cfg.Anthropic, client), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
}

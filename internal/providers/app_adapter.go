package providers

import (
	"context"
	"errors"
	"strings"

	"github.com/wellivea1/alexa-brainslug/internal/app"
)

// AsAppProvider adapts an existing provider client to the application port.
func AsAppProvider(provider Provider) app.Provider {
	if provider == nil {
		return nil
	}
	return &appProvider{provider: provider}
}

type appProvider struct {
	provider Provider
}

func (p *appProvider) Name() string {
	return p.provider.Name()
}

func (p *appProvider) Ask(ctx context.Context, req app.ProviderRequest) (app.ProviderResponse, error) {
	response, err := p.provider.Ask(ctx, Request{
		UserID:          providerPrincipalKey(req.Principal),
		Prompt:          req.Prompt,
		Model:           req.Model,
		MaxOutputTokens: req.MaxOutputTokens,
		SystemPrompt:    req.SystemPrompt,
	})
	if err != nil {
		return app.ProviderResponse{}, mapAppError(err)
	}
	return app.ProviderResponse{
		ShortAnswer: response.ShortAnswer,
		FullAnswer:  response.FullAnswer,
		Provider:    response.ProviderName,
		Model:       response.Model,
		Latency:     response.Latency,
	}, nil
}

// providerPrincipalKey gives provider-side user hashing a cross-platform input.
// Existing provider clients hash this value before placing it on the wire.
func providerPrincipalKey(principal app.Principal) string {
	return strings.TrimSpace(principal.Namespace) + ":" + strings.TrimSpace(principal.Subject)
}

func mapAppError(err error) error {
	switch {
	case errors.Is(err, ErrNotConfigured):
		return errors.Join(app.ErrProviderNotConfigured, err)
	case errors.Is(err, ErrEmptyResponse):
		return errors.Join(app.ErrProviderNoText, err)
	case errors.Is(err, context.DeadlineExceeded):
		return errors.Join(app.ErrProviderUnavailable, err)
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Retryable() {
		return errors.Join(app.ErrProviderUnavailable, err)
	}
	return errors.Join(app.ErrProviderFailed, err)
}

package providers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/app"
)

type adapterProvider struct {
	request  Request
	response Response
	err      error
}

func (p *adapterProvider) Name() string { return "adapter-fake" }

func (p *adapterProvider) Ask(_ context.Context, req Request) (Response, error) {
	p.request = req
	return p.response, p.err
}

func TestAppProviderMapsRequestAndResponse(t *testing.T) {
	underlying := &adapterProvider{response: Response{
		FullAnswer:   "full",
		ProviderName: "resolved-provider",
		Model:        "resolved-model",
		Latency:      20 * time.Millisecond,
	}}
	provider := AsAppProvider(underlying)

	response, err := provider.Ask(t.Context(), app.ProviderRequest{
		Principal:       app.Principal{Namespace: "alexa", Subject: "user-1"},
		Prompt:          "question",
		Model:           "requested-model",
		MaxOutputTokens: 512,
		SystemPrompt:    "system",
	})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if underlying.request.UserID != "alexa:user-1" || underlying.request.Prompt != "question" {
		t.Errorf("underlying request = %+v", underlying.request)
	}
	if underlying.request.Model != "requested-model" || underlying.request.MaxOutputTokens != 512 {
		t.Errorf("underlying request = %+v", underlying.request)
	}
	if response.Provider != "resolved-provider" || response.Model != "resolved-model" {
		t.Errorf("response = %+v", response)
	}
	if response.FullAnswer != "full" {
		t.Errorf("response = %+v", response)
	}
}

func TestProviderPrincipalKeySeparatesNamespaces(t *testing.T) {
	alexa := providerPrincipalKey(app.Principal{Namespace: "alexa", Subject: "user-1"})
	mcp := providerPrincipalKey(app.Principal{Namespace: "mcp", Subject: "user-1"})
	if alexa == mcp {
		t.Fatalf("provider principal keys collide: %q", alexa)
	}
}

func TestAppProviderMapsErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		want     error
		alsoWant error
	}{
		{"not configured", ErrNotConfigured, app.ErrProviderNotConfigured, nil},
		{"output limit", ErrOutputLimit, app.ErrProviderOutputLimit, app.ErrProviderNoText},
		{"empty response", ErrEmptyResponse, app.ErrProviderNoText, nil},
		{"deadline", context.DeadlineExceeded, app.ErrProviderUnavailable, nil},
		{"caller canceled", fmt.Errorf("call provider: %w", context.Canceled), app.ErrRequestCanceled, nil},
		{"incomplete", ErrIncompleteResponse, app.ErrProviderFailed, nil},
		{"failed response", ErrFailedResponse, app.ErrProviderFailed, nil},
		{"rate limit", &APIError{Provider: "fake", Status: 429}, app.ErrProviderUnavailable, nil},
		{"server error", &APIError{Provider: "fake", Status: 503}, app.ErrProviderUnavailable, nil},
		{"bad request", &APIError{Provider: "fake", Status: 400}, app.ErrProviderFailed, nil},
		{"unknown", errors.New("boom"), app.ErrProviderFailed, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := AsAppProvider(&adapterProvider{err: tt.err})
			_, err := provider.Ask(t.Context(), app.ProviderRequest{})
			if !errors.Is(err, tt.want) {
				t.Errorf("Ask() error = %v, want %v", err, tt.want)
			}
			if tt.alsoWant != nil && !errors.Is(err, tt.alsoWant) {
				t.Errorf("Ask() error = %v, also want %v", err, tt.alsoWant)
			}
		})
	}
}

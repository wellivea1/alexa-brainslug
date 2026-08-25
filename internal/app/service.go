// Package app implements Brainslug's platform-neutral question and history
// use cases.
//
// Inbound adapters translate their protocol into these types. Provider and
// persistence adapters implement the narrow interfaces below, so Alexa is one
// interface to the application rather than the owner of its behavior.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/prompts"
	"github.com/wellivea1/alexa-brainslug/internal/security"
)

var (
	// ErrEmptyPrompt means an inbound adapter submitted no question text.
	ErrEmptyPrompt = errors.New("prompt is empty")

	// ErrInvalidPrincipal means a caller could not be scoped safely.
	ErrInvalidPrincipal = errors.New("principal is invalid")

	// ErrNoHistory means no saved answer is available to the caller.
	ErrNoHistory = errors.New("no saved response")

	// ErrEmptySavedAnswer means a saved success contains no speakable text.
	ErrEmptySavedAnswer = errors.New("saved response is empty")

	// ErrRepositoryNotFound is returned by persistence adapters for no row.
	ErrRepositoryNotFound = errors.New("repository record not found")

	// ErrProviderNotConfigured means operator configuration is incomplete.
	ErrProviderNotConfigured = errors.New("provider is not configured")

	// ErrProviderUnavailable means retrying later may succeed.
	ErrProviderUnavailable = errors.New("provider is unavailable")

	// ErrProviderNoText means the provider returned no visible answer.
	ErrProviderNoText = errors.New("provider returned no text")

	// ErrProviderFailed is the stable category for other provider failures.
	ErrProviderFailed = errors.New("provider failed")
)

const (
	// StatusOK records a completed provider response.
	StatusOK = "ok"

	// StatusError records a provider failure.
	StatusError = "error"
)

// Principal identifies a caller without coupling the application to one
// platform's request type.
type Principal struct {
	Namespace string
	Subject   string
}

// Valid reports whether the principal has enough information for scoping.
func (p Principal) Valid() bool {
	return strings.TrimSpace(p.Namespace) != "" && strings.TrimSpace(p.Subject) != ""
}

// AskRequest is one normalized question submitted by an inbound adapter.
type AskRequest struct {
	Principal      Principal
	ConversationID string
	Prompt         string
}

// Answer is a completed provider result in transport-neutral form.
type Answer struct {
	ShortAnswer string
	FullAnswer  string
	Provider    string
	Model       string
	Latency     time.Duration
}

// ProviderRequest is the provider-independent input needed by the application.
type ProviderRequest struct {
	Principal       Principal
	Prompt          string
	Model           string
	MaxOutputTokens int
	SystemPrompt    string
}

// ProviderResponse is the provider-independent result used by the application.
type ProviderResponse struct {
	ShortAnswer string
	FullAnswer  string
	Provider    string
	Model       string
	Latency     time.Duration
}

// Provider is the outbound model capability the application requires.
type Provider interface {
	// Name reports the stable provider identifier stored with results.
	Name() string

	// Ask executes one provider request and respects the context deadline.
	Ask(ctx context.Context, req ProviderRequest) (ProviderResponse, error)
}

// Interaction is one persisted question and its outcome.
type Interaction struct {
	ID             int64
	CreatedAt      time.Time
	Principal      Principal
	ConversationID string
	Provider       string
	Model          string
	Prompt         string
	ShortAnswer    string
	FullAnswer     string
	Latency        time.Duration
	Status         string
	ErrorMessage   string
}

// Repository is the persistence capability the application requires.
type Repository interface {
	// SaveInteraction persists one completed or failed interaction.
	SaveInteraction(ctx context.Context, interaction Interaction) (int64, error)

	// GetLatestResponse returns the newest successful response for a principal.
	GetLatestResponse(ctx context.Context, principal Principal) (*Interaction, error)
}

// Options configures application policy rather than an inbound protocol.
type Options struct {
	// Model is the explicit model requested for every question.
	Model string

	// MaxOutputTokens is the provider output budget.
	MaxOutputTokens int

	// SaveHistory controls whether outcomes are persisted.
	SaveHistory bool

	// Secrets are removed from diagnostics before persistence.
	Secrets []string
}

// Service implements Brainslug's synchronous question and history use cases.
type Service struct {
	opts       Options
	provider   Provider
	repository Repository
}

// NewService builds the application service from explicit outbound adapters.
func NewService(opts Options, provider Provider, repository Repository) *Service {
	return &Service{opts: opts, provider: provider, repository: repository}
}

// Ask sends one question to the selected provider and records its outcome when
// history is enabled.
func (s *Service) Ask(ctx context.Context, req AskRequest) (Answer, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return Answer{}, ErrEmptyPrompt
	}
	if !req.Principal.Valid() {
		return Answer{}, ErrInvalidPrincipal
	}
	if s.provider == nil {
		return Answer{}, ErrProviderNotConfigured
	}

	started := time.Now()
	providerResp, err := s.provider.Ask(ctx, ProviderRequest{
		Principal:       req.Principal,
		Prompt:          prompt,
		Model:           s.opts.Model,
		MaxOutputTokens: s.opts.MaxOutputTokens,
		SystemPrompt:    prompts.DefaultSystemPrompt,
	})

	latency := providerResp.Latency
	if latency <= 0 {
		latency = time.Since(started)
	}

	if err != nil {
		err = normalizeProviderError(err)
		s.saveInteraction(ctx, Interaction{
			Principal:      req.Principal,
			ConversationID: req.ConversationID,
			Provider:       s.provider.Name(),
			Model:          s.opts.Model,
			Prompt:         prompt,
			Latency:        latency,
			Status:         StatusError,
			ErrorMessage:   security.RedactString(err.Error(), s.opts.Secrets...),
		})
		return Answer{}, err
	}

	answer := Answer{
		ShortAnswer: strings.TrimSpace(providerResp.ShortAnswer),
		FullAnswer:  strings.TrimSpace(providerResp.FullAnswer),
		Provider:    firstNonEmpty(providerResp.Provider, s.provider.Name()),
		Model:       firstNonEmpty(providerResp.Model, s.opts.Model),
		Latency:     latency,
	}

	s.saveInteraction(ctx, Interaction{
		Principal:      req.Principal,
		ConversationID: req.ConversationID,
		Provider:       answer.Provider,
		Model:          answer.Model,
		Prompt:         prompt,
		ShortAnswer:    answer.ShortAnswer,
		FullAnswer:     answer.FullAnswer,
		Latency:        answer.Latency,
		Status:         StatusOK,
	})

	return answer, nil
}

// LatestAnswer returns the most recent saved answer owned by principal.
func (s *Service) LatestAnswer(ctx context.Context, principal Principal) (Answer, error) {
	if !principal.Valid() || !s.opts.SaveHistory || s.repository == nil {
		return Answer{}, ErrNoHistory
	}

	latest, err := s.repository.GetLatestResponse(ctx, principal)
	if err != nil {
		if errors.Is(err, ErrRepositoryNotFound) {
			return Answer{}, ErrNoHistory
		}
		return Answer{}, fmt.Errorf("load latest response: %w", err)
	}

	shortAnswer := strings.TrimSpace(latest.ShortAnswer)
	fullAnswer := strings.TrimSpace(latest.FullAnswer)
	if shortAnswer == "" && fullAnswer == "" {
		return Answer{}, ErrEmptySavedAnswer
	}

	return Answer{
		ShortAnswer: shortAnswer,
		FullAnswer:  fullAnswer,
		Provider:    latest.Provider,
		Model:       latest.Model,
		Latency:     latest.Latency,
	}, nil
}

func (s *Service) saveInteraction(ctx context.Context, interaction Interaction) {
	if !s.opts.SaveHistory || s.repository == nil {
		return
	}
	if _, err := s.repository.SaveInteraction(ctx, interaction); err != nil {
		slog.WarnContext(ctx, "save interaction", "error", err)
	}
}

func normalizeProviderError(err error) error {
	switch {
	case errors.Is(err, ErrProviderNotConfigured),
		errors.Is(err, ErrProviderUnavailable),
		errors.Is(err, ErrProviderNoText),
		errors.Is(err, ErrProviderFailed):
		return err
	case errors.Is(err, context.DeadlineExceeded):
		return errors.Join(ErrProviderUnavailable, err)
	default:
		return errors.Join(ErrProviderFailed, err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

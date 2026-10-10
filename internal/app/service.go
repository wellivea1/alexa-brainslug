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
	"sync"
	"time"

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

	// ErrRequestCanceled means the caller went away before the provider
	// answered. It is recorded so history does not blame the provider.
	ErrRequestCanceled = errors.New("request was canceled by the caller")
)

// persistTimeout bounds saving one outcome. When the caller's context has a
// deadline, the provider call ends this much earlier, so a provider that uses
// all of its time still leaves room to record what happened.
const persistTimeout = time.Second

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
	SystemPrompt   string
}

// Answer is a completed provider result in transport-neutral form.
type Answer struct {
	FullAnswer string
	Provider   string
	Model      string
	Latency    time.Duration
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
	FullAnswer string
	Provider   string
	Model      string
	Latency    time.Duration
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
	// ShortAnswer is a legacy summary read from existing history. New
	// interactions store the full answer; inbound adapters shape their output.
	ShortAnswer  string
	FullAnswer   string
	Latency      time.Duration
	Status       string
	ErrorMessage string
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
	mu         sync.RWMutex
	opts       Options
	provider   Provider
	repository Repository
}

// NewService builds the application service from explicit outbound adapters.
func NewService(opts Options, provider Provider, repository Repository) *Service {
	return &Service{opts: opts, provider: provider, repository: repository}
}

// Reconfigure replaces inference policy for subsequent requests. In-flight
// requests keep the provider snapshot they started with, so a dashboard save
// never changes one request halfway through execution.
func (s *Service) Reconfigure(opts Options, provider Provider) error {
	if provider == nil {
		return ErrProviderNotConfigured
	}
	s.mu.Lock()
	s.opts = opts
	s.provider = provider
	s.mu.Unlock()
	return nil
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
	opts, provider := s.inferenceSnapshot()
	if provider == nil {
		return Answer{}, ErrProviderNotConfigured
	}

	providerCtx := ctx
	if deadline, ok := ctx.Deadline(); ok && opts.SaveHistory && s.repository != nil {
		var cancel context.CancelFunc
		providerCtx, cancel = context.WithDeadline(ctx, deadline.Add(-persistTimeout))
		defer cancel()
	}

	started := time.Now()
	providerResp, err := provider.Ask(providerCtx, ProviderRequest{
		Principal:       req.Principal,
		Prompt:          prompt,
		Model:           opts.Model,
		MaxOutputTokens: opts.MaxOutputTokens,
		SystemPrompt:    req.SystemPrompt,
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
			Provider:       provider.Name(),
			Model:          opts.Model,
			Prompt:         prompt,
			Latency:        latency,
			Status:         StatusError,
			ErrorMessage:   security.RedactString(err.Error(), opts.Secrets...),
		}, opts.SaveHistory)
		return Answer{}, err
	}

	answer := Answer{
		FullAnswer: strings.TrimSpace(providerResp.FullAnswer),
		Provider:   firstNonEmpty(providerResp.Provider, provider.Name()),
		Model:      firstNonEmpty(providerResp.Model, opts.Model),
		Latency:    latency,
	}

	s.saveInteraction(ctx, Interaction{
		Principal:      req.Principal,
		ConversationID: req.ConversationID,
		Provider:       answer.Provider,
		Model:          answer.Model,
		Prompt:         prompt,
		FullAnswer:     answer.FullAnswer,
		Latency:        answer.Latency,
		Status:         StatusOK,
	}, opts.SaveHistory)

	return answer, nil
}

// LatestAnswer returns the most recent saved answer owned by principal.
func (s *Service) LatestAnswer(ctx context.Context, principal Principal) (Answer, error) {
	opts, _ := s.inferenceSnapshot()
	if !principal.Valid() || !opts.SaveHistory || s.repository == nil {
		return Answer{}, ErrNoHistory
	}

	latest, err := s.repository.GetLatestResponse(ctx, principal)
	if err != nil {
		if errors.Is(err, ErrRepositoryNotFound) {
			return Answer{}, ErrNoHistory
		}
		return Answer{}, fmt.Errorf("load latest response: %w", err)
	}

	fullAnswer := strings.TrimSpace(firstNonEmpty(latest.FullAnswer, latest.ShortAnswer))
	if fullAnswer == "" {
		return Answer{}, ErrEmptySavedAnswer
	}

	return Answer{
		FullAnswer: fullAnswer,
		Provider:   latest.Provider,
		Model:      latest.Model,
		Latency:    latest.Latency,
	}, nil
}

// saveInteraction records an outcome even when the caller's context has been
// canceled or has expired: a request that ran out of time, or whose caller hung
// up, is exactly the one an operator needs to see in history.
func (s *Service) saveInteraction(ctx context.Context, interaction Interaction, saveHistory bool) {
	if !saveHistory || s.repository == nil {
		return
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), persistTimeout)
	defer cancel()
	if _, err := s.repository.SaveInteraction(saveCtx, interaction); err != nil {
		slog.WarnContext(ctx, "save interaction", "error", err)
	}
}

func (s *Service) inferenceSnapshot() (Options, Provider) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.opts, s.provider
}

func normalizeProviderError(err error) error {
	switch {
	case errors.Is(err, ErrProviderNotConfigured),
		errors.Is(err, ErrProviderUnavailable),
		errors.Is(err, ErrProviderNoText),
		errors.Is(err, ErrProviderFailed),
		errors.Is(err, ErrRequestCanceled):
		return err
	case errors.Is(err, context.DeadlineExceeded):
		return errors.Join(ErrProviderUnavailable, err)
	case errors.Is(err, context.Canceled):
		return errors.Join(ErrRequestCanceled, err)
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

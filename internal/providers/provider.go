// Package providers contains the AI backends the gateway can call.
//
// Each provider is a thin, explicit HTTP client. There is deliberately no
// vendor SDK: the gateway uses one endpoint per provider, and a hand-written
// client keeps the request shape visible and the dependency surface empty.
//
// Providers never decide policy. They do not retry, they do not fall back to
// another model, and they do not silently substitute a model the operator did
// not ask for. Model choice is configuration and stays that way.
package providers

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const providerRoleUser = "user"

// Provider is an AI backend that can answer a single question.
//
// Implementations must be safe for concurrent use and must respect the
// context deadline: the caller's deadline is the voice budget, and overrunning
// it means the user hears an error.
type Provider interface {
	// Name reports the provider identifier stored in history, for example
	// "openai". It must be stable across releases.
	Name() string

	// Ask sends a single question and returns the answer. It returns an error
	// wrapping one of the sentinel errors in this package where the cause is
	// known, and an *APIError when the provider itself rejected the request.
	Ask(ctx context.Context, req Request) (Response, error)
}

// Sentinel errors describing why a provider call could not produce an answer.
// Callers should match these with errors.Is rather than inspecting strings.
var (
	// ErrNotConfigured means a credential or model was not supplied. This is
	// an operator error, not a runtime failure, and retrying will not help.
	ErrNotConfigured = errors.New("provider is not configured")

	// ErrEmptyPrompt means there was nothing to ask.
	ErrEmptyPrompt = errors.New("prompt is empty")

	// ErrEmptyResponse means the provider returned a well-formed completed
	// response containing no visible text.
	ErrEmptyResponse = errors.New("provider returned no text output")

	// ErrOutputLimit means the configured output budget was exhausted. On
	// reasoning models this can happen before any visible answer is produced
	// because reasoning and visible output share the same budget.
	ErrOutputLimit = errors.New("provider output token limit was exhausted")

	// ErrIncompleteResponse means a synchronous provider call ended in a
	// nonterminal or otherwise incomplete state for a reason other than the
	// configured output-token limit.
	ErrIncompleteResponse = errors.New("provider response is incomplete")

	// ErrFailedResponse means the provider returned a terminal failed or
	// canceled response despite an otherwise successful HTTP request.
	ErrFailedResponse = errors.New("provider response failed")
)

// APIError is a non-2xx response from a provider.
//
// Message has already been passed through secret redaction and is safe to
// store in local history. It is never spoken to the user.
type APIError struct {
	Provider string
	Status   int
	Message  string
}

// Error implements error.
func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("%s API error: status %d", e.Provider, e.Status)
	}
	return fmt.Sprintf("%s API error: %s", e.Provider, e.Message)
}

// Retryable reports whether the same request might succeed if sent again.
// Rate limiting and server-side faults are worth another attempt; a rejected
// request is not.
func (e *APIError) Retryable() bool {
	return e.Status == 429 || e.Status >= 500
}

// Request is a single question for a provider.
type Request struct {
	// UserID is a namespaced platform identifier. It is hashed before being
	// sent to any provider that accepts an end-user identifier, never sent raw.
	UserID string

	// Prompt is the question normalized by the inbound adapter.
	Prompt string

	// Model overrides the provider's configured model when set. It exists so
	// a caller can pin a model for one request; it is not a fallback path.
	Model string

	// MaxOutputTokens caps the response. On reasoning models this budget also
	// covers reasoning tokens, so a value tuned for visible text alone can
	// yield ErrOutputLimit before the model writes an answer.
	MaxOutputTokens int

	// SystemPrompt is supplied by the inbound adapter. Empty means no
	// additional instructions; providers do not choose a presentation style.
	SystemPrompt string

	// PriorContext is earlier conversation turns, oldest first. Providers
	// that do not support multi-turn context ignore it.
	PriorContext []Message
}

// Message is one turn of prior conversation context.
type Message struct {
	// Role is "user" or "assistant". Other values are ignored.
	Role string `json:"role"`

	// Content is the turn's text.
	Content string `json:"content"`
}

// Response is a provider's answer.
type Response struct {
	// FullAnswer is the complete model output, stored locally.
	FullAnswer string

	// Model is the model that actually answered, as reported by the provider.
	// It can differ from the requested model when the provider resolves an
	// alias, which is why it is recorded rather than assumed.
	Model string

	// ProviderName matches Provider.Name.
	ProviderName string

	// Latency is the time spent in the provider HTTP call alone, excluding
	// local request construction and response shaping.
	Latency time.Duration
}

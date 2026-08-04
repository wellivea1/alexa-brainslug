// Package alexa implements the Alexa custom skill endpoint: request
// verification, intent routing, and response construction.
//
// The package deliberately does not depend on the config package. It takes an
// Options value describing only what it needs, which keeps it testable
// without a process environment and keeps the dependency graph one edge
// smaller.
package alexa

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/prompts"
	"github.com/wellivea1/alexa-brainslug/internal/providers"
	"github.com/wellivea1/alexa-brainslug/internal/security"
	"github.com/wellivea1/alexa-brainslug/internal/store"
)

// maxRequestBytes caps the Alexa request body. Real requests are a few
// kilobytes; this bounds what a malicious caller can make the process read.
const maxRequestBytes = 1 << 20

// Spoken responses. They are constants because every one of them is user
// experience: each is heard aloud, with no screen to fall back on.
const (
	speechReady        = "Brainslug is ready. What should I ask?"
	speechRepromptAsk  = "What should I ask?"
	speechRepromptHelp = "Try asking Brain Slug a question."
	speechGoodbye      = "Goodbye."
	speechHelp         = "Say, ask Brain Slug, followed by your question. You can also say, last response, to hear the previous answer."
	speechNoQuery      = "What should I ask?"
	speechNoQueryHint  = "Tell me the question to send."
	speechFallback     = "I did not catch the question. Say ask, followed by what you want to know."
	speechUnknownType  = "I did not recognize that Alexa request."
	speechUnknownName  = "I do not know how to handle that request yet."
	speechNoIntent     = "I did not catch the intent."
	speechNoHistory    = "I do not have a previous response for you yet."
	speechHistoryError = "I could not read the local history right now."
	speechEmptyPrior   = "The previous response was empty."
	speechEmptyAnswer  = "The AI provider returned an empty answer."

	// Provider failure messages. They distinguish the cases an operator can
	// act on without leaking anything about the provider or the key.
	speechProviderError     = "I could not get an answer from the AI provider."
	speechProviderMisconfig = "The AI provider is not configured yet."
	speechProviderBusy      = "The AI provider is busy right now. Try again in a moment."
	speechProviderNoText    = "The AI provider ran out of room before it answered. The response limit may be too low."
)

// Status values recorded in history.
const (
	statusOK    = "ok"
	statusError = "error"
)

// InteractionStore is the slice of the history store the handler needs.
// Narrowing it here keeps the handler testable with a fake.
type InteractionStore interface {
	SaveInteraction(ctx context.Context, interaction store.Interaction) (int64, error)
	GetLatestResponse(ctx context.Context, userID string) (*store.Interaction, error)
}

// Options is the handler's configuration.
type Options struct {
	// Model and MaxOutputTokens are passed through to the provider and
	// recorded in history, so that history shows what actually answered.
	Model           string
	MaxOutputTokens int

	// SaveHistory controls persistence. When false the handler still answers,
	// it just keeps no record, which also disables the last response intent.
	SaveHistory bool

	// Secrets are redacted from provider errors before storage.
	Secrets []string

	// Verification controls Alexa request authentication.
	Verification VerificationConfig
}

// Handler serves POST /alexa.
type Handler struct {
	opts     Options
	provider providers.Provider
	store    InteractionStore
}

// NewHandler builds the Alexa endpoint handler.
func NewHandler(opts Options, provider providers.Provider, interactionStore InteractionStore) *Handler {
	return &Handler{opts: opts, provider: provider, store: interactionStore}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// The raw bytes are kept because Alexa's signature covers the exact body.
	// Re-encoding the parsed envelope would not verify.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes))
	if err != nil {
		http.Error(w, "could not read request", http.StatusBadRequest)
		return
	}

	var envelope RequestEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		http.Error(w, "invalid Alexa request JSON", http.StatusBadRequest)
		return
	}

	if err := VerifyRequest(r, body, &envelope, h.opts.Verification); err != nil {
		// Amazon specifies 400 for a request that fails verification. The
		// reason is logged locally and never returned: an attacker probing
		// the endpoint learns nothing about which check failed.
		slog.WarnContext(r.Context(), "alexa request verification failed", "error", err)
		http.Error(w, "Alexa request verification failed", http.StatusBadRequest)
		return
	}

	h.writeResponse(r.Context(), w, h.Route(r.Context(), &envelope))
}

// Route dispatches an Alexa request to its handler.
func (h *Handler) Route(ctx context.Context, envelope *RequestEnvelope) ResponseEnvelope {
	switch envelope.Request.Type {
	case requestTypeLaunch:
		return NewAsk(speechReady, speechRepromptAsk)
	case requestTypeIntent:
		return h.handleIntent(ctx, envelope)
	case requestTypeSessionEnded:
		return NewTell(speechGoodbye)
	default:
		return NewAsk(speechUnknownType, speechRepromptHelp)
	}
}

func (h *Handler) handleIntent(ctx context.Context, envelope *RequestEnvelope) ResponseEnvelope {
	if envelope.Request.Intent == nil {
		return NewAsk(speechNoIntent, speechRepromptHelp)
	}

	switch envelope.Request.Intent.Name {
	case IntentAskGPT:
		return h.handleAskGPT(ctx, envelope)
	case IntentLastResponse, IntentRepeat:
		return h.handleLastResponse(ctx, envelope)
	case IntentHelp:
		return NewAsk(speechHelp, speechRepromptAsk)
	case IntentStop, IntentCancel:
		return NewTell(speechGoodbye)
	case IntentFallback:
		return NewAsk(speechFallback, speechRepromptAsk)
	default:
		return NewAsk(speechUnknownName, speechRepromptHelp)
	}
}

// handleAskGPT sends the captured question to the provider.
func (h *Handler) handleAskGPT(ctx context.Context, envelope *RequestEnvelope) ResponseEnvelope {
	query := envelope.Request.Intent.SlotValue(slotQuery)
	if query == "" {
		return NewAsk(speechNoQuery, speechNoQueryHint)
	}

	started := time.Now()
	resp, err := h.provider.Ask(ctx, providers.Request{
		UserID:          envelope.UserID(),
		Prompt:          query,
		Model:           h.opts.Model,
		MaxOutputTokens: h.opts.MaxOutputTokens,
		SystemPrompt:    prompts.DefaultSystemPrompt,
	})

	// Prefer the provider's own measurement, which excludes local encoding
	// and response shaping.
	latency := resp.Latency
	if latency <= 0 {
		latency = time.Since(started)
	}

	if err != nil {
		h.saveInteraction(ctx, store.Interaction{
			AlexaUserID:  envelope.UserID(),
			SessionID:    envelope.SessionID(),
			Provider:     h.provider.Name(),
			Model:        h.opts.Model,
			Prompt:       query,
			LatencyMS:    latency.Milliseconds(),
			Status:       statusError,
			ErrorMessage: security.RedactString(err.Error(), h.opts.Secrets...),
		})
		slog.WarnContext(ctx, "provider call failed", "provider", h.provider.Name(), "error", err)
		return NewTell(speechForProviderError(err))
	}

	short := strings.TrimSpace(resp.ShortAnswer)
	if short == "" {
		short = speechEmptyAnswer
	}

	h.saveInteraction(ctx, store.Interaction{
		AlexaUserID: envelope.UserID(),
		SessionID:   envelope.SessionID(),
		Provider:    firstNonEmpty(resp.ProviderName, h.provider.Name()),
		Model:       firstNonEmpty(resp.Model, h.opts.Model),
		Prompt:      query,
		ShortAnswer: short,
		FullAnswer:  strings.TrimSpace(resp.FullAnswer),
		LatencyMS:   latency.Milliseconds(),
		Status:      statusOK,
	})

	return NewTell(short)
}

// speechForProviderError maps a provider failure to something worth hearing.
//
// The distinctions are the ones a listener can act on: a misconfiguration
// needs an operator, a busy provider needs patience, and an exhausted output
// budget needs a settings change. Everything else is one generic message,
// because guessing aloud is worse than being brief.
func speechForProviderError(err error) string {
	switch {
	case errors.Is(err, providers.ErrNotConfigured):
		return speechProviderMisconfig
	case errors.Is(err, providers.ErrEmptyResponse):
		return speechProviderNoText
	case errors.Is(err, context.DeadlineExceeded):
		return speechProviderBusy
	}

	var apiErr *providers.APIError
	if errors.As(err, &apiErr) && apiErr.Retryable() {
		return speechProviderBusy
	}
	return speechProviderError
}

// handleLastResponse speaks the most recent stored answer for this user.
func (h *Handler) handleLastResponse(ctx context.Context, envelope *RequestEnvelope) ResponseEnvelope {
	if h.store == nil || !h.opts.SaveHistory {
		return NewTell(speechNoHistory)
	}

	latest, err := h.store.GetLatestResponse(ctx, envelope.UserID())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return NewTell(speechNoHistory)
		}
		slog.WarnContext(ctx, "load latest response", "error", err)
		return NewTell(speechHistoryError)
	}

	answer := strings.TrimSpace(latest.ShortAnswer)
	if answer == "" {
		answer = strings.TrimSpace(latest.FullAnswer)
	}
	if answer == "" {
		return NewTell(speechEmptyPrior)
	}
	return NewTell(answer)
}

// saveInteraction records an interaction, if history is enabled.
//
// A failure to record is logged and swallowed: the user has already been
// answered, and failing the spoken response because a local write failed
// would make things worse rather than better.
func (h *Handler) saveInteraction(ctx context.Context, interaction store.Interaction) {
	if !h.opts.SaveHistory || h.store == nil {
		return
	}
	if _, err := h.store.SaveInteraction(ctx, interaction); err != nil {
		slog.WarnContext(ctx, "save interaction", "error", err)
	}
}

// writeResponse encodes an Alexa response.
func (h *Handler) writeResponse(ctx context.Context, w http.ResponseWriter, response ResponseEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		// The status line is already sent, so this cannot become an error
		// response. Logging it is the only remaining option, and silence here
		// would make a truncated reply look like an Alexa-side fault.
		slog.ErrorContext(ctx, "encode alexa response", "error", err)
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

// Package alexa implements the Alexa custom skill endpoint: request
// verification, intent routing, and response construction.
//
// The package is an inbound adapter. It translates ASK requests into
// application operations and translates application results back into Alexa
// responses; provider execution and persistence policy live outside it.
package alexa

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/wellivea1/alexa-brainslug/internal/app"
)

// maxRequestBytes caps the Alexa request body. Real requests are a few
// kilobytes; this bounds what a malicious caller can make the process read.
const maxRequestBytes = 1 << 20

const alexaPrincipalNamespace = "alexa"

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

// ApplicationService is the platform-neutral behavior the ASK adapter needs.
type ApplicationService interface {
	// Ask submits one normalized question.
	Ask(ctx context.Context, req app.AskRequest) (app.Answer, error)

	// LatestAnswer returns the caller's newest saved answer.
	LatestAnswer(ctx context.Context, principal app.Principal) (app.Answer, error)
}

// Options is the ASK adapter configuration.
type Options struct {
	// Verification controls Alexa request authentication.
	Verification VerificationConfig
}

// Handler serves POST /alexa.
type Handler struct {
	opts        Options
	application ApplicationService
}

// NewHandler builds the Alexa endpoint handler.
func NewHandler(opts Options, application ApplicationService) *Handler {
	return &Handler{opts: opts, application: application}
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

func (h *Handler) handleAskGPT(ctx context.Context, envelope *RequestEnvelope) ResponseEnvelope {
	query := envelope.Request.Intent.SlotValue(slotQuery)
	if query == "" {
		return NewAsk(speechNoQuery, speechNoQueryHint)
	}
	if h.application == nil {
		return NewTell(speechProviderMisconfig)
	}

	answer, err := h.application.Ask(ctx, app.AskRequest{
		Principal:      principalForEnvelope(envelope),
		ConversationID: envelope.SessionID(),
		Prompt:         query,
	})
	if err != nil {
		slog.WarnContext(ctx, "application question failed", "error", err)
		return NewTell(speechForApplicationError(err))
	}

	shortAnswer := strings.TrimSpace(answer.ShortAnswer)
	if shortAnswer == "" {
		shortAnswer = speechEmptyAnswer
	}
	return NewTell(shortAnswer)
}

func speechForApplicationError(err error) string {
	switch {
	case errors.Is(err, app.ErrProviderNotConfigured):
		return speechProviderMisconfig
	case errors.Is(err, app.ErrProviderNoText):
		return speechProviderNoText
	case errors.Is(err, app.ErrProviderUnavailable), errors.Is(err, context.DeadlineExceeded):
		return speechProviderBusy
	default:
		return speechProviderError
	}
}

func (h *Handler) handleLastResponse(ctx context.Context, envelope *RequestEnvelope) ResponseEnvelope {
	if h.application == nil {
		return NewTell(speechNoHistory)
	}

	answer, err := h.application.LatestAnswer(ctx, principalForEnvelope(envelope))
	if err != nil {
		switch {
		case errors.Is(err, app.ErrNoHistory):
			return NewTell(speechNoHistory)
		case errors.Is(err, app.ErrEmptySavedAnswer):
			return NewTell(speechEmptyPrior)
		default:
			slog.WarnContext(ctx, "load latest response", "error", err)
			return NewTell(speechHistoryError)
		}
	}

	answerText := strings.TrimSpace(answer.ShortAnswer)
	if answerText == "" {
		answerText = strings.TrimSpace(answer.FullAnswer)
	}
	if answerText == "" {
		return NewTell(speechEmptyPrior)
	}
	return NewTell(answerText)
}

func principalForEnvelope(envelope *RequestEnvelope) app.Principal {
	return app.Principal{
		Namespace: alexaPrincipalNamespace,
		Subject:   envelope.UserID(),
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

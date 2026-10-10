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
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/app"
	"github.com/wellivea1/alexa-brainslug/internal/speech"
)

// maxRequestBytes caps the Alexa request body. Real requests are a few
// kilobytes; this bounds what a malicious caller can make the process read.
const maxRequestBytes = 1 << 20

// ResponseBudget bounds everything between receiving an ASK request and
// returning its response: signature verification, the provider call, and
// saving the outcome. Alexa abandons a skill response after eight seconds,
// measured on Amazon's side, so the rest is left for the network and proxy.
// A provider that is still running at the deadline is canceled, and the user
// hears the busy message instead of Alexa's generic failure.
const ResponseBudget = 7 * time.Second

const alexaPrincipalNamespace = "alexa"

// helpLastResponseExample is the retrieval phrase the help speech teaches. It
// must stay an exact LastResponseIntent sample in skill/interaction-model.json,
// because a one-shot request only routes through the model's samples.
const helpLastResponseExample = "what was the last answer"

// Spoken responses. They are constants because every one of them is user
// experience: each is heard aloud, with no screen to fall back on.
//
// Help, fallback, and unknown-intent speech is followed by the Query
// elicitation prompt from the dialog model, so it ends without a question.
const (
	speechReady        = "Brainslug is ready."
	speechRepromptHelp = "Try asking Brain Slug a question."
	speechGoodbye      = "Goodbye."
	speechHelp         = "Brainslug sends your question to your chosen AI and reads you a short answer. " +
		"To hear an answer again later, say, ask Brain Slug " + helpLastResponseExample + "."
	speechNoQuery      = "What should I ask?"
	speechNoQueryHint  = "Tell me the question to send."
	speechFallback     = "I did not catch that."
	speechUnknownType  = "I did not recognize that Alexa request."
	speechUnknownName  = "I cannot do that yet."
	speechUnknownUser  = "I could not identify this Alexa account."
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
	Tasks        TaskApplication
}

// Handler serves POST /alexa.
type Handler struct {
	application ApplicationService
	verifier    *RequestVerifier
	tasks       TaskApplication
}

// NewHandler builds the Alexa endpoint handler.
func NewHandler(opts Options, application ApplicationService) *Handler {
	return &Handler{
		application: application,
		verifier:    NewRequestVerifier(opts.Verification),
		tasks:       opts.Tasks,
	}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// One deadline covers the whole request, including a cold certificate
	// fetch, so no single stage can spend the time another stage needs.
	ctx, cancel := context.WithTimeout(r.Context(), ResponseBudget)
	defer cancel()
	r = r.WithContext(ctx)

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

	if err := h.verifier.Verify(r, body, &envelope); err != nil {
		// Amazon specifies 400 for a request that fails verification. The
		// reason is logged locally and never returned: an attacker probing
		// the endpoint learns nothing about which check failed.
		slog.WarnContext(ctx, "alexa request verification failed", "error", err)
		http.Error(w, "Alexa request verification failed", http.StatusBadRequest)
		return
	}

	h.writeResponse(ctx, w, h.Route(ctx, &envelope))
}

// Route dispatches an Alexa request to its handler.
func (h *Handler) Route(ctx context.Context, envelope *RequestEnvelope) ResponseEnvelope {
	switch envelope.Request.Type {
	case requestTypeLaunch:
		return NewDelegate(speechReady, IntentAskGPT, slotQuery)
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

	// Help, fallback, and unrecognized intents chain into the question dialog
	// rather than leaving a bare open session. Every AskGPTIntent sample needs
	// a carrier phrase, so outside dialog management a plain question cannot
	// match any intent and would land in fallback again.
	switch envelope.Request.Intent.Name {
	case IntentAskGPT:
		return h.handleAskGPT(ctx, envelope)
	case IntentLastResponse, IntentRepeat:
		return h.handleLastResponse(ctx, envelope)
	case IntentCodexTask, IntentCodexStatus, IntentCodexResult, IntentCodexContinue, IntentCodexReply, IntentCodexCancel:
		return h.handleCodex(ctx, envelope)
	case IntentHelp:
		return askForQuestion(speechHelp)
	case IntentStop, IntentCancel:
		return NewTell(speechGoodbye)
	case IntentFallback:
		return askForQuestion(speechFallback)
	default:
		return askForQuestion(speechUnknownName)
	}
}

// askForQuestion speaks text and then lets the dialog model elicit the Query
// slot, so the user's next utterance is accepted as the question itself.
func askForQuestion(text string) ResponseEnvelope {
	return NewDelegate(text, IntentAskGPT, slotQuery)
}

func (h *Handler) handleAskGPT(ctx context.Context, envelope *RequestEnvelope) ResponseEnvelope {
	query := envelope.Request.Intent.SlotValue(slotQuery)
	if query == "" {
		// Alexa rejects a delegate back into a COMPLETED dialog's own intent.
		if envelope.Request.DialogState == dialogStateCompleted {
			return NewAsk(speechNoQuery, speechNoQueryHint)
		}
		return askForQuestion("")
	}
	if isLastResponseRequest(query) {
		return h.handleLastResponse(ctx, envelope)
	}
	if h.tasks != nil && h.tasks.Automatic() {
		return h.taskResponse(ctx, envelope, "", query, "")
	}
	if h.application == nil {
		return NewTell(speechProviderMisconfig)
	}

	principal := principalForEnvelope(envelope)
	if !principal.Valid() {
		return NewTell(speechUnknownUser)
	}
	answer, err := h.application.Ask(ctx, app.AskRequest{
		Principal:      principal,
		ConversationID: envelope.SessionID(),
		Prompt:         query,
		SystemPrompt:   voiceSystemPrompt,
	})
	if err != nil {
		slog.WarnContext(ctx, "application question failed", "error", err)
		return NewTell(speechForApplicationError(err))
	}

	shortAnswer := speech.Summarize(answer.FullAnswer, spokenWordLimit)
	if shortAnswer == "" {
		shortAnswer = speechEmptyAnswer
	}
	return NewTell(shortAnswer)
}

func speechForApplicationError(err error) string {
	switch {
	case errors.Is(err, app.ErrInvalidPrincipal):
		return speechUnknownUser
	case errors.Is(err, app.ErrProviderNotConfigured):
		return speechProviderMisconfig
	case errors.Is(err, app.ErrProviderNoText), errors.Is(err, app.ErrProviderOutputLimit):
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

	principal := principalForEnvelope(envelope)
	if !principal.Valid() {
		return NewTell(speechUnknownUser)
	}
	answer, err := h.application.LatestAnswer(ctx, principal)
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

	answerText := speech.Summarize(answer.FullAnswer, spokenWordLimit)
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

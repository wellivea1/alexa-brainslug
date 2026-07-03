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

	"alexa-brainslug/internal/config"
	"alexa-brainslug/internal/prompts"
	"alexa-brainslug/internal/providers"
	"alexa-brainslug/internal/security"
	"alexa-brainslug/internal/store"
)

type InteractionStore interface {
	SaveInteraction(ctx context.Context, interaction store.Interaction) (int64, error)
	GetLatestResponse(ctx context.Context, userID string) (*store.Interaction, error)
}

type Handler struct {
	cfg      config.Config
	provider providers.Provider
	store    InteractionStore
}

func NewHandler(cfg config.Config, provider providers.Provider, interactionStore InteractionStore) *Handler {
	return &Handler{
		cfg:      cfg,
		provider: provider,
		store:    interactionStore,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "could not read request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var envelope RequestEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		http.Error(w, "invalid Alexa request JSON", http.StatusBadRequest)
		return
	}

	verifyCfg := VerificationConfig{
		VerifyRequests: h.cfg.AlexaVerifyRequests,
		AllowedSkillID: h.cfg.AlexaAllowedSkillID,
	}
	if err := VerifyRequest(r, body, envelope, verifyCfg); err != nil {
		slog.Warn("Alexa request verification failed", "error", err)
		http.Error(w, "Alexa request verification failed", http.StatusUnauthorized)
		return
	}

	writeAlexaResponse(w, h.Route(r.Context(), envelope))
}

func (h *Handler) Route(ctx context.Context, envelope RequestEnvelope) ResponseEnvelope {
	switch envelope.Request.Type {
	case "LaunchRequest":
		return NewAsk("Brainslug is ready. What should I ask?", "What should I ask?")
	case "IntentRequest":
		return h.handleIntent(ctx, envelope)
	case "SessionEndedRequest":
		return NewTell("Goodbye.")
	default:
		return NewAsk("I did not recognize that Alexa request.", "Try asking Brain Slug a question.")
	}
}

func (h *Handler) handleIntent(ctx context.Context, envelope RequestEnvelope) ResponseEnvelope {
	if envelope.Request.Intent == nil {
		return NewAsk("I did not catch the intent.", "Try asking Brain Slug a question.")
	}

	switch envelope.Request.Intent.Name {
	case IntentAskGPT:
		return h.handleAskGPT(ctx, envelope)
	case IntentLastResponse, IntentRepeat:
		return h.handleLastResponse(ctx, envelope)
	case IntentHelp:
		return NewAsk("Say, ask Brain Slug, followed by your question. You can also say, last response, to hear the previous answer.", "What should I ask?")
	case IntentStop, IntentCancel:
		return NewTell("Goodbye.")
	case IntentFallback:
		return NewAsk("I did not catch the question. Say ask, followed by what you want to know.", "What should I ask?")
	default:
		return NewAsk("I do not know how to handle that request yet.", "Try asking Brain Slug a question.")
	}
}

func (h *Handler) handleAskGPT(ctx context.Context, envelope RequestEnvelope) ResponseEnvelope {
	query := envelope.Request.Intent.SlotValue("Query")
	if query == "" {
		return NewAsk("What should I ask?", "Tell me the question to send.")
	}

	started := time.Now()
	providerResp, err := h.provider.Ask(ctx, providers.Request{
		UserID:          envelope.UserID(),
		Prompt:          query,
		Model:           h.cfg.SelectedModel(),
		MaxOutputTokens: h.cfg.SelectedMaxOutputTokens(),
		SystemPrompt:    prompts.DefaultSystemPrompt,
	})
	latency := time.Since(started)
	if providerResp.Latency > 0 {
		latency = providerResp.Latency
	}

	if err != nil {
		secrets := h.cfg.SecretValues()
		errorMessage := security.RedactString(err.Error(), secrets...)
		h.saveInteraction(ctx, store.Interaction{
			AlexaUserID:  envelope.UserID(),
			SessionID:    envelope.SessionID(),
			Provider:     h.provider.Name(),
			Model:        h.cfg.SelectedModel(),
			Prompt:       query,
			LatencyMS:    latency.Milliseconds(),
			Status:       "error",
			ErrorMessage: errorMessage,
		})
		return NewTell("I could not get an answer from the AI provider. Check the local dashboard for details.")
	}

	shortAnswer := strings.TrimSpace(providerResp.ShortAnswer)
	if shortAnswer == "" {
		shortAnswer = "The AI provider returned an empty answer."
	}
	model := firstNonEmpty(providerResp.Model, h.cfg.SelectedModel())
	providerName := firstNonEmpty(providerResp.ProviderName, h.provider.Name())

	h.saveInteraction(ctx, store.Interaction{
		AlexaUserID: envelope.UserID(),
		SessionID:   envelope.SessionID(),
		Provider:    providerName,
		Model:       model,
		Prompt:      query,
		ShortAnswer: shortAnswer,
		FullAnswer:  strings.TrimSpace(providerResp.FullAnswer),
		LatencyMS:   latency.Milliseconds(),
		Status:      "ok",
	})

	return NewTell(shortAnswer)
}

func (h *Handler) handleLastResponse(ctx context.Context, envelope RequestEnvelope) ResponseEnvelope {
	latest, err := h.store.GetLatestResponse(ctx, envelope.UserID())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return NewTell("I do not have a previous response for you yet.")
		}
		slog.Warn("load latest response", "error", err)
		return NewTell("I could not read the local history right now.")
	}
	answer := strings.TrimSpace(latest.ShortAnswer)
	if answer == "" {
		answer = latest.FullAnswer
	}
	if answer == "" {
		return NewTell("The previous response was empty.")
	}
	return NewTell(answer)
}

func (h *Handler) saveInteraction(ctx context.Context, interaction store.Interaction) {
	if !h.cfg.SaveHistory || h.store == nil {
		return
	}
	if _, err := h.store.SaveInteraction(ctx, interaction); err != nil {
		slog.Warn("save interaction", "error", err)
	}
}

func writeAlexaResponse(w http.ResponseWriter, response ResponseEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

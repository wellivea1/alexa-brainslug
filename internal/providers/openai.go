package providers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/wellivea1/alexa-brainslug/internal/config"
	"github.com/wellivea1/alexa-brainslug/internal/speech"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	openAIResponsesPath  = "/responses"
	openAIName           = "openai"
	openAICompatibleName = "openai-compatible"

	openAIStatusCompleted  = "completed"
	openAIStatusIncomplete = "incomplete"
	openAIStatusFailed     = "failed"
	openAIStatusCanceled   = "cancelled"
	openAIStatusQueued     = "queued"
	openAIStatusInProgress = "in_progress"

	openAIIncompleteMaxOutputTokens = "max_output_tokens"
)

// OpenAI calls the OpenAI Responses API.
type OpenAI struct {
	ep              endpoint
	name            string
	keyName         string
	modelName       string
	reasoningEffort string
	textVerbosity   string
}

// NewOpenAI builds an OpenAI provider. Passing a nil client uses one with the
// configured timeout; tests pass their own.
func NewOpenAI(cfg config.OpenAIConfig, client *http.Client) *OpenAI {
	return newOpenAI(cfg, openAIName, "OPENAI_API_KEY", "OPENAI_MODEL", client)
}

// NewOpenAICompatible builds a distinct compatible provider route. It shares
// the Responses wire format but retains an independent endpoint, model,
// credential, and provider identity.
func NewOpenAICompatible(cfg config.OpenAIConfig, client *http.Client) *OpenAI {
	return newOpenAI(cfg, openAICompatibleName, "OPENAI_COMPATIBLE_API_KEY", "OPENAI_COMPATIBLE_MODEL", client)
}

func newOpenAI(cfg config.OpenAIConfig, name, keyName, modelName string, client *http.Client) *OpenAI {
	return &OpenAI{
		ep:              newEndpoint(cfg.APIKey, cfg.BaseURL, defaultOpenAIBaseURL, cfg.Model, cfg.MaxOutputTokens, cfg.Timeout, client),
		name:            name,
		keyName:         keyName,
		modelName:       modelName,
		reasoningEffort: strings.ToLower(strings.TrimSpace(cfg.ReasoningEffort)),
		textVerbosity:   strings.ToLower(strings.TrimSpace(cfg.TextVerbosity)),
	}
}

// Name implements Provider.
func (p *OpenAI) Name() string { return p.name }

// Ask implements Provider.
func (p *OpenAI) Ask(ctx context.Context, req Request) (Response, error) {
	resolved, err := p.ep.resolve(req, p.keyName, p.modelName)
	if err != nil {
		return Response{}, err
	}

	payload := openAIRequest{
		Model:           resolved.model,
		Instructions:    resolved.systemPrompt,
		Input:           resolved.prompt,
		MaxOutputTokens: resolved.maxTokens,
		Store:           false,
		SafetyID:        endUserIdentifier(req.UserID),
	}
	if p.reasoningEffort != "" {
		payload.Reasoning = &openAIReasoningConfig{Effort: p.reasoningEffort}
	}
	if p.textVerbosity != "" {
		payload.Text = &openAITextConfig{Verbosity: p.textVerbosity}
	}
	headers := map[string]string{"Authorization": "Bearer " + p.ep.apiKey}

	var decoded openAIResponse
	status, latency, err := p.ep.postJSON(ctx, openAIResponsesPath, headers, payload, &decoded)
	if err != nil {
		return Response{}, err
	}
	if status < http.StatusOK || status > 299 {
		return Response{}, p.ep.apiError(p.name, status, decoded.Error.Message)
	}
	if err := decoded.statusError(p.ep); err != nil {
		return Response{}, err
	}

	full := strings.TrimSpace(decoded.outputText())
	if full == "" {
		return Response{}, ErrEmptyResponse
	}

	return Response{
		ShortAnswer:  speech.Summarize(full, spokenWordLimit),
		FullAnswer:   full,
		Model:        firstNonEmpty(decoded.Model, resolved.model),
		ProviderName: p.name,
		Latency:      latency,
	}, nil
}

type openAIRequest struct {
	Model           string                 `json:"model"`
	Instructions    string                 `json:"instructions"`
	Input           string                 `json:"input"`
	MaxOutputTokens int                    `json:"max_output_tokens"`
	Store           bool                   `json:"store"`
	Reasoning       *openAIReasoningConfig `json:"reasoning,omitempty"`
	Text            *openAITextConfig      `json:"text,omitempty"`
	SafetyID        string                 `json:"safety_identifier,omitempty"`
}

type openAIReasoningConfig struct {
	Effort string `json:"effort"`
}

type openAITextConfig struct {
	Verbosity string `json:"verbosity,omitempty"`
}

type openAIResponse struct {
	Model             string                   `json:"model"`
	Status            string                   `json:"status"`
	IncompleteDetails *openAIIncompleteDetails `json:"incomplete_details"`
	OutputText        string                   `json:"output_text"`
	Output            []openAIOutputItem       `json:"output"`
	Error             openAIError              `json:"error"`
}

type openAIIncompleteDetails struct {
	Reason string `json:"reason"`
}

type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

type openAIOutputItem struct {
	Type    string                `json:"type"`
	Content []openAIOutputContent `json:"content"`
}

type openAIOutputContent struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

func (r openAIResponse) statusError(endpoint endpoint) error {
	status := strings.TrimSpace(r.Status)
	if status == "" || status == openAIStatusCompleted {
		return nil
	}

	reason := ""
	if r.IncompleteDetails != nil {
		reason = strings.TrimSpace(r.IncompleteDetails.Reason)
	}

	switch status {
	case openAIStatusIncomplete:
		if reason == openAIIncompleteMaxOutputTokens {
			return ErrOutputLimit
		}
		if reason == "" {
			return ErrIncompleteResponse
		}
		return fmt.Errorf("%w: %s", ErrIncompleteResponse, reason)
	case openAIStatusFailed, openAIStatusCanceled:
		message := strings.TrimSpace(r.Error.Message)
		if message == "" {
			return fmt.Errorf("%w: status %s", ErrFailedResponse, status)
		}
		return fmt.Errorf("%w: %s", ErrFailedResponse, endpoint.redact(message))
	case openAIStatusQueued, openAIStatusInProgress:
		return fmt.Errorf("%w: unexpected synchronous status %s", ErrIncompleteResponse, status)
	default:
		return fmt.Errorf("%w: unrecognized status %s", ErrIncompleteResponse, status)
	}
}

// outputText extracts the answer.
//
// The Responses API exposes a flattened output_text convenience field, but it
// is absent on some response shapes, so the nested output array is the
// fallback. A refusal is returned as the answer rather than discarded: the
// user asked a question and deserves to hear that it was declined.
func (r openAIResponse) outputText() string {
	if strings.TrimSpace(r.OutputText) != "" {
		return r.OutputText
	}

	var parts []string
	for _, item := range r.Output {
		for _, content := range item.Content {
			switch content.Type {
			case "output_text":
				if strings.TrimSpace(content.Text) != "" {
					parts = append(parts, content.Text)
				}
			case "refusal":
				if strings.TrimSpace(content.Refusal) != "" {
					parts = append(parts, content.Refusal)
				}
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

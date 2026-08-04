package providers

import (
	"context"
	"net/http"
	"strings"

	"github.com/wellivea1/alexa-brainslug/internal/config"
	"github.com/wellivea1/alexa-brainslug/internal/speech"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	openAIResponsesPath  = "/responses"
	openAIName           = "openai"
)

// OpenAI calls the OpenAI Responses API.
type OpenAI struct {
	ep endpoint
}

// NewOpenAI builds an OpenAI provider. Passing a nil client uses one with the
// configured timeout; tests pass their own.
func NewOpenAI(cfg config.OpenAIConfig, client *http.Client) *OpenAI {
	return &OpenAI{
		ep: newEndpoint(cfg.APIKey, cfg.BaseURL, defaultOpenAIBaseURL, cfg.Model, cfg.MaxOutputTokens, cfg.Timeout, client),
	}
}

// Name implements Provider.
func (p *OpenAI) Name() string { return openAIName }

// Ask implements Provider.
func (p *OpenAI) Ask(ctx context.Context, req Request) (Response, error) {
	resolved, err := p.ep.resolve(req, "OPENAI_API_KEY", "OPENAI_MODEL")
	if err != nil {
		return Response{}, err
	}

	payload := openAIRequest{
		Model:           resolved.model,
		Instructions:    resolved.systemPrompt,
		Input:           resolved.prompt,
		MaxOutputTokens: resolved.maxTokens,
		Store:           false,
		Text:            &openAITextConfig{Verbosity: "low"},
		SafetyID:        endUserIdentifier(req.UserID),
	}
	headers := map[string]string{"Authorization": "Bearer " + p.ep.apiKey}

	var decoded openAIResponse
	status, latency, err := p.ep.postJSON(ctx, openAIResponsesPath, headers, payload, &decoded)
	if err != nil {
		return Response{}, err
	}
	if status < http.StatusOK || status > 299 {
		return Response{}, p.ep.apiError(openAIName, status, decoded.Error.Message)
	}

	full := strings.TrimSpace(decoded.outputText())
	if full == "" {
		return Response{}, ErrEmptyResponse
	}

	return Response{
		ShortAnswer:  speech.Summarize(full, spokenWordLimit),
		FullAnswer:   full,
		Model:        firstNonEmpty(decoded.Model, resolved.model),
		ProviderName: openAIName,
		Latency:      latency,
	}, nil
}

type openAIRequest struct {
	Model           string            `json:"model"`
	Instructions    string            `json:"instructions"`
	Input           string            `json:"input"`
	MaxOutputTokens int               `json:"max_output_tokens"`
	Store           bool              `json:"store"`
	Text            *openAITextConfig `json:"text,omitempty"`
	SafetyID        string            `json:"safety_identifier,omitempty"`
}

type openAITextConfig struct {
	Verbosity string `json:"verbosity,omitempty"`
}

type openAIResponse struct {
	Model      string             `json:"model"`
	OutputText string             `json:"output_text"`
	Output     []openAIOutputItem `json:"output"`
	Error      openAIError        `json:"error"`
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

package providers

import (
	"context"
	"net/http"
	"strings"

	"github.com/wellivea1/alexa-brainslug/internal/config"
)

const (
	anthropicMessagesPath = "/messages"
	anthropicName         = "anthropic"

	// anthropicVersion pins the Messages API wire format. Anthropic treats
	// this header as required and uses it to keep breaking changes opt-in, so
	// it is a deliberate pin rather than a value to keep current.
	anthropicVersion = "2023-06-01"
)

// Anthropic calls the Anthropic Messages API.
type Anthropic struct {
	ep endpoint
}

// NewAnthropic builds an Anthropic provider. Passing a nil client uses one
// with the configured timeout; tests pass their own.
func NewAnthropic(cfg config.AnthropicConfig, client *http.Client) *Anthropic {
	definition, _ := config.LookupProvider(config.ProviderAnthropic)
	return &Anthropic{
		ep: newEndpoint(cfg.APIKey, cfg.BaseURL, definition.DefaultBaseURL, cfg.Model, cfg.MaxOutputTokens, cfg.Timeout, client),
	}
}

// Name implements Provider.
func (p *Anthropic) Name() string { return anthropicName }

// Ask implements Provider.
func (p *Anthropic) Ask(ctx context.Context, req Request) (Response, error) {
	resolved, err := p.ep.resolve(req, "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL")
	if err != nil {
		return Response{}, err
	}

	payload := anthropicRequest{
		Model:     resolved.model,
		System:    resolved.systemPrompt,
		MaxTokens: resolved.maxTokens,
		Messages:  anthropicMessages(req.PriorContext, resolved.prompt),
	}
	headers := map[string]string{
		"x-api-key":         p.ep.apiKey,
		"anthropic-version": anthropicVersion,
	}

	var decoded anthropicResponse
	status, latency, err := p.ep.postJSON(ctx, anthropicMessagesPath, headers, payload, &decoded)
	if err != nil {
		return Response{}, err
	}
	if status < http.StatusOK || status > 299 {
		return Response{}, p.ep.apiError(anthropicName, status, decoded.Error.Message)
	}

	full := strings.TrimSpace(decoded.text())
	if full == "" {
		return Response{}, ErrEmptyResponse
	}

	return Response{
		FullAnswer:   full,
		Model:        firstNonEmpty(decoded.Model, resolved.model),
		ProviderName: anthropicName,
		Latency:      latency,
	}, nil
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system,omitempty"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage = Message

type anthropicResponse struct {
	Model   string             `json:"model"`
	Content []anthropicContent `json:"content"`
	Error   anthropicError     `json:"error"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// anthropicMessages builds the message list, dropping prior turns with an
// unrecognized role or empty content. The Messages API rejects both, and a
// malformed history entry should not fail the current question.
func anthropicMessages(prior []Message, prompt string) []anthropicMessage {
	messages := make([]anthropicMessage, 0, len(prior)+1)
	for _, message := range prior {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		if role == "assistant" || role == providerRoleUser {
			messages = append(messages, anthropicMessage{Role: role, Content: content})
		}
	}
	return append(messages, anthropicMessage{Role: providerRoleUser, Content: prompt})
}

// text concatenates the text blocks of a response. Thinking blocks and other
// non-text content carry no spoken answer and are skipped; a response made
// entirely of them yields ErrEmptyResponse, which is the reasoning-budget
// exhaustion case.
func (r anthropicResponse) text() string {
	var parts []string
	for _, content := range r.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			parts = append(parts, content.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

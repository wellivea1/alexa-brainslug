package providers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/wellivea1/alexa-brainslug/internal/config"
)

const chatCompletionsPath = "/chat/completions"

// ChatCompletions calls a configured compatible server using its Chat
// Completions wire format. Selecting it never probes or retries another API.
type ChatCompletions struct {
	ep              endpoint
	definition      *config.ProviderDefinition
	reasoningEffort string
	textVerbosity   string
}

func newChatCompletions(cfg config.ProviderConfig, definition *config.ProviderDefinition, client *http.Client) *ChatCompletions {
	return &ChatCompletions{
		ep:         newEndpoint(cfg.APIKey, cfg.BaseURL, definition.DefaultBaseURL, cfg.Model, cfg.MaxOutputTokens, cfg.Timeout, client),
		definition: definition, reasoningEffort: cfg.ReasoningEffort, textVerbosity: cfg.TextVerbosity,
	}
}

// Name implements Provider, keeping identity independent of the wire format.
func (p *ChatCompletions) Name() string { return p.definition.ID }

// Ask implements Provider.
func (p *ChatCompletions) Ask(ctx context.Context, req Request) (Response, error) {
	resolved, err := p.ep.resolve(req, p.definition.EnvPrefix+"_API_KEY", p.definition.EnvPrefix+"_MODEL")
	if err != nil {
		return Response{}, err
	}
	messages := make([]Message, 0, len(req.PriorContext)+2)
	if resolved.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: resolved.systemPrompt})
	}
	messages = append(messages, anthropicMessages(req.PriorContext, resolved.prompt)...)
	payload := chatCompletionRequest{
		Model: resolved.model, Messages: messages, MaxTokens: resolved.maxTokens,
		User: endUserIdentifier(req.UserID), ReasoningEffort: p.reasoningEffort, Verbosity: p.textVerbosity,
	}
	var decoded chatCompletionResponse
	status, latency, err := p.ep.postJSON(ctx, chatCompletionsPath, map[string]string{"Authorization": "Bearer " + p.ep.apiKey}, payload, &decoded)
	if err != nil {
		return Response{}, err
	}
	if status < http.StatusOK || status > 299 {
		return Response{}, p.ep.apiError(p.Name(), status, decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return Response{}, ErrEmptyResponse
	}
	choice := decoded.Choices[0]
	switch choice.FinishReason {
	case "length":
		return Response{}, ErrOutputLimit
	case "content_filter":
		return Response{}, ErrEmptyResponse
	case "", "stop":
	default:
		return Response{}, fmt.Errorf("%w: finish reason %s", ErrIncompleteResponse, p.ep.redact(choice.FinishReason))
	}
	full := strings.TrimSpace(choice.Message.Content)
	if full == "" {
		return Response{}, ErrEmptyResponse
	}
	return Response{FullAnswer: full, Model: firstNonEmpty(decoded.Model, resolved.model), ProviderName: p.Name(), Latency: latency}, nil
}

type chatCompletionRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	// Compatible servers commonly implement this older field rather than
	// OpenAI's newer max_completion_tokens. Official OpenAI uses Responses.
	MaxTokens       int    `json:"max_tokens"`
	User            string `json:"user,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	Verbosity       string `json:"verbosity,omitempty"`
}

type chatCompletionResponse struct {
	Model   string                 `json:"model"`
	Choices []chatCompletionChoice `json:"choices"`
	Error   openAIError            `json:"error"`
}

type chatCompletionChoice struct {
	FinishReason string `json:"finish_reason"`
	Message      struct {
		Content string `json:"content"`
	} `json:"message"`
}

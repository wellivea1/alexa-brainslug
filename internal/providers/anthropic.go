package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"alexa-brainslug/internal/config"
	"alexa-brainslug/internal/prompts"
	"alexa-brainslug/internal/security"
)

const (
	defaultAnthropicBaseURL = "https://api.anthropic.com/v1"
	anthropicVersion        = "2023-06-01"
)

type Anthropic struct {
	apiKey          string
	baseURL         string
	model           string
	maxOutputTokens int
	timeout         time.Duration
	client          *http.Client
}

func NewAnthropic(cfg config.AnthropicConfig, client *http.Client) *Anthropic {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultAnthropicBaseURL
	}
	return &Anthropic{
		apiKey:          strings.TrimSpace(cfg.APIKey),
		baseURL:         baseURL,
		model:           strings.TrimSpace(cfg.Model),
		maxOutputTokens: cfg.MaxOutputTokens,
		timeout:         timeout,
		client:          client,
	}
}

func (p *Anthropic) Name() string {
	return "anthropic"
}

func (p *Anthropic) Ask(ctx context.Context, req Request) (Response, error) {
	if p.apiKey == "" {
		return Response{}, errors.New("ANTHROPIC_API_KEY is not configured")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.model
	}
	if model == "" {
		return Response{}, errors.New("ANTHROPIC_MODEL is not configured")
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return Response{}, errors.New("prompt is empty")
	}
	maxTokens := req.MaxOutputTokens
	if maxTokens == 0 {
		maxTokens = p.maxOutputTokens
	}
	if maxTokens < 16 {
		maxTokens = 16
	}
	systemPrompt := strings.TrimSpace(req.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = prompts.DefaultSystemPrompt
	}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	body := anthropicRequest{
		Model:     model,
		System:    systemPrompt,
		MaxTokens: maxTokens,
		Messages:  anthropicMessages(req.PriorContext, prompt),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("marshal Anthropic request: %w", err)
	}

	endpoint, err := anthropicMessagesEndpoint(p.baseURL)
	if err != nil {
		return Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("create Anthropic request: %w", err)
	}
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("Content-Type", "application/json")

	started := time.Now()
	httpResp, err := p.client.Do(httpReq)
	latency := time.Since(started)
	if err != nil {
		return Response{}, fmt.Errorf("call Anthropic messages API: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 4<<20))
	if err != nil {
		return Response{}, fmt.Errorf("read Anthropic response: %w", err)
	}

	var decoded anthropicResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return Response{}, fmt.Errorf("decode Anthropic response: %w", err)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode > 299 {
		if decoded.Error.Message != "" {
			return Response{}, fmt.Errorf("Anthropic API error: %s", security.RedactString(decoded.Error.Message, p.apiKey))
		}
		return Response{}, fmt.Errorf("Anthropic API error: status %d", httpResp.StatusCode)
	}

	fullAnswer := strings.TrimSpace(extractAnthropicText(decoded))
	if fullAnswer == "" {
		return Response{}, errors.New("Anthropic response did not include text output")
	}
	return Response{
		ShortAnswer:  voiceSummary(fullAnswer, 120),
		FullAnswer:   fullAnswer,
		Model:        firstNonEmpty(decoded.Model, model),
		ProviderName: p.Name(),
		Latency:      latency,
	}, nil
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system,omitempty"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

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

func anthropicMessages(prior []Message, prompt string) []anthropicMessage {
	messages := make([]anthropicMessage, 0, len(prior)+1)
	for _, message := range prior {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		switch role {
		case "assistant", "user":
			messages = append(messages, anthropicMessage{Role: role, Content: content})
		}
	}
	messages = append(messages, anthropicMessage{Role: "user", Content: prompt})
	return messages
}

func anthropicMessagesEndpoint(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("ANTHROPIC_BASE_URL is invalid: %q", baseURL)
	}
	return strings.TrimRight(baseURL, "/") + "/messages", nil
}

func extractAnthropicText(resp anthropicResponse) string {
	var parts []string
	for _, content := range resp.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			parts = append(parts, content.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

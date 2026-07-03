package providers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

const defaultOpenAIBaseURL = "https://api.openai.com/v1"

type OpenAI struct {
	apiKey          string
	baseURL         string
	model           string
	maxOutputTokens int
	timeout         time.Duration
	client          *http.Client
}

func NewOpenAI(cfg config.OpenAIConfig, client *http.Client) *OpenAI {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	return &OpenAI{
		apiKey:          strings.TrimSpace(cfg.APIKey),
		baseURL:         baseURL,
		model:           strings.TrimSpace(cfg.Model),
		maxOutputTokens: cfg.MaxOutputTokens,
		timeout:         timeout,
		client:          client,
	}
}

func (p *OpenAI) Name() string {
	return "openai"
}

func (p *OpenAI) Ask(ctx context.Context, req Request) (Response, error) {
	if p.apiKey == "" {
		return Response{}, errors.New("OPENAI_API_KEY is not configured")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.model
	}
	if model == "" {
		return Response{}, errors.New("OPENAI_MODEL is not configured")
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

	body := openAIRequest{
		Model:           model,
		Instructions:    systemPrompt,
		Input:           prompt,
		MaxOutputTokens: maxTokens,
		Store:           false,
		Text:            &openAITextConfig{Verbosity: "low"},
		SafetyID:        safetyIdentifier(req.UserID),
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("marshal OpenAI request: %w", err)
	}

	endpoint, err := responsesEndpoint(p.baseURL)
	if err != nil {
		return Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("create OpenAI request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	started := time.Now()
	httpResp, err := p.client.Do(httpReq)
	latency := time.Since(started)
	if err != nil {
		return Response{}, fmt.Errorf("call OpenAI responses API: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 4<<20))
	if err != nil {
		return Response{}, fmt.Errorf("read OpenAI response: %w", err)
	}

	var decoded openAIResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return Response{}, fmt.Errorf("decode OpenAI response: %w", err)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode > 299 {
		if decoded.Error.Message != "" {
			return Response{}, fmt.Errorf("OpenAI API error: %s", security.RedactString(decoded.Error.Message, p.apiKey))
		}
		return Response{}, fmt.Errorf("OpenAI API error: status %d", httpResp.StatusCode)
	}

	fullAnswer := strings.TrimSpace(extractOutputText(decoded))
	if fullAnswer == "" {
		return Response{}, errors.New("OpenAI response did not include text output")
	}
	return Response{
		ShortAnswer:  voiceSummary(fullAnswer, 120),
		FullAnswer:   fullAnswer,
		Model:        firstNonEmpty(decoded.Model, model),
		ProviderName: p.Name(),
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

func responsesEndpoint(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("OPENAI_BASE_URL is invalid: %q", baseURL)
	}
	return strings.TrimRight(baseURL, "/") + "/responses", nil
}

func extractOutputText(resp openAIResponse) string {
	if strings.TrimSpace(resp.OutputText) != "" {
		return resp.OutputText
	}
	var parts []string
	for _, item := range resp.Output {
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

func voiceSummary(text string, maxWords int) string {
	text = strings.TrimSpace(stripMarkdownForSpeech(text))
	if text == "" {
		return ""
	}
	words := strings.Fields(text)
	if len(words) <= maxWords {
		return text
	}
	summary := strings.Join(words[:maxWords], " ")
	if idx := strings.LastIndexAny(summary, ".!?"); idx > len(summary)/2 {
		summary = strings.TrimSpace(summary[:idx+1])
	}
	return summary + " More detail is available in the local dashboard."
}

func stripMarkdownForSpeech(text string) string {
	replacements := []struct {
		old string
		new string
	}{
		{"**", ""},
		{"__", ""},
		{"`", ""},
		{"### ", ""},
		{"## ", ""},
		{"# ", ""},
		{"- ", ""},
		{"* ", ""},
	}
	cleaned := text
	for _, replacement := range replacements {
		cleaned = strings.ReplaceAll(cleaned, replacement.old, replacement.new)
	}
	return cleaned
}

func safetyIdentifier(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(userID))
	return hex.EncodeToString(sum[:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

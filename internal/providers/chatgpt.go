package providers

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/config"
	"github.com/wellivea1/alexa-brainslug/internal/prompts"
	"github.com/wellivea1/alexa-brainslug/internal/security"
	"github.com/wellivea1/alexa-brainslug/internal/speech"
)

const chatGPTCodexResponsesURL = "https://chatgpt.com/backend-api/codex/responses"

// ChatGPTCredentialSource supplies a current ChatGPT account credential. It is
// implemented by openauth.Manager without making the provider own login state.
type ChatGPTCredentialSource interface {
	Connected() bool
	Token(context.Context) (accessToken, accountID string, err error)
}

// ChatGPT calls the account-scoped Codex Responses transport. It intentionally
// remains distinct from OpenAI, which calls the documented OpenAI API.
type ChatGPT struct {
	credentials     ChatGPTCredentialSource
	model           string
	maxOutputTokens int
	timeout         time.Duration
	reasoningEffort string
	textVerbosity   string
	endpoint        string
	http            *http.Client
}

// NewChatGPT builds the account connector used after dashboard login.
func NewChatGPT(cfg config.OpenAIConfig, credentials ChatGPTCredentialSource, client *http.Client) *ChatGPT {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	return &ChatGPT{
		credentials:     credentials,
		model:           strings.TrimSpace(cfg.Model),
		maxOutputTokens: cfg.MaxOutputTokens,
		timeout:         timeout,
		reasoningEffort: strings.ToLower(strings.TrimSpace(cfg.ReasoningEffort)),
		textVerbosity:   strings.ToLower(strings.TrimSpace(cfg.TextVerbosity)),
		endpoint:        chatGPTCodexResponsesURL,
		http:            client,
	}
}

// Name implements Provider while retaining the stable OpenAI history label.
func (p *ChatGPT) Name() string { return openAIName }

// Ask sends one non-persistent response request and normalizes its event
// stream into the same response contract as the OpenAI API adapter.
func (p *ChatGPT) Ask(ctx context.Context, req Request) (Response, error) {
	if p.credentials == nil || !p.credentials.Connected() {
		return Response{}, fmt.Errorf("%w: connect a ChatGPT account in the dashboard", ErrNotConfigured)
	}
	accessToken, accountID, err := p.credentials.Token(ctx)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %w", ErrNotConfigured, err)
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.model
	}
	if model == "" {
		return Response{}, fmt.Errorf("%w: OPENAI_MODEL is not set", ErrNotConfigured)
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return Response{}, ErrEmptyPrompt
	}
	maxTokens := req.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = p.maxOutputTokens
	}
	maxTokens = max(maxTokens, minOutputTokens)
	instructions := strings.TrimSpace(req.SystemPrompt)
	if instructions == "" {
		instructions = prompts.DefaultSystemPrompt
	}

	payload := chatGPTRequest{
		Model:           model,
		Instructions:    instructions,
		Input:           []chatGPTInput{{Role: providerRoleUser, Content: prompt}},
		MaxOutputTokens: maxTokens,
		Store:           false,
		Stream:          true,
		Include:         []string{"reasoning.encrypted_content"},
	}
	if p.reasoningEffort != "" {
		payload.Reasoning = &openAIReasoningConfig{Effort: p.reasoningEffort}
	}
	if p.textVerbosity != "" {
		payload.Text = &openAITextConfig{Verbosity: p.textVerbosity}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Response{}, fmt.Errorf("encode ChatGPT request: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(callCtx, http.MethodPost, p.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return Response{}, fmt.Errorf("build ChatGPT request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+accessToken)
	httpRequest.Header.Set("ChatGPT-Account-Id", accountID)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Originator", "brainslug")
	httpRequest.Header.Set("Session-Id", newChatGPTSessionID())
	httpRequest.Header.Set("User-Agent", "Alexa-Brainslug")

	started := time.Now()
	httpResponse, err := p.http.Do(httpRequest)
	latency := time.Since(started)
	if err != nil {
		return Response{}, fmt.Errorf("call ChatGPT provider: %w", err)
	}
	if latency <= 0 {
		latency = time.Nanosecond
	}
	if httpResponse.Body == nil {
		return Response{}, errors.New("ChatGPT provider response body is missing")
	}
	defer func() {
		_, _ = io.Copy(io.Discard, httpResponse.Body)
		_ = httpResponse.Body.Close()
	}()
	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxResponseBytes+1))
	if err != nil {
		return Response{}, fmt.Errorf("read ChatGPT response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return Response{}, errors.New("ChatGPT response exceeds size limit")
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode > 299 {
		var envelope openAIResponse
		_ = json.Unmarshal(body, &envelope)
		message := security.RedactString(envelope.Error.Message, accessToken)
		if message == "" {
			message = "ChatGPT account request was rejected"
		}
		return Response{}, &APIError{Provider: openAIName, Status: httpResponse.StatusCode, Message: message}
	}

	decoded, err := decodeChatGPTResponse(body, accessToken)
	if err != nil {
		return Response{}, err
	}
	if err := decoded.statusError(newEndpoint(accessToken, "", "https://chatgpt.com/backend-api/codex", model, maxTokens, p.timeout, p.http)); err != nil {
		return Response{}, err
	}
	full := strings.TrimSpace(decoded.outputText())
	if full == "" {
		return Response{}, ErrEmptyResponse
	}
	return Response{
		ShortAnswer:  speech.Summarize(full, spokenWordLimit),
		FullAnswer:   full,
		Model:        firstNonEmpty(decoded.Model, model),
		ProviderName: openAIName,
		Latency:      latency,
	}, nil
}

type chatGPTRequest struct {
	Model           string                 `json:"model"`
	Instructions    string                 `json:"instructions"`
	Input           []chatGPTInput         `json:"input"`
	MaxOutputTokens int                    `json:"max_output_tokens"`
	Store           bool                   `json:"store"`
	Stream          bool                   `json:"stream"`
	Include         []string               `json:"include"`
	Reasoning       *openAIReasoningConfig `json:"reasoning,omitempty"`
	Text            *openAITextConfig      `json:"text,omitempty"`
}

type chatGPTInput struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func decodeChatGPTResponse(body []byte, accessToken string) (openAIResponse, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return openAIResponse{}, errors.New("ChatGPT returned an empty response stream")
	}
	if trimmed[0] == '{' {
		var response openAIResponse
		if err := json.Unmarshal(trimmed, &response); err != nil {
			return openAIResponse{}, fmt.Errorf("decode ChatGPT response: %w", err)
		}
		return response, nil
	}

	var response openAIResponse
	var outputText strings.Builder
	var streamErr error
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), maxResponseBytes)
	var dataLines []string
	flush := func() {
		if streamErr != nil || len(dataLines) == 0 {
			dataLines = nil
			return
		}
		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = nil
		if payload == "" || payload == "[DONE]" {
			return
		}
		var event struct {
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			Text     string          `json:"text"`
			Response json.RawMessage `json:"response"`
			Error    openAIError     `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			streamErr = fmt.Errorf("decode ChatGPT response event: %w", err)
			return
		}
		if event.Type == "error" || strings.HasSuffix(event.Type, ".failed") || strings.TrimSpace(event.Error.Message) != "" {
			message := security.RedactString(event.Error.Message, accessToken)
			if message == "" {
				message = "ChatGPT response stream failed"
			}
			streamErr = errors.New(message)
			return
		}
		if len(event.Response) > 0 && string(event.Response) != "null" {
			if err := json.Unmarshal(event.Response, &response); err != nil {
				streamErr = fmt.Errorf("decode completed ChatGPT response: %w", err)
				return
			}
		}
		if strings.Contains(event.Type, "output_text.delta") {
			outputText.WriteString(event.Delta)
		}
		if strings.Contains(event.Type, "output_text.done") && outputText.Len() == 0 {
			outputText.WriteString(event.Text)
		}
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
	if err := scanner.Err(); err != nil {
		return openAIResponse{}, fmt.Errorf("read ChatGPT response stream: %w", err)
	}
	if streamErr != nil {
		return openAIResponse{}, streamErr
	}
	if strings.TrimSpace(response.OutputText) == "" {
		response.OutputText = outputText.String()
	}
	if strings.TrimSpace(response.OutputText) == "" && len(response.Output) == 0 {
		return openAIResponse{}, errors.New("ChatGPT response stream contained no answer")
	}
	return response, nil
}

func newChatGPTSessionID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("brainslug-%d", time.Now().UTC().UnixNano())
	}
	return "brainslug-" + hex.EncodeToString(value[:])
}

var _ Provider = (*ChatGPT)(nil)

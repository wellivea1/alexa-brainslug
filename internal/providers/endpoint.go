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

	"github.com/wellivea1/alexa-brainslug/internal/prompts"
)

const (
	// defaultTimeout applies when the operator did not set one. It leaves room
	// for Alexa and proxy overhead inside the external request budget.
	defaultTimeout = 6 * time.Second

	// maxResponseBytes caps how much of a provider response is read. A
	// well-behaved provider returns far less; this bounds memory if one does
	// not.
	maxResponseBytes = 4 << 20

	// minOutputTokens is the floor for the output cap. Below this a response
	// cannot contain a usable sentence.
	minOutputTokens = 16
)

// endpoint is the HTTP plumbing shared by every provider: URL construction,
// credential handling, timeouts, request encoding, and response decoding.
//
// It holds no provider-specific knowledge. Wire formats, header names, and
// error extraction stay in the provider that owns them.
type endpoint struct {
	apiKey          string
	baseURL         string
	model           string
	maxOutputTokens int
	timeout         time.Duration
	client          *http.Client
}

// newEndpoint normalizes provider configuration once, at construction, so
// that per-request code does not repeat it.
func newEndpoint(apiKey, baseURL, defaultBaseURL, model string, maxOutputTokens int, timeout time.Duration, client *http.Client) endpoint {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	resolved := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if resolved == "" {
		resolved = defaultBaseURL
	}
	return endpoint{
		apiKey:          strings.TrimSpace(apiKey),
		baseURL:         resolved,
		model:           strings.TrimSpace(model),
		maxOutputTokens: maxOutputTokens,
		timeout:         timeout,
		client:          client,
	}
}

// call is a Request with provider defaults applied.
type call struct {
	model        string
	prompt       string
	maxTokens    int
	systemPrompt string
}

// resolve validates a Request and fills in configured defaults.
//
// keyName and modelName are the operator-facing environment variable names,
// so that a misconfiguration names the variable the operator has to fix
// rather than an internal field.
func (e endpoint) resolve(req Request, keyName, modelName string) (call, error) {
	if e.apiKey == "" {
		return call{}, fmt.Errorf("%w: %s is not set", ErrNotConfigured, keyName)
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = e.model
	}
	if model == "" {
		return call{}, fmt.Errorf("%w: %s is not set", ErrNotConfigured, modelName)
	}

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return call{}, ErrEmptyPrompt
	}

	maxTokens := req.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = e.maxOutputTokens
	}
	maxTokens = max(maxTokens, minOutputTokens)

	systemPrompt := strings.TrimSpace(req.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = prompts.DefaultSystemPrompt
	}

	return call{
		model:        model,
		prompt:       prompt,
		maxTokens:    maxTokens,
		systemPrompt: systemPrompt,
	}, nil
}

// postJSON sends payload to path and decodes the response into out.
//
// It returns the HTTP status and the time spent in the call. A non-2xx status
// is not an error here: the provider owns extracting a message from its own
// error envelope, which postJSON has already decoded into out.
func (e endpoint) postJSON(ctx context.Context, providerPath string, headers map[string]string, payload, out any) (int, time.Duration, error) {
	endpointURL, err := e.resolveURL(providerPath)
	if err != nil {
		return 0, 0, err
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, 0, fmt.Errorf("encode request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(encoded))
	if err != nil {
		return 0, 0, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		httpReq.Header.Set(name, value)
	}

	started := time.Now()
	httpResp, err := e.client.Do(httpReq)
	latency := time.Since(started)
	if err != nil {
		return 0, latency, fmt.Errorf("call provider: %w", err)
	}
	// Extremely fast in-memory transports can complete inside one clock tick.
	// A successful call still has a measured duration, and downstream status
	// views use zero to mean "not measured".
	if latency <= 0 {
		latency = time.Nanosecond
	}
	if httpResp.Body == nil {
		return 0, latency, errors.New("provider response body is missing")
	}
	defer func() {
		// Drain before closing so the connection can be reused. Neither error
		// is actionable: the response has already been read.
		_, _ = io.Copy(io.Discard, httpResp.Body)
		_ = httpResp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, maxResponseBytes+1))
	if err != nil {
		return httpResp.StatusCode, latency, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return httpResp.StatusCode, latency, errors.New("provider response exceeds size limit")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return httpResp.StatusCode, latency, fmt.Errorf("decode response: %w", err)
	}
	return httpResp.StatusCode, latency, nil
}

// resolveURL joins the configured base URL with a provider path.
func (e endpoint) resolveURL(providerPath string) (string, error) {
	parsed, err := url.Parse(e.baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("%w: base URL %q is not a valid absolute URL", ErrNotConfigured, e.baseURL)
	}
	return e.baseURL + providerPath, nil
}

// apiError builds an *APIError with the credential redacted from the message,
// so that a provider echoing a key back cannot leak it into local history.
func (e endpoint) apiError(provider string, status int, message string) *APIError {
	return &APIError{
		Provider: provider,
		Status:   status,
		Message:  e.redact(message),
	}
}

// endUserIdentifier hashes a namespaced platform user key for providers that
// accept a stable end-user identifier for abuse monitoring.
func endUserIdentifier(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(userID))
	return hex.EncodeToString(sum[:])
}

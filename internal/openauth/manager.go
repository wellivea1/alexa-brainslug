// Package openauth manages the optional ChatGPT account connection used by
// the OpenAI Codex transport. It is deliberately separate from OpenAI API-key
// authentication: the two credentials have different scopes and endpoints.
package openauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/security"
)

const (
	chatGPTClientID       = "app_EMoamEEZ73f0CkXaXp7hrann"
	clientIDParameter     = "client_id"
	chatGPTIssuer         = "https://auth.openai.com"
	callbackAddress       = "localhost:1455"
	callbackPath          = "/auth/callback"
	credentialVersion     = 1
	browserFlowTTL        = 5 * time.Minute
	deviceFlowTTL         = 10 * time.Minute
	defaultTokenLifetime  = time.Hour
	maximumAuthBodyBytes  = 1 << 20
	refreshSafetyWindow   = time.Minute
	deviceVerificationURL = chatGPTIssuer + "/codex/device"
)

// Snapshot is a secret-free view of the local ChatGPT connection.
type Snapshot struct {
	Connected    bool
	AccountIDSet bool
	ExpiresAt    time.Time
	StoragePath  string
}

// DeviceFlow is the non-secret state displayed during code login.
type DeviceFlow struct {
	ID                  string
	UserCode            string
	VerificationURL     string
	ExpiresAt           time.Time
	PollIntervalSeconds int
}

// PollResult describes whether a device-code flow is still waiting.
type PollResult string

const (
	// PollPending means the operator has not completed sign-in yet.
	PollPending PollResult = "pending"
	// PollConnected means tokens were exchanged and stored successfully.
	PollConnected PollResult = "connected"
)

type credentials struct {
	Version      int       `json:"version"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	AccountID    string    `json:"account_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type tokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type browserPending struct {
	Verifier  string
	ReturnURL string
	ExpiresAt time.Time
}

type devicePending struct {
	DeviceAuthID string
	UserCode     string
	ExpiresAt    time.Time
	Interval     time.Duration
}

// Manager owns credentials, refreshes, and the short-lived localhost OAuth
// callback listener. The credential file is never exposed through Snapshot.
type Manager struct {
	mu             sync.Mutex
	path           string
	http           *http.Client
	credentials    credentials
	browser        map[string]browserPending
	device         map[string]devicePending
	callbackServer *http.Server
	onConnected    func() error
}

// New loads an existing credential file when present.
func New(path string, client *http.Client) (*Manager, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	m := &Manager{
		path:    strings.TrimSpace(path),
		http:    client,
		browser: make(map[string]browserPending),
		device:  make(map[string]devicePending),
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

// SetOnConnected installs the runtime activation hook invoked after tokens
// have been durably stored.
func (m *Manager) SetOnConnected(callback func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onConnected = callback
}

// Snapshot returns connection metadata without tokens or account identifiers.
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Snapshot{
		Connected:    strings.TrimSpace(m.credentials.RefreshToken) != "" && strings.TrimSpace(m.credentials.AccountID) != "",
		AccountIDSet: strings.TrimSpace(m.credentials.AccountID) != "",
		ExpiresAt:    m.credentials.ExpiresAt,
		StoragePath:  m.path,
	}
}

// Connected reports whether a refreshable ChatGPT account credential exists.
func (m *Manager) Connected() bool { return m.Snapshot().Connected }

// Token returns a current bearer token and its ChatGPT account identifier.
func (m *Manager) Token(ctx context.Context) (accessToken, accountID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if strings.TrimSpace(m.credentials.RefreshToken) == "" || strings.TrimSpace(m.credentials.AccountID) == "" {
		return "", "", errors.New("ChatGPT account is not connected")
	}
	if token := strings.TrimSpace(m.credentials.AccessToken); token != "" &&
		(m.credentials.ExpiresAt.IsZero() || time.Now().UTC().Add(refreshSafetyWindow).Before(m.credentials.ExpiresAt)) {
		return token, m.credentials.AccountID, nil
	}
	tokens, err := m.refresh(ctx, m.credentials.RefreshToken)
	if err != nil {
		return "", "", err
	}
	if err := m.storeTokensLocked(tokens); err != nil {
		return "", "", err
	}
	return m.credentials.AccessToken, m.credentials.AccountID, nil
}

// StartBrowser starts a PKCE flow and the localhost callback listener.
func (m *Manager) StartBrowser(returnURL string) (string, error) {
	returnURL, err := validateReturnURL(returnURL)
	if err != nil {
		return "", err
	}
	verifier, challenge, err := newPKCE()
	if err != nil {
		return "", err
	}
	state, err := randomToken(32)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.cleanupLocked(time.Now().UTC())
	if err := m.ensureCallbackServerLocked(); err != nil {
		m.mu.Unlock()
		return "", err
	}
	m.browser[state] = browserPending{Verifier: verifier, ReturnURL: returnURL, ExpiresAt: time.Now().UTC().Add(browserFlowTTL)}
	m.mu.Unlock()
	time.AfterFunc(browserFlowTTL, func() { m.stopCallbackServerIfIdle(context.Background()) })

	params := url.Values{
		"response_type":              {"code"},
		clientIDParameter:            {chatGPTClientID},
		"redirect_uri":               {redirectURI()},
		"scope":                      {"openid profile email offline_access"},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"state":                      {state},
		"originator":                 {"brainslug"},
	}
	return chatGPTIssuer + "/oauth/authorize?" + params.Encode(), nil
}

// StartDevice starts the code flow used when the browser callback is not
// practical. No credential is stored until PollDevice completes.
func (m *Manager) StartDevice(ctx context.Context) (DeviceFlow, error) {
	payload, err := json.Marshal(map[string]string{clientIDParameter: chatGPTClientID})
	if err != nil {
		return DeviceFlow{}, err
	}
	var response struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		Interval     string `json:"interval"`
	}
	if err := m.postJSON(ctx, chatGPTIssuer+"/api/accounts/deviceauth/usercode", payload, &response); err != nil {
		return DeviceFlow{}, err
	}
	if strings.TrimSpace(response.DeviceAuthID) == "" || strings.TrimSpace(response.UserCode) == "" {
		return DeviceFlow{}, errors.New("OpenAI device authorization response was incomplete")
	}
	id, err := randomToken(24)
	if err != nil {
		return DeviceFlow{}, err
	}
	interval := parseInterval(response.Interval)
	pending := devicePending{
		DeviceAuthID: response.DeviceAuthID,
		UserCode:     response.UserCode,
		ExpiresAt:    time.Now().UTC().Add(deviceFlowTTL),
		Interval:     interval,
	}
	m.mu.Lock()
	m.cleanupLocked(time.Now().UTC())
	m.device[id] = pending
	m.mu.Unlock()
	return DeviceFlow{ID: id, UserCode: pending.UserCode, VerificationURL: deviceVerificationURL, ExpiresAt: pending.ExpiresAt, PollIntervalSeconds: int(interval / time.Second)}, nil
}

// Device returns a pending code flow for display.
func (m *Manager) Device(id string) (DeviceFlow, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(time.Now().UTC())
	pending, ok := m.device[strings.TrimSpace(id)]
	if !ok {
		return DeviceFlow{}, false
	}
	return DeviceFlow{ID: id, UserCode: pending.UserCode, VerificationURL: deviceVerificationURL, ExpiresAt: pending.ExpiresAt, PollIntervalSeconds: int(pending.Interval / time.Second)}, true
}

// PollDevice checks whether the user completed a code flow.
func (m *Manager) PollDevice(ctx context.Context, id string) (PollResult, error) {
	id = strings.TrimSpace(id)
	m.mu.Lock()
	m.cleanupLocked(time.Now().UTC())
	pending, ok := m.device[id]
	m.mu.Unlock()
	if !ok {
		return "", errors.New("OpenAI code login expired; start again")
	}
	payload, err := json.Marshal(map[string]string{"device_auth_id": pending.DeviceAuthID, "user_code": pending.UserCode})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatGPTIssuer+"/api/accounts/deviceauth/token", strings.NewReader(string(payload)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Alexa-Brainslug")
	resp, err := m.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("poll OpenAI code login: %w", err)
	}
	data, readErr := readLimited(resp.Body)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return PollPending, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", authHTTPError("poll OpenAI code login", resp.StatusCode, data, pending.DeviceAuthID, pending.UserCode)
	}
	var exchange struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.Unmarshal(data, &exchange); err != nil {
		return "", fmt.Errorf("decode OpenAI code login: %w", err)
	}
	if strings.TrimSpace(exchange.AuthorizationCode) == "" || strings.TrimSpace(exchange.CodeVerifier) == "" {
		return "", errors.New("OpenAI code login response was incomplete")
	}
	tokens, err := m.exchange(ctx, exchange.AuthorizationCode, chatGPTIssuer+"/deviceauth/callback", exchange.CodeVerifier)
	if err != nil {
		return "", err
	}
	if err := m.saveTokensAndActivate(tokens); err != nil {
		return "", err
	}
	m.mu.Lock()
	delete(m.device, id)
	m.mu.Unlock()
	return PollConnected, nil
}

// Clear removes the locally stored ChatGPT credential.
func (m *Manager) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credentials = credentials{}
	if m.path == "" {
		return nil
	}
	if err := os.Remove(m.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove ChatGPT credential file: %w", err)
	}
	return nil
}

// Close stops the optional callback listener.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	server := m.callbackServer
	m.callbackServer = nil
	m.mu.Unlock()
	if server == nil {
		return nil
	}
	return server.Shutdown(ctx)
}

func (m *Manager) ensureCallbackServerLocked() error {
	if m.callbackServer != nil {
		return nil
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(context.Background(), "tcp", callbackAddress)
	if err != nil {
		return fmt.Errorf("OpenAI callback port %s is unavailable: %w", callbackAddress, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, m.handleCallback)
	server := &http.Server{Addr: callbackAddress, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	m.callbackServer = server
	go func() { _ = server.Serve(listener) }()
	return nil
}

func (m *Manager) handleCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	state := strings.TrimSpace(query.Get("state"))
	code := strings.TrimSpace(query.Get("code"))
	m.mu.Lock()
	pending, ok := m.browser[state]
	if ok {
		delete(m.browser, state)
	}
	m.cleanupLocked(time.Now().UTC())
	m.mu.Unlock()
	go m.stopCallbackServerIfIdle(context.WithoutCancel(r.Context()))
	if !ok || time.Now().UTC().After(pending.ExpiresAt) {
		http.Error(w, "OpenAI login expired or did not match the request.", http.StatusBadRequest)
		return
	}
	if query.Get("error") != "" || code == "" {
		redirectAuthResult(w, r, pending.ReturnURL, "error")
		return
	}
	tokens, err := m.exchange(r.Context(), code, redirectURI(), pending.Verifier)
	if err != nil {
		redirectAuthResult(w, r, pending.ReturnURL, "error")
		return
	}
	if err := m.saveTokensAndActivate(tokens); err != nil {
		redirectAuthResult(w, r, pending.ReturnURL, "error")
		return
	}
	redirectAuthResult(w, r, pending.ReturnURL, "connected")
}

func (m *Manager) stopCallbackServerIfIdle(parent context.Context) {
	m.mu.Lock()
	m.cleanupLocked(time.Now().UTC())
	if len(m.browser) != 0 || m.callbackServer == nil {
		m.mu.Unlock()
		return
	}
	server := m.callbackServer
	m.callbackServer = nil
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func (m *Manager) saveTokensAndActivate(tokens tokenResponse) error {
	m.mu.Lock()
	if err := m.storeTokensLocked(tokens); err != nil {
		m.mu.Unlock()
		return err
	}
	callback := m.onConnected
	m.mu.Unlock()
	if callback != nil {
		return callback()
	}
	return nil
}

func (m *Manager) storeTokensLocked(tokens tokenResponse) error {
	accessToken := strings.TrimSpace(tokens.AccessToken)
	refreshToken := strings.TrimSpace(tokens.RefreshToken)
	if refreshToken == "" {
		refreshToken = strings.TrimSpace(m.credentials.RefreshToken)
	}
	if accessToken == "" || refreshToken == "" {
		return errors.New("OpenAI did not return refreshable ChatGPT credentials")
	}
	accountID := extractAccountID(tokens)
	if accountID == "" {
		accountID = strings.TrimSpace(m.credentials.AccountID)
	}
	if accountID == "" {
		return errors.New("OpenAI did not return a ChatGPT account identifier")
	}
	expiresIn := time.Duration(tokens.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = defaultTokenLifetime
	}
	next := credentials{Version: credentialVersion, AccessToken: accessToken, RefreshToken: refreshToken, AccountID: accountID, ExpiresAt: time.Now().UTC().Add(expiresIn)}
	if m.path != "" {
		// This is the dedicated private credential file, not a response or log.
		data, err := json.MarshalIndent(next, "", "  ") //nolint:gosec // G117: persisting these fields is the file's purpose.
		if err != nil {
			return fmt.Errorf("encode ChatGPT credentials: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(m.path), 0o750); err != nil {
			return fmt.Errorf("create ChatGPT credential directory: %w", err)
		}
		if err := os.WriteFile(m.path, append(data, '\n'), 0o600); err != nil {
			return fmt.Errorf("write ChatGPT credential file: %w", err)
		}
	}
	m.credentials = next
	return nil
}

func (m *Manager) load() error {
	if m.path == "" {
		return nil
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read ChatGPT credential file: %w", err)
	}
	var stored credentials
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return fmt.Errorf("decode ChatGPT credential file: %w", err)
	}
	if stored.Version != credentialVersion {
		return fmt.Errorf("ChatGPT credential version must be %d", credentialVersion)
	}
	m.credentials = stored
	return nil
}

func (m *Manager) exchange(ctx context.Context, code, callbackURL, verifier string) (tokenResponse, error) {
	form := url.Values{
		"grant_type":      {"authorization_code"},
		"code":            {strings.TrimSpace(code)},
		"redirect_uri":    {callbackURL},
		clientIDParameter: {chatGPTClientID},
		"code_verifier":   {strings.TrimSpace(verifier)},
	}
	return m.tokenRequest(ctx, form)
}

func (m *Manager) refresh(ctx context.Context, refreshToken string) (tokenResponse, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {strings.TrimSpace(refreshToken)}, clientIDParameter: {chatGPTClientID}}
	return m.tokenRequest(ctx, form)
}

func (m *Manager) tokenRequest(ctx context.Context, form url.Values) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatGPTIssuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.http.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("call OpenAI authentication: %w", err)
	}
	data, readErr := readLimited(resp.Body)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	if readErr != nil {
		return tokenResponse{}, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return tokenResponse{}, authHTTPError("OpenAI token exchange", resp.StatusCode, data, form.Get("refresh_token"), form.Get("code"), form.Get("code_verifier"))
	}
	var tokens tokenResponse
	if err := json.Unmarshal(data, &tokens); err != nil {
		return tokenResponse{}, fmt.Errorf("decode OpenAI token response: %w", err)
	}
	return tokens, nil
}

func (m *Manager) postJSON(ctx context.Context, endpoint string, payload []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Alexa-Brainslug")
	resp, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("call OpenAI authentication: %w", err)
	}
	data, readErr := readLimited(resp.Body)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return authHTTPError("OpenAI authentication", resp.StatusCode, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode OpenAI authentication response: %w", err)
	}
	return nil
}

func (m *Manager) cleanupLocked(now time.Time) {
	for state, pending := range m.browser {
		if now.After(pending.ExpiresAt) {
			delete(m.browser, state)
		}
	}
	for id, pending := range m.device {
		if now.After(pending.ExpiresAt) {
			delete(m.device, id)
		}
	}
}

func newPKCE() (verifier, challenge string, err error) {
	verifier, err = randomToken(32)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func redirectURI() string { return "http://" + callbackAddress + callbackPath }

func validateReturnURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return "", errors.New("OpenAI login return URL must be a local HTTP URL")
	}
	host := parsed.Hostname()
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("OpenAI browser login is available only from the local dashboard")
		}
	}
	parsed.Fragment = ""
	return parsed.String(), nil
}

func redirectAuthResult(w http.ResponseWriter, r *http.Request, returnURL, result string) {
	parsed, err := url.Parse(returnURL)
	if err != nil {
		http.Error(w, "OpenAI login completed; return to Brainslug.", http.StatusOK)
		return
	}
	query := parsed.Query()
	query.Set("section", "inference")
	query.Set("provider", "openai")
	query.Set("auth", result)
	parsed.RawQuery = query.Encode()
	http.Redirect(w, r, parsed.String(), http.StatusSeeOther)
}

func extractAccountID(tokens tokenResponse) string {
	for _, raw := range []string{tokens.IDToken, tokens.AccessToken} {
		parts := strings.Split(raw, ".")
		if len(parts) != 3 {
			continue
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			continue
		}
		var claims map[string]any
		if json.Unmarshal(payload, &claims) != nil {
			continue
		}
		if value := stringClaim(claims, "chatgpt_account_id"); value != "" {
			return value
		}
		if nested, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
			if value := stringClaim(nested, "chatgpt_account_id"); value != "" {
				return value
			}
		}
		if organizations, ok := claims["organizations"].([]any); ok && len(organizations) > 0 {
			if first, ok := organizations[0].(map[string]any); ok {
				if value := stringClaim(first, "id"); value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func stringClaim(claims map[string]any, name string) string {
	value, _ := claims[name].(string)
	return strings.TrimSpace(value)
}

func parseInterval(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		seconds = 5
	}
	if seconds > 30 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}

func readLimited(body io.Reader) ([]byte, error) {
	if body == nil {
		return nil, errors.New("OpenAI authentication response body is missing")
	}
	data, err := io.ReadAll(io.LimitReader(body, maximumAuthBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read OpenAI authentication response: %w", err)
	}
	if len(data) > maximumAuthBodyBytes {
		return nil, errors.New("OpenAI authentication response is too large")
	}
	return data, nil
}

func authHTTPError(action string, status int, data []byte, secrets ...string) error {
	var envelope struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		Message          string `json:"message"`
	}
	_ = json.Unmarshal(data, &envelope)
	message := strings.TrimSpace(envelope.ErrorDescription)
	if message == "" {
		message = strings.TrimSpace(envelope.Message)
	}
	if message == "" {
		message = strings.TrimSpace(envelope.Error)
	}
	if message == "" {
		return fmt.Errorf("%s failed with HTTP %d", action, status)
	}
	message = security.RedactString(message, secrets...)
	return fmt.Errorf("%s failed with HTTP %d: %s", action, status, message)
}

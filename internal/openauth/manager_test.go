package openauth

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestStartBrowserBuildsPKCEForLocalReturnOnly(t *testing.T) {
	manager, err := New(filepath.Join(t.TempDir(), "openai.auth.json"), &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })

	authURL, err := manager.StartBrowser("http://127.0.0.1:8081/?section=inference")
	if err != nil {
		t.Fatalf("StartBrowser() error = %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "auth.openai.com" || parsed.Query().Get("code_challenge_method") != "S256" || parsed.Query().Get("state") == "" {
		t.Fatalf("authorization URL = %s", authURL)
	}
	if _, err := manager.StartBrowser("https://example.com/return"); err == nil {
		t.Fatal("remote return URL was accepted")
	}
}

func TestBrowserCallbackStoresCredentialRedirectsAndRejectsReplay(t *testing.T) {
	jwt := testJWT(`{"chatgpt_account_id":"account-browser"}`)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/oauth/token" {
			t.Fatalf("token request path = %q", request.URL.Path)
		}
		body := `{"access_token":"access-browser","id_token":"` + jwt + `","refresh_token":"refresh-browser","expires_in":3600}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	manager, err := New(filepath.Join(t.TempDir(), "openai.auth.json"), client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	authURL, err := manager.StartBrowser("http://127.0.0.1:8081/?section=inference")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	callbackURL := "http://localhost:1455/auth/callback?code=code-browser&state=" + url.QueryEscape(parsed.Query().Get("state"))
	recorder := httptest.NewRecorder()
	manager.handleCallback(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, callbackURL, http.NoBody))
	if recorder.Code != http.StatusSeeOther || !strings.Contains(recorder.Header().Get("Location"), "auth=connected") || !manager.Snapshot().Connected {
		t.Fatalf("callback response = %d %q, connected=%v", recorder.Code, recorder.Header().Get("Location"), manager.Snapshot().Connected)
	}
	replay := httptest.NewRecorder()
	manager.handleCallback(replay, httptest.NewRequestWithContext(t.Context(), http.MethodGet, callbackURL, http.NoBody))
	if replay.Code != http.StatusBadRequest {
		t.Fatalf("replayed callback status = %d", replay.Code)
	}
}

func TestDeviceFlowStoresRefreshableCredentialAndActivates(t *testing.T) {
	var activated atomic.Bool
	var tokenCalls atomic.Int32
	jwt := testJWT(`{"chatgpt_account_id":"account-test"}`)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		status := http.StatusOK
		var body string
		switch request.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			body = `{"device_auth_id":"device-1","user_code":"ABCD-EFGH","interval":"1"}`
		case "/api/accounts/deviceauth/token":
			body = `{"authorization_code":"code-1","code_verifier":"verifier-1"}`
		case "/oauth/token":
			if tokenCalls.Add(1) == 1 {
				body = `{"access_token":"access-old","id_token":"` + jwt + `","refresh_token":"refresh-test","expires_in":1}`
			} else {
				body = `{"access_token":"access-new","id_token":"` + jwt + `","expires_in":3600}`
			}
		default:
			status = http.StatusNotFound
			body = `{}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	path := filepath.Join(t.TempDir(), "brainslug.openai-auth.json")
	manager, err := New(path, client)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetOnConnected(func() error { activated.Store(true); return nil })

	flow, err := manager.StartDevice(t.Context())
	if err != nil {
		t.Fatalf("StartDevice() error = %v", err)
	}
	if flow.UserCode != "ABCD-EFGH" || flow.VerificationURL != deviceVerificationURL {
		t.Fatalf("flow = %#v", flow)
	}
	result, err := manager.PollDevice(t.Context(), flow.ID)
	if err != nil || result != PollConnected {
		t.Fatalf("PollDevice() = %q, %v", result, err)
	}
	if !activated.Load() || !manager.Snapshot().Connected {
		t.Fatal("connection was not activated")
	}
	access, account, err := manager.Token(t.Context())
	if err != nil || access != "access-new" || account != "account-test" {
		t.Fatalf("Token() = %q, %q, %v", access, account, err)
	}
	if tokenCalls.Load() != 2 {
		t.Fatalf("token endpoint calls = %d, want exchange and refresh", tokenCalls.Load())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "refresh-test") || strings.Contains(string(data), "device-1") {
		t.Fatalf("credential file has unexpected content: %s", data)
	}
	if err := manager.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("credential file remains after disconnect: %v", err)
	}
}

func testJWT(payload string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
}

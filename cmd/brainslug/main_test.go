package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/adminauth"
	"github.com/wellivea1/alexa-brainslug/internal/alexa"
	"github.com/wellivea1/alexa-brainslug/internal/app"
	"github.com/wellivea1/alexa-brainslug/internal/config"
	"github.com/wellivea1/alexa-brainslug/internal/inference"
	"github.com/wellivea1/alexa-brainslug/internal/providers"
	"github.com/wellivea1/alexa-brainslug/internal/store"
)

type fakeInferenceController struct {
	snapshot inference.Snapshot
}

func (f *fakeInferenceController) Snapshot() inference.Snapshot  { return f.snapshot }
func (f *fakeInferenceController) Update(inference.Update) error { return nil }

func TestPublicHandlerExposesOnlyAlexa(t *testing.T) {
	alexaHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := buildPublicHandler("", alexaHandler)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /alexa status = %d", rec.Code)
	}

	for _, path := range []string{"/", "/healthz", "/health", "/history", "/setup", "/login", "/logout", "/assets/dashboard.css", "/assets/dashboard.js", "/settings/provider", "/auth/provider/api-key", "/auth/provider/api-key/remove", "/auth/openai/browser", "/auth/openai/device/start", "/auth/openai/device/poll", "/auth/openai/disconnect"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Errorf("GET %s status = %d, want %d", path, rec.Code, http.StatusNotFound)
			}
		})
	}
}

func TestAdminHandlerDoesNotExposeAlexa(t *testing.T) {
	directory := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(directory, "brainslug.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("store.Close() error = %v", err)
		}
	})

	cfg := config.Config{
		Provider: config.ProviderOpenAI,
		OpenAI: config.OpenAIConfig{
			Model: "gpt-test",
		},
	}
	controller := &fakeInferenceController{snapshot: inference.Snapshot{
		ActiveProvider: config.ProviderOpenAI,
		OpenAI:         inference.ProviderState{ID: config.ProviderOpenAI, Model: "gpt-test"},
	}}
	managementAuth, err := adminauth.New(filepath.Join(directory, "brainslug.admin-auth.json"), adminauth.Options{PasswordIterations: 1_000})
	if err != nil {
		t.Fatal(err)
	}
	handler := buildAdminHandler(&cfg, controller, db, time.Now().UTC(), nil, managementAuth)

	for _, path := range []string{"/healthz", "/assets/dashboard.css", "/assets/dashboard.js"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("GET %s status = %d, body = %s", path, rec.Code, rec.Body.String())
			}
		})
	}

	for _, path := range []string{"/health", "/history"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated GET %s status = %d", path, rec.Code)
		}
	}
	root := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	rootRec := httptest.NewRecorder()
	handler.ServeHTTP(rootRec, root)
	if rootRec.Code != http.StatusSeeOther || rootRec.Header().Get("Location") != "/setup" {
		t.Fatalf("fresh root response = %d %q", rootRec.Code, rootRec.Header().Get("Location"))
	}

	form := url.Values{
		"csrf_token":            {managementAuth.CSRFToken()},
		"username":              {"owner"},
		"password":              {"correct horse battery staple"},
		"password_confirmation": {"correct horse battery staple"},
	}
	setup := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/setup", strings.NewReader(form.Encode()))
	setup.Host = "127.0.0.1:8081"
	setup.RemoteAddr = "127.0.0.1:12345"
	setup.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setup.Header.Set("Origin", "http://127.0.0.1:8081")
	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, setup)
	if setupRec.Code != http.StatusSeeOther {
		t.Fatalf("setup status = %d, body = %s", setupRec.Code, setupRec.Body.String())
	}
	cookies := setupRec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("setup cookies = %#v", cookies)
	}

	for _, path := range []string{"/", "/health", "/history"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
		req.AddCookie(cookies[0])
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("authenticated GET %s status = %d, body = %s", path, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("POST /alexa status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAlexaQuestionRoundTripThroughProviderAndHistory(t *testing.T) {
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" {
			t.Errorf("provider path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("provider authorization header is missing")
		}

		var payload struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		if payload.Model != "gpt-test" || payload.Input != "what is the test answer" {
			t.Errorf("provider payload = %+v", payload)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"model": "gpt-test-2026",
			"status": "completed",
			"output_text": "The end-to-end answer is ready."
		}`)
	}))
	t.Cleanup(providerServer.Close)

	database, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "round-trip.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("store.Close() error = %v", err)
		}
	})

	provider := providers.NewOpenAI(config.OpenAIConfig{
		APIKey:          "test-key",
		BaseURL:         providerServer.URL + "/v1",
		Model:           "gpt-test",
		MaxOutputTokens: 256,
		Timeout:         5 * time.Second,
	}, providerServer.Client())
	application := app.NewService(app.Options{
		Model:           "gpt-test",
		MaxOutputTokens: 256,
		SaveHistory:     true,
	}, providers.AsAppProvider(provider), store.AsAppRepository(database))
	const skillID = "amzn1.ask.skill.round-trip"
	alexaHandler := alexa.NewHandler(alexa.Options{Verification: alexa.VerificationConfig{
		VerifyRequests: false,
		AllowedSkillID: skillID,
	}}, application)
	publicHandler := buildPublicHandler("", alexaHandler)

	question := `{
		"version": "1.0",
		"session": {
			"sessionId": "session-round-trip",
			"application": {"applicationId": "amzn1.ask.skill.round-trip"},
			"user": {"userId": "user-round-trip"}
		},
		"request": {
			"type": "IntentRequest",
			"intent": {
				"name": "AskGPTIntent",
				"slots": {"Query": {"name": "Query", "value": "what is the test answer"}}
			}
		}
	}`
	response := postPublicAlexa(t, publicHandler, question)
	if response.Response.OutputSpeech == nil || response.Response.OutputSpeech.Text != "The end-to-end answer is ready." {
		t.Fatalf("question speech = %#v", response.Response.OutputSpeech)
	}

	saved, err := database.GetLatestResponse(t.Context(), "user-round-trip")
	if err != nil {
		t.Fatalf("GetLatestResponse() error = %v", err)
	}
	if saved.Prompt != "what is the test answer" || saved.Model != "gpt-test-2026" || saved.Status != app.StatusOK {
		t.Errorf("saved interaction = %+v", saved)
	}

	history := `{
		"version": "1.0",
		"session": {
			"application": {"applicationId": "amzn1.ask.skill.round-trip"},
			"user": {"userId": "user-round-trip"}
		},
		"request": {
			"type": "IntentRequest",
			"intent": {"name": "LastResponseIntent"}
		}
	}`
	historyResponse := postPublicAlexa(t, publicHandler, history)
	if historyResponse.Response.OutputSpeech == nil || historyResponse.Response.OutputSpeech.Text != "The end-to-end answer is ready." {
		t.Fatalf("history speech = %#v", historyResponse.Response.OutputSpeech)
	}
}

func postPublicAlexa(t *testing.T, handler http.Handler, body string) alexa.ResponseEnvelope {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("POST /alexa status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response alexa.ResponseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode Alexa response: %v", err)
	}
	return response
}

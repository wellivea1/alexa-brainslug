package alexa

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/providers"
	"github.com/wellivea1/alexa-brainslug/internal/store"
)

type fakeProvider struct {
	request providers.Request
	err     error
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) Ask(_ context.Context, req providers.Request) (providers.Response, error) {
	p.request = req
	if p.err != nil {
		return providers.Response{}, p.err
	}
	return providers.Response{
		ShortAnswer:  "Go is a programming language.",
		FullAnswer:   "Go is a statically typed programming language designed for reliable services.",
		Model:        req.Model,
		ProviderName: "fake",
		Latency:      25 * time.Millisecond,
	}, nil
}

type fakeStore struct {
	saved   []store.Interaction
	latest  *store.Interaction
	loadErr error
}

func (s *fakeStore) SaveInteraction(_ context.Context, interaction store.Interaction) (int64, error) {
	s.saved = append(s.saved, interaction)
	return int64(len(s.saved)), nil
}

func (s *fakeStore) GetLatestResponse(_ context.Context, _ string) (*store.Interaction, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if s.latest == nil {
		return nil, store.ErrNotFound
	}
	return s.latest, nil
}

func testOptions() Options {
	return Options{
		Model:           "gpt-test",
		MaxOutputTokens: 128,
		SaveHistory:     true,
		Verification: VerificationConfig{
			VerifyRequests: false,
			AllowedSkillID: "amzn1.ask.skill.test",
		},
	}
}

const askIntentBody = `{
	"version": "1.0",
	"session": {
		"sessionId": "session-1",
		"application": {"applicationId": "amzn1.ask.skill.test"},
		"user": {"userId": "user-1"}
	},
	"context": {
		"System": {
			"application": {"applicationId": "amzn1.ask.skill.test"},
			"user": {"userId": "user-1"}
		}
	},
	"request": {
		"type": "IntentRequest",
		"intent": {
			"name": "AskGPTIntent",
			"slots": {"Query": {"name": "Query", "value": "what is go"}}
		}
	}
}`

func postAlexa(t *testing.T, handler *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) ResponseEnvelope {
	t.Helper()
	var resp ResponseEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON error = %v, body = %s", err, rec.Body.String())
	}
	return resp
}

func TestHandlerRoutesAskGPTIntent(t *testing.T) {
	provider := &fakeProvider{}
	history := &fakeStore{}
	handler := NewHandler(testOptions(), provider, history)

	rec := postAlexa(t, handler, askIntentBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if got := resp.Response.OutputSpeech.Text; got != "Go is a programming language." {
		t.Errorf("speech = %q", got)
	}
	if !resp.Response.ShouldEndSession {
		t.Error("an answered question should end the session")
	}
	if provider.request.Prompt != "what is go" {
		t.Errorf("provider prompt = %q", provider.request.Prompt)
	}
	if provider.request.Model != "gpt-test" {
		t.Errorf("provider model = %q", provider.request.Model)
	}
	if len(history.saved) != 1 {
		t.Fatalf("saved interactions = %d", len(history.saved))
	}
	saved := history.saved[0]
	if saved.Status != statusOK || saved.LatencyMS != 25 {
		t.Errorf("saved interaction = %+v", saved)
	}
	if saved.FullAnswer == "" {
		t.Error("the full answer should be stored even though only the short one is spoken")
	}
}

func TestHandlerRoutesLastResponseIntent(t *testing.T) {
	history := &fakeStore{latest: &store.Interaction{ShortAnswer: "previous answer"}}
	handler := NewHandler(testOptions(), &fakeProvider{}, history)

	resp := handler.Route(t.Context(), &RequestEnvelope{
		Session: Session{SessionID: "session-1", User: User{UserID: "user-1"}},
		Request: RequestPayload{
			Type:   requestTypeIntent,
			Intent: &Intent{Name: IntentLastResponse},
		},
	})

	if got := resp.Response.OutputSpeech.Text; got != "previous answer" {
		t.Errorf("speech = %q", got)
	}
}

// Verification failures must answer 400. Amazon specifies that status for a
// request whose signature or certificate does not validate.
func TestHandlerFailsClosedWhenVerificationEnabled(t *testing.T) {
	opts := testOptions()
	opts.Verification.VerifyRequests = true
	handler := NewHandler(opts, &fakeProvider{}, &fakeStore{})

	rec := postAlexa(t, handler, `{
		"version": "1.0",
		"session": {
			"application": {"applicationId": "amzn1.ask.skill.test"},
			"user": {"userId": "user-1"}
		},
		"request": {"type": "LaunchRequest"}
	}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerRejectsForeignSkillID(t *testing.T) {
	handler := NewHandler(testOptions(), &fakeProvider{}, &fakeStore{})

	rec := postAlexa(t, handler, `{
		"version": "1.0",
		"session": {
			"application": {"applicationId": "amzn1.ask.skill.someone-else"},
			"user": {"userId": "user-1"}
		},
		"request": {"type": "LaunchRequest"}
	}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

// A provider failure must still be recorded, so that a silent outage is
// visible in history rather than only in the logs.
func TestHandlerRecordsProviderFailure(t *testing.T) {
	provider := &fakeProvider{err: errors.New("boom")}
	history := &fakeStore{}
	handler := NewHandler(testOptions(), provider, history)

	rec := postAlexa(t, handler, askIntentBody)
	resp := decodeResponse(t, rec)

	if got := resp.Response.OutputSpeech.Text; got != speechProviderError {
		t.Errorf("speech = %q", got)
	}
	if len(history.saved) != 1 {
		t.Fatalf("saved interactions = %d", len(history.saved))
	}
	if history.saved[0].Status != statusError {
		t.Errorf("status = %q", history.saved[0].Status)
	}
}

// The spoken message has to distinguish the failures a listener can act on.
func TestSpeechForProviderError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"unconfigured", providers.ErrNotConfigured, speechProviderMisconfig},
		{"wrapped unconfigured", errors.Join(errors.New("startup"), providers.ErrNotConfigured), speechProviderMisconfig},
		{"no text output", providers.ErrEmptyResponse, speechProviderNoText},
		{"timeout", context.DeadlineExceeded, speechProviderBusy},
		{"rate limited", &providers.APIError{Provider: "openai", Status: 429}, speechProviderBusy},
		{"server error", &providers.APIError{Provider: "openai", Status: 503}, speechProviderBusy},
		{"rejected request", &providers.APIError{Provider: "openai", Status: 400}, speechProviderError},
		{"unknown", errors.New("something else"), speechProviderError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := speechForProviderError(tt.err); got != tt.want {
				t.Errorf("speechForProviderError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestHandlerAsksForAQuestionWhenSlotIsEmpty(t *testing.T) {
	provider := &fakeProvider{}
	handler := NewHandler(testOptions(), provider, &fakeStore{})

	resp := handler.Route(t.Context(), &RequestEnvelope{
		Request: RequestPayload{
			Type:   requestTypeIntent,
			Intent: &Intent{Name: IntentAskGPT},
		},
	})

	if resp.Response.ShouldEndSession {
		t.Error("the session should stay open so the user can answer")
	}
	if provider.request.Prompt != "" {
		t.Error("the provider must not be called without a question")
	}
}

func TestHandlerRoutesRequestTypes(t *testing.T) {
	tests := []struct {
		name       string
		envelope   RequestEnvelope
		endSession bool
	}{
		{
			"launch keeps the session open",
			RequestEnvelope{Request: RequestPayload{Type: requestTypeLaunch}},
			false,
		},
		{
			"session ended",
			RequestEnvelope{Request: RequestPayload{Type: requestTypeSessionEnded}},
			true,
		},
		{
			"stop ends the session",
			RequestEnvelope{Request: RequestPayload{Type: requestTypeIntent, Intent: &Intent{Name: IntentStop}}},
			true,
		},
		{
			"help keeps the session open",
			RequestEnvelope{Request: RequestPayload{Type: requestTypeIntent, Intent: &Intent{Name: IntentHelp}}},
			false,
		},
		{
			"fallback keeps the session open",
			RequestEnvelope{Request: RequestPayload{Type: requestTypeIntent, Intent: &Intent{Name: IntentFallback}}},
			false,
		},
		{
			"unknown request type keeps the session open",
			RequestEnvelope{Request: RequestPayload{Type: "SomethingNew"}},
			false,
		},
	}

	handler := NewHandler(testOptions(), &fakeProvider{}, &fakeStore{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := handler.Route(t.Context(), &tt.envelope)
			if resp.Response.ShouldEndSession != tt.endSession {
				t.Errorf("ShouldEndSession = %v, want %v", resp.Response.ShouldEndSession, tt.endSession)
			}
			if resp.Response.OutputSpeech == nil || resp.Response.OutputSpeech.Text == "" {
				t.Error("every response must say something")
			}
		})
	}
}

func TestHandlerHonoursSaveHistoryDisabled(t *testing.T) {
	opts := testOptions()
	opts.SaveHistory = false
	history := &fakeStore{}
	handler := NewHandler(opts, &fakeProvider{}, history)

	postAlexa(t, handler, askIntentBody)

	if len(history.saved) != 0 {
		t.Errorf("saved %d interactions with history disabled", len(history.saved))
	}
}

func TestHandlerRejectsNonPost(t *testing.T) {
	handler := NewHandler(testOptions(), &fakeProvider{}, &fakeStore{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/alexa", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandlerRejectsMalformedJSON(t *testing.T) {
	handler := NewHandler(testOptions(), &fakeProvider{}, &fakeStore{})

	rec := postAlexa(t, handler, `{"version": `)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

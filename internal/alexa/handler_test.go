package alexa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alexa-brainslug/internal/config"
	"alexa-brainslug/internal/providers"
	"alexa-brainslug/internal/store"
)

type fakeProvider struct {
	request providers.Request
	err     error
}

func (p *fakeProvider) Name() string {
	return "fake"
}

func (p *fakeProvider) Ask(ctx context.Context, req providers.Request) (providers.Response, error) {
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
	saved  []store.Interaction
	latest *store.Interaction
}

func (s *fakeStore) SaveInteraction(ctx context.Context, interaction store.Interaction) (int64, error) {
	s.saved = append(s.saved, interaction)
	return int64(len(s.saved)), nil
}

func (s *fakeStore) GetLatestResponse(ctx context.Context, userID string) (*store.Interaction, error) {
	if s.latest == nil {
		return nil, store.ErrNotFound
	}
	return s.latest, nil
}

func TestHandlerRoutesAskGPTIntent(t *testing.T) {
	provider := &fakeProvider{}
	history := &fakeStore{}
	handler := NewHandler(testConfig(), provider, history)

	req := httptest.NewRequest(http.MethodPost, "/alexa", strings.NewReader(`{
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
				"slots": {
					"Query": {"name": "Query", "value": "what is go"}
				}
			}
		}
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var resp ResponseEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON error = %v", err)
	}
	if resp.Response.OutputSpeech.Text != "Go is a programming language." {
		t.Fatalf("speech = %q", resp.Response.OutputSpeech.Text)
	}
	if provider.request.Prompt != "what is go" {
		t.Fatalf("provider prompt = %q", provider.request.Prompt)
	}
	if provider.request.Model != "gpt-test" {
		t.Fatalf("provider model = %q", provider.request.Model)
	}
	if len(history.saved) != 1 {
		t.Fatalf("saved interactions = %d", len(history.saved))
	}
	if history.saved[0].Status != "ok" || history.saved[0].LatencyMS != 25 {
		t.Fatalf("saved interaction = %+v", history.saved[0])
	}
}

func TestHandlerRoutesLastResponseIntent(t *testing.T) {
	history := &fakeStore{latest: &store.Interaction{ShortAnswer: "previous answer"}}
	handler := NewHandler(testConfig(), &fakeProvider{}, history)

	resp := handler.Route(context.Background(), RequestEnvelope{
		Session: Session{
			SessionID: "session-1",
			User:      User{UserID: "user-1"},
		},
		Request: RequestPayload{
			Type:   "IntentRequest",
			Intent: &Intent{Name: IntentLastResponse},
		},
	})

	if resp.Response.OutputSpeech.Text != "previous answer" {
		t.Fatalf("speech = %q", resp.Response.OutputSpeech.Text)
	}
}

func TestHandlerFailsClosedWhenVerificationEnabled(t *testing.T) {
	cfg := testConfig()
	cfg.AlexaVerifyRequests = true
	handler := NewHandler(cfg, &fakeProvider{}, &fakeStore{})

	req := httptest.NewRequest(http.MethodPost, "/alexa", strings.NewReader(`{
		"version": "1.0",
		"session": {
			"application": {"applicationId": "amzn1.ask.skill.test"},
			"user": {"userId": "user-1"}
		},
		"request": {"type": "LaunchRequest"}
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func testConfig() config.Config {
	return config.Config{
		ServerAddr: ":8080",
		OpenAI: config.OpenAIConfig{
			Model:           "gpt-test",
			MaxOutputTokens: 128,
			Timeout:         time.Second,
		},
		SQLitePath:          ":memory:",
		AlexaVerifyRequests: false,
		AlexaAllowedSkillID: "amzn1.ask.skill.test",
		SaveHistory:         true,
	}
}

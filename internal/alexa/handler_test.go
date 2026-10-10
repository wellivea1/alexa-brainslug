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

	"github.com/wellivea1/alexa-brainslug/internal/app"
)

type fakeApplication struct {
	askRequest      app.AskRequest
	askAnswer       app.Answer
	askErr          error
	askDeadline     time.Time
	askHasDeadline  bool
	latestPrincipal app.Principal
	latestAnswer    app.Answer
	latestErr       error
}

func (a *fakeApplication) Ask(ctx context.Context, req app.AskRequest) (app.Answer, error) {
	a.askRequest = req
	a.askDeadline, a.askHasDeadline = ctx.Deadline()
	if a.askErr != nil {
		return app.Answer{}, a.askErr
	}
	if a.askAnswer.FullAnswer == "" {
		return app.Answer{FullAnswer: "Go is a programming language."}, nil
	}
	return a.askAnswer, nil
}

func (a *fakeApplication) LatestAnswer(_ context.Context, principal app.Principal) (app.Answer, error) {
	a.latestPrincipal = principal
	if a.latestErr != nil {
		return app.Answer{}, a.latestErr
	}
	return a.latestAnswer, nil
}

func testOptions() Options {
	return Options{Verification: VerificationConfig{
		VerifyRequests: false,
		AllowedSkillID: "amzn1.ask.skill.test",
	}}
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
	application := &fakeApplication{}
	handler := NewHandler(testOptions(), application)

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
	if application.askRequest.Prompt != "what is go" {
		t.Errorf("application prompt = %q", application.askRequest.Prompt)
	}
	if application.askRequest.ConversationID != "session-1" {
		t.Errorf("conversation ID = %q", application.askRequest.ConversationID)
	}
	if application.askRequest.SystemPrompt != voiceSystemPrompt {
		t.Errorf("Alexa instructions = %q", application.askRequest.SystemPrompt)
	}
	wantPrincipal := app.Principal{Namespace: "alexa", Subject: "user-1"}
	if application.askRequest.Principal != wantPrincipal {
		t.Errorf("principal = %+v, want %+v", application.askRequest.Principal, wantPrincipal)
	}
}

// The application runs under one deadline set when the request arrives, so
// the provider cannot run past the point where Alexa has already given up.
func TestHandlerAppliesResponseBudget(t *testing.T) {
	application := &fakeApplication{}
	handler := NewHandler(testOptions(), application)

	before := time.Now()
	postAlexa(t, handler, askIntentBody)
	after := time.Now()

	if !application.askHasDeadline {
		t.Fatal("application context has no deadline")
	}
	if application.askDeadline.Before(before.Add(ResponseBudget)) || application.askDeadline.After(after.Add(ResponseBudget)) {
		t.Errorf("deadline = %v, want %v after the request arrived", application.askDeadline, ResponseBudget)
	}
}

func TestHandlerRoutesLastResponseIntent(t *testing.T) {
	application := &fakeApplication{latestAnswer: app.Answer{FullAnswer: "previous answer"}}
	handler := NewHandler(testOptions(), application)

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
	if application.latestPrincipal.Subject != "user-1" {
		t.Errorf("latest principal = %+v", application.latestPrincipal)
	}
}

func TestHandlerShapesNewAndSavedAnswersForVoice(t *testing.T) {
	full := "# The answer\n\n**Keep the useful conclusion.** " + strings.Repeat("More detail follows. ", 100)
	application := &fakeApplication{
		askAnswer: app.Answer{FullAnswer: full}, latestAnswer: app.Answer{FullAnswer: full},
	}
	handler := NewHandler(testOptions(), application)
	newAnswer := decodeResponse(t, postAlexa(t, handler, askIntentBody)).Response.OutputSpeech.Text
	recalled := handler.Route(t.Context(), &RequestEnvelope{
		Session: Session{User: User{UserID: "user-1"}},
		Request: RequestPayload{Type: requestTypeIntent, Intent: &Intent{Name: IntentLastResponse}},
	}).Response.OutputSpeech.Text
	if newAnswer != recalled {
		t.Fatalf("new answer and recall differ: %q / %q", newAnswer, recalled)
	}
	if !strings.HasPrefix(newAnswer, "The answer\n\nKeep the useful conclusion.") &&
		!strings.HasPrefix(newAnswer, "The answer Keep the useful conclusion.") {
		t.Fatalf("the useful conclusion was lost: %q", newAnswer)
	}
	if strings.ContainsAny(newAnswer, "#*") || len(strings.Fields(newAnswer)) > 126 || !strings.HasSuffix(newAnswer, "That is the short version.") {
		t.Fatalf("answer was not shaped for voice: %q", newAnswer)
	}
	if application.askAnswer.FullAnswer != full || application.askRequest.SystemPrompt != voiceSystemPrompt {
		t.Fatal("Alexa must supply voice instructions without changing the full answer")
	}
}

// Verification failures must answer 400. Amazon specifies that status for a
// request whose signature or certificate does not validate.
func TestHandlerFailsClosedWhenVerificationEnabled(t *testing.T) {
	opts := testOptions()
	opts.Verification.VerifyRequests = true
	handler := NewHandler(opts, &fakeApplication{})

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
	handler := NewHandler(testOptions(), &fakeApplication{})

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

func TestHandlerMapsApplicationFailure(t *testing.T) {
	application := &fakeApplication{askErr: app.ErrProviderFailed}
	handler := NewHandler(testOptions(), application)

	rec := postAlexa(t, handler, askIntentBody)
	resp := decodeResponse(t, rec)

	if got := resp.Response.OutputSpeech.Text; got != speechProviderError {
		t.Errorf("speech = %q", got)
	}
}

func TestSpeechForApplicationError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"unconfigured", app.ErrProviderNotConfigured, speechProviderMisconfig},
		{"wrapped unconfigured", errors.Join(errors.New("startup"), app.ErrProviderNotConfigured), speechProviderMisconfig},
		{"no text output", app.ErrProviderNoText, speechProviderNoText},
		{"unavailable", app.ErrProviderUnavailable, speechProviderBusy},
		{"timeout", context.DeadlineExceeded, speechProviderBusy},
		{"unknown", errors.New("something else"), speechProviderError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := speechForApplicationError(tt.err); got != tt.want {
				t.Errorf("speechForApplicationError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestHandlerAsksForAQuestionWhenSlotIsEmpty(t *testing.T) {
	tests := []struct {
		name         string
		dialogState  string
		wantDelegate bool
	}{
		{"no dialog state re-enters the dialog", "", true},
		{"in-progress dialog re-enters the dialog", "IN_PROGRESS", true},
		// Alexa rejects a delegate back into a COMPLETED dialog's own intent.
		{"completed dialog asks without a directive", dialogStateCompleted, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			application := &fakeApplication{}
			handler := NewHandler(testOptions(), application)

			resp := handler.Route(t.Context(), &RequestEnvelope{
				Session: Session{User: User{UserID: "user-1"}},
				Request: RequestPayload{
					Type:        requestTypeIntent,
					DialogState: tt.dialogState,
					Intent:      &Intent{Name: IntentAskGPT},
				},
			})

			if resp.Response.ShouldEndSession {
				t.Error("the session should stay open so the user can answer")
			}
			if application.askRequest.Prompt != "" {
				t.Error("the application must not be called without a question")
			}
			gotDelegate := len(resp.Response.Directives) == 1 &&
				resp.Response.Directives[0].Type == directiveDialogDelegate &&
				resp.Response.Directives[0].UpdatedIntent.Name == IntentAskGPT
			if gotDelegate != tt.wantDelegate {
				t.Errorf("delegate = %v, want %v (directives %+v)", gotDelegate, tt.wantDelegate, resp.Response.Directives)
			}
		})
	}
}

func TestHandlerAnswersRetrievalPhraseFromHistory(t *testing.T) {
	application := &fakeApplication{latestAnswer: app.Answer{FullAnswer: "previous answer"}}
	handler := NewHandler(testOptions(), application)

	// "Last response" spoken in answer to "What should I ask?" can arrive as
	// the Query slot of AskGPTIntent instead of as LastResponseIntent.
	resp := handler.Route(t.Context(), &RequestEnvelope{
		Session: Session{User: User{UserID: "user-1"}},
		Request: RequestPayload{
			Type: requestTypeIntent,
			Intent: &Intent{
				Name:  IntentAskGPT,
				Slots: map[string]Slot{slotQuery: {Name: slotQuery, Value: "Last response."}},
			},
		},
	})

	if got := resp.Response.OutputSpeech.Text; got != "previous answer" {
		t.Errorf("speech = %q, want the saved answer", got)
	}
	if application.askRequest.Prompt != "" {
		t.Errorf("a retrieval phrase must not be sent to the provider: %+v", application.askRequest)
	}
	if application.latestPrincipal.Subject != "user-1" {
		t.Errorf("latest principal = %+v", application.latestPrincipal)
	}
}

func TestIsLastResponseRequest(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"last response", true},
		{"  The LAST answer?  ", true},
		{"what was the last answer", true},
		{"what is the last answer in a game of jeopardy", false},
		{"why is the sky blue", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isLastResponseRequest(tt.query); got != tt.want {
			t.Errorf("isLastResponseRequest(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestHandlerMapsLatestAnswerStates(t *testing.T) {
	tests := []struct {
		name   string
		answer app.Answer
		err    error
		want   string
	}{
		{"no history", app.Answer{}, app.ErrNoHistory, speechNoHistory},
		{"empty saved answer", app.Answer{}, app.ErrEmptySavedAnswer, speechEmptyPrior},
		{"history failure", app.Answer{}, errors.New("disk failure"), speechHistoryError},
		{"full answer fallback", app.Answer{FullAnswer: "full answer"}, nil, "full answer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			application := &fakeApplication{latestAnswer: tt.answer, latestErr: tt.err}
			handler := NewHandler(testOptions(), application)
			resp := handler.Route(t.Context(), &RequestEnvelope{
				Session: Session{User: User{UserID: "user-1"}},
				Request: RequestPayload{Type: requestTypeIntent, Intent: &Intent{Name: IntentLastResponse}},
			})
			if got := resp.Response.OutputSpeech.Text; got != tt.want {
				t.Errorf("speech = %q, want %q", got, tt.want)
			}
		})
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

	handler := NewHandler(testOptions(), &fakeApplication{})
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

func TestHandlerRejectsNonPost(t *testing.T) {
	handler := NewHandler(testOptions(), &fakeApplication{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/alexa", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandlerRejectsMalformedJSON(t *testing.T) {
	handler := NewHandler(testOptions(), &fakeApplication{})

	rec := postAlexa(t, handler, `{"version": `)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

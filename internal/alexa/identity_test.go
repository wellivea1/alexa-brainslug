package alexa

import (
	"testing"

	"github.com/wellivea1/alexa-brainslug/internal/app"
)

func TestRequestEnvelopeUserID(t *testing.T) {
	tests := []struct {
		name     string
		envelope RequestEnvelope
		want     string
	}{
		{
			name:     "session user",
			envelope: RequestEnvelope{Session: Session{User: User{UserID: " session-user "}}},
			want:     "session-user",
		},
		{
			name: "context user",
			envelope: RequestEnvelope{Context: Context{System: System{
				User: User{UserID: " context-user "},
			}}},
			want: "context-user",
		},
		{
			name:     "missing user",
			envelope: RequestEnvelope{},
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.envelope.UserID(); got != tt.want {
				t.Errorf("UserID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandlerRejectsUnscopedQuestion(t *testing.T) {
	application := &fakeApplication{}
	handler := NewHandler(testOptions(), application)

	response := handler.Route(t.Context(), &RequestEnvelope{
		Request: RequestPayload{
			Type: requestTypeIntent,
			Intent: &Intent{
				Name: IntentAskGPT,
				Slots: map[string]Slot{
					slotQuery: {Name: slotQuery, Value: "question"},
				},
			},
		},
	})

	if got := response.Response.OutputSpeech.Text; got != speechUnknownUser {
		t.Errorf("speech = %q, want %q", got, speechUnknownUser)
	}
	if application.askRequest != (app.AskRequest{}) {
		t.Errorf("application received unscoped request: %+v", application.askRequest)
	}
}

func TestHandlerRejectsUnscopedHistoryRead(t *testing.T) {
	application := &fakeApplication{}
	handler := NewHandler(testOptions(), application)

	response := handler.Route(t.Context(), &RequestEnvelope{
		Request: RequestPayload{
			Type:   requestTypeIntent,
			Intent: &Intent{Name: IntentLastResponse},
		},
	})

	if got := response.Response.OutputSpeech.Text; got != speechUnknownUser {
		t.Errorf("speech = %q, want %q", got, speechUnknownUser)
	}
	if application.latestPrincipal != (app.Principal{}) {
		t.Errorf("application received unscoped history read: %+v", application.latestPrincipal)
	}
}

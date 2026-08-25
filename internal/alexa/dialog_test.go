package alexa

import (
	"encoding/json"
	"testing"
)

func TestLaunchChainsIntoAskDialog(t *testing.T) {
	handler := NewHandler(testOptions(), &fakeApplication{})
	response := handler.Route(t.Context(), &RequestEnvelope{
		Request: RequestPayload{Type: requestTypeLaunch},
	})

	if response.Response.ShouldEndSession {
		t.Fatal("launch dialog should keep the session open")
	}
	if response.Response.OutputSpeech == nil || response.Response.OutputSpeech.Text != speechReady {
		t.Fatalf("output speech = %+v", response.Response.OutputSpeech)
	}
	if response.Response.Reprompt != nil {
		t.Fatal("Dialog.Delegate uses the dialog model prompt, not a response reprompt")
	}
	if len(response.Response.Directives) != 1 {
		t.Fatalf("directives = %d", len(response.Response.Directives))
	}

	directive := response.Response.Directives[0]
	if directive.Type != directiveDialogDelegate {
		t.Errorf("directive type = %q", directive.Type)
	}
	if directive.UpdatedIntent.Name != IntentAskGPT {
		t.Errorf("updated intent = %q", directive.UpdatedIntent.Name)
	}
	slot, ok := directive.UpdatedIntent.Slots[slotQuery]
	if !ok || slot.Name != slotQuery {
		t.Errorf("query slot = %+v, present = %v", slot, ok)
	}
}

func TestNewDelegateJSON(t *testing.T) {
	body, err := json.Marshal(NewDelegate("Ready.", IntentAskGPT, slotQuery))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded ResponseEnvelope
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(decoded.Response.Directives) != 1 {
		t.Fatalf("directives = %d, body = %s", len(decoded.Response.Directives), body)
	}
	if got := decoded.Response.Directives[0].UpdatedIntent.ConfirmationStatus; got != confirmationStatusNone {
		t.Errorf("intent confirmation status = %q", got)
	}
	if got := decoded.Response.Directives[0].UpdatedIntent.Slots[slotQuery].ConfirmationStatus; got != confirmationStatusNone {
		t.Errorf("slot confirmation status = %q", got)
	}
}

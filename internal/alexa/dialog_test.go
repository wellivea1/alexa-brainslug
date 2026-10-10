package alexa

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// The help, fallback, and unknown-intent paths must re-enter dialog
// management. A plain open session cannot accept a bare question, because
// every AskGPTIntent sample needs a carrier phrase, so the user would be sent
// round the fallback path again.
func TestRecoveryIntentsChainIntoAskDialog(t *testing.T) {
	tests := []struct {
		name   string
		intent string
		speech string
	}{
		{"help", IntentHelp, speechHelp},
		{"fallback", IntentFallback, speechFallback},
		{"unknown intent", "AMAZON.NavigateHomeIntent", speechUnknownName},
	}

	handler := NewHandler(testOptions(), &fakeApplication{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := handler.Route(t.Context(), &RequestEnvelope{
				Request: RequestPayload{Type: requestTypeIntent, Intent: &Intent{Name: tt.intent}},
			})

			if response.Response.ShouldEndSession {
				t.Fatal("the session must stay open for the question")
			}
			if response.Response.OutputSpeech == nil || response.Response.OutputSpeech.Text != tt.speech {
				t.Errorf("output speech = %+v, want %q", response.Response.OutputSpeech, tt.speech)
			}
			if len(response.Response.Directives) != 1 {
				t.Fatalf("directives = %+v, want one Dialog.Delegate", response.Response.Directives)
			}
			directive := response.Response.Directives[0]
			if directive.Type != directiveDialogDelegate || directive.UpdatedIntent.Name != IntentAskGPT {
				t.Errorf("directive = %+v, want Dialog.Delegate into %s", directive, IntentAskGPT)
			}
			if _, ok := directive.UpdatedIntent.Slots[slotQuery]; !ok {
				t.Errorf("updated intent slots = %+v, want %s", directive.UpdatedIntent.Slots, slotQuery)
			}
		})
	}
}

// Spoken guidance and Query-slot retrieval must agree with the samples the
// interaction model actually defines; a phrase Alexa cannot route is worse
// than no hint at all.
func TestRetrievalPhrasesMatchInteractionModel(t *testing.T) {
	raw, err := os.ReadFile("../../skill/interaction-model.json")
	if err != nil {
		t.Fatalf("read interaction model: %v", err)
	}
	var document struct {
		InteractionModel struct {
			LanguageModel struct {
				Intents []struct {
					Name    string   `json:"name"`
					Samples []string `json:"samples"`
				} `json:"intents"`
			} `json:"languageModel"`
		} `json:"interactionModel"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode interaction model: %v", err)
	}

	var samples []string
	for _, intent := range document.InteractionModel.LanguageModel.Intents {
		if intent.Name == IntentLastResponse {
			samples = intent.Samples
		}
	}
	if len(samples) == 0 {
		t.Fatalf("%s has no samples", IntentLastResponse)
	}
	if !slices.Contains(samples, helpLastResponseExample) {
		t.Errorf("help teaches %q, which is not a %s sample: %q", helpLastResponseExample, IntentLastResponse, samples)
	}
	for _, sample := range samples {
		if !isLastResponseRequest(sample) {
			t.Errorf("%s sample %q is not recognized when captured as a Query", IntentLastResponse, sample)
		}
	}
}

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

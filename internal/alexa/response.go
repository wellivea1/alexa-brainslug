package alexa

// speechTypePlain is the only output speech type this skill emits. SSML would
// let the model's text be interpreted as markup, so plain text is both
// simpler and safer.
const speechTypePlain = "PlainText"

// responseVersion is the Alexa response envelope version.
const responseVersion = "1.0"

const (
	directiveDialogDelegate = "Dialog.Delegate"
	confirmationStatusNone  = "NONE"
)

// ResponseEnvelope is the JSON body returned to Alexa.
type ResponseEnvelope struct {
	Version  string        `json:"version"`
	Response SkillResponse `json:"response"`
}

// SkillResponse is what Alexa should say and whether to keep listening.
type SkillResponse struct {
	OutputSpeech *OutputSpeech     `json:"outputSpeech,omitempty"`
	Reprompt     *Reprompt         `json:"reprompt,omitempty"`
	Directives   []DialogDirective `json:"directives,omitempty"`

	// ShouldEndSession false keeps the microphone open for a reply. It is
	// never omitted: Alexa treats a missing value differently from false.
	ShouldEndSession bool `json:"shouldEndSession"`
}

// OutputSpeech is a block of speech.
type OutputSpeech struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Reprompt is spoken if the user says nothing after an Ask.
type Reprompt struct {
	OutputSpeech *OutputSpeech `json:"outputSpeech,omitempty"`
}

// DialogDirective tells Alexa to continue a dialog for UpdatedIntent.
type DialogDirective struct {
	Type          string       `json:"type"`
	UpdatedIntent DialogIntent `json:"updatedIntent"`
}

// DialogIntent is the complete intent object used to start dialog management.
type DialogIntent struct {
	Name               string                `json:"name"`
	ConfirmationStatus string                `json:"confirmationStatus"`
	Slots              map[string]DialogSlot `json:"slots"`
}

// DialogSlot is one empty or populated slot in an updated dialog intent.
type DialogSlot struct {
	Name               string `json:"name"`
	ConfirmationStatus string `json:"confirmationStatus"`
}

// NewTell speaks text and ends the session.
func NewTell(text string) ResponseEnvelope {
	return ResponseEnvelope{
		Version: responseVersion,
		Response: SkillResponse{
			OutputSpeech:     PlainSpeech(text),
			ShouldEndSession: true,
		},
	}
}

// NewAsk speaks text and keeps the session open, using reprompt if the user
// stays silent. Alexa requires a reprompt whenever the session stays open.
func NewAsk(text, reprompt string) ResponseEnvelope {
	return ResponseEnvelope{
		Version: responseVersion,
		Response: SkillResponse{
			OutputSpeech:     PlainSpeech(text),
			Reprompt:         &Reprompt{OutputSpeech: PlainSpeech(reprompt)},
			ShouldEndSession: false,
		},
	}
}

// NewDelegate starts Alexa dialog management for intentName. slotNames must
// include every slot in the target intent, including empty ones.
func NewDelegate(text, intentName string, slotNames ...string) ResponseEnvelope {
	slots := make(map[string]DialogSlot, len(slotNames))
	for _, name := range slotNames {
		slots[name] = DialogSlot{Name: name, ConfirmationStatus: confirmationStatusNone}
	}

	response := SkillResponse{
		Directives: []DialogDirective{{
			Type: directiveDialogDelegate,
			UpdatedIntent: DialogIntent{
				Name:               intentName,
				ConfirmationStatus: confirmationStatusNone,
				Slots:              slots,
			},
		}},
		ShouldEndSession: false,
	}
	if text != "" {
		response.OutputSpeech = PlainSpeech(text)
	}

	return ResponseEnvelope{Version: responseVersion, Response: response}
}

// PlainSpeech wraps text as plain-text output speech.
func PlainSpeech(text string) *OutputSpeech {
	return &OutputSpeech{Type: speechTypePlain, Text: text}
}

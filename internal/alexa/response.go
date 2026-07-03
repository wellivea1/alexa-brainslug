package alexa

type ResponseEnvelope struct {
	Version  string        `json:"version"`
	Response SkillResponse `json:"response"`
}

type SkillResponse struct {
	OutputSpeech     *OutputSpeech `json:"outputSpeech,omitempty"`
	Reprompt         *Reprompt     `json:"reprompt,omitempty"`
	ShouldEndSession bool          `json:"shouldEndSession"`
}

type OutputSpeech struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type Reprompt struct {
	OutputSpeech *OutputSpeech `json:"outputSpeech,omitempty"`
}

func NewTell(text string) ResponseEnvelope {
	return ResponseEnvelope{
		Version: "1.0",
		Response: SkillResponse{
			OutputSpeech:     PlainSpeech(text),
			ShouldEndSession: true,
		},
	}
}

func NewAsk(text, reprompt string) ResponseEnvelope {
	return ResponseEnvelope{
		Version: "1.0",
		Response: SkillResponse{
			OutputSpeech:     PlainSpeech(text),
			Reprompt:         &Reprompt{OutputSpeech: PlainSpeech(reprompt)},
			ShouldEndSession: false,
		},
	}
}

func PlainSpeech(text string) *OutputSpeech {
	return &OutputSpeech{
		Type: "PlainText",
		Text: text,
	}
}

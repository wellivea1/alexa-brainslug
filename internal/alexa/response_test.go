package alexa

import (
	"encoding/json"
	"testing"
)

func TestNewAskJSON(t *testing.T) {
	resp := NewAsk("Brainslug is ready.", "What should I ask?")
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded ResponseEnvelope
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Version != "1.0" {
		t.Fatalf("Version = %q", decoded.Version)
	}
	if decoded.Response.ShouldEndSession {
		t.Fatal("ShouldEndSession should be false")
	}
	if decoded.Response.OutputSpeech.Text != "Brainslug is ready." {
		t.Fatalf("OutputSpeech.Text = %q", decoded.Response.OutputSpeech.Text)
	}
	if decoded.Response.Reprompt.OutputSpeech.Text != "What should I ask?" {
		t.Fatalf("Reprompt text = %q", decoded.Response.Reprompt.OutputSpeech.Text)
	}
}

func TestNewTellJSON(t *testing.T) {
	resp := NewTell("Goodbye.")
	if !resp.Response.ShouldEndSession {
		t.Fatal("ShouldEndSession should be true")
	}
	if resp.Response.OutputSpeech.Type != "PlainText" {
		t.Fatalf("speech type = %q", resp.Response.OutputSpeech.Type)
	}
}

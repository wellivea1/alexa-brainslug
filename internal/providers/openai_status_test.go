package providers

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestOpenAIAskReportsOutputLimit(t *testing.T) {
	provider, _ := newOpenAITestServer(t, http.StatusOK, `{
		"status": "incomplete",
		"incomplete_details": {"reason": "max_output_tokens"},
		"output_text": "partial answer that must not be treated as complete"
	}`)

	_, err := provider.Ask(t.Context(), Request{Prompt: "q"})
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("Ask() error = %v, want ErrOutputLimit", err)
	}
}

func TestOpenAIAskReportsOtherIncompleteState(t *testing.T) {
	provider, _ := newOpenAITestServer(t, http.StatusOK, `{
		"status": "incomplete",
		"incomplete_details": {"reason": "content_filter"}
	}`)

	_, err := provider.Ask(t.Context(), Request{Prompt: "q"})
	if !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("Ask() error = %v, want ErrIncompleteResponse", err)
	}
	if !strings.Contains(err.Error(), "content_filter") {
		t.Errorf("Ask() error = %v", err)
	}
}

func TestOpenAIAskRejectsUnexpectedBackgroundState(t *testing.T) {
	for _, status := range []string{openAIStatusQueued, openAIStatusInProgress} {
		t.Run(status, func(t *testing.T) {
			provider, _ := newOpenAITestServer(t, http.StatusOK, `{"status":"`+status+`"}`)
			_, err := provider.Ask(t.Context(), Request{Prompt: "q"})
			if !errors.Is(err, ErrIncompleteResponse) {
				t.Fatalf("Ask() error = %v, want ErrIncompleteResponse", err)
			}
		})
	}
}

func TestOpenAIAskReportsAndRedactsTerminalFailure(t *testing.T) {
	provider, _ := newOpenAITestServer(t, http.StatusOK, `{
		"status": "failed",
		"error": {"message": "Invalid key test-key-value supplied"}
	}`)

	_, err := provider.Ask(t.Context(), Request{Prompt: "q"})
	if !errors.Is(err, ErrFailedResponse) {
		t.Fatalf("Ask() error = %v, want ErrFailedResponse", err)
	}
	if strings.Contains(err.Error(), "test-key-value") {
		t.Fatalf("provider credential leaked: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("Ask() error = %v", err)
	}
}

func TestOpenAIAskAcceptsExplicitCompletedStatus(t *testing.T) {
	provider, _ := newOpenAITestServer(t, http.StatusOK, `{
		"status": "completed",
		"output_text": "answer"
	}`)

	response, err := provider.Ask(t.Context(), Request{Prompt: "q"})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if response.FullAnswer != "answer" {
		t.Errorf("FullAnswer = %q", response.FullAnswer)
	}
}

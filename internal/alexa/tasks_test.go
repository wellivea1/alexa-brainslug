package alexa

import (
	"context"
	"strings"
	"testing"

	"github.com/wellivea1/alexa-brainslug/internal/app"
	"github.com/wellivea1/alexa-brainslug/internal/control"
)

type fakeTaskApplication struct {
	automatic bool
	calls     []app.TaskRequest
	job       *control.Job
	err       error
}

func (f *fakeTaskApplication) Automatic() bool { return f.automatic }
func (f *fakeTaskApplication) Task(_ context.Context, request app.TaskRequest) (*control.Job, error) {
	f.calls = append(f.calls, request)
	return f.job, f.err
}

func taskEnvelope(intent string) *RequestEnvelope {
	return &RequestEnvelope{Session: Session{User: User{UserID: "verified-account"}}, Request: RequestPayload{Type: requestTypeIntent, RequestID: "verified-request-id", Intent: &Intent{Name: intent, Slots: map[string]Slot{slotQuery: {Value: "inspect startup in the registered project"}}}}}
}

func TestVoiceTaskAcknowledgesOnlySavedAcceptanceAndForwardsVerifiedScope(t *testing.T) {
	tasks := &fakeTaskApplication{job: &control.Job{ID: "saved", State: control.Accepted}}
	options := testOptions()
	options.Tasks = tasks
	handler := NewHandler(options, &fakeApplication{})
	response := handler.Route(t.Context(), taskEnvelope(IntentCodexTask))
	if len(tasks.calls) != 1 || tasks.calls[0].RequestID != "verified-request-id" || tasks.calls[0].Principal.Namespace != "alexa" || tasks.calls[0].Principal.Subject != "verified-account" || tasks.calls[0].Action != control.ActionNew {
		t.Fatalf("voice task scope = %+v", tasks.calls)
	}
	text := response.Response.OutputSpeech.Text
	if !strings.Contains(text, "saved") || strings.Contains(text, "working") || strings.Contains(text, "started") {
		t.Fatalf("acceptance claimed execution: %s", text)
	}
	tasks.err = control.ErrUnavailable
	response = handler.Route(t.Context(), taskEnvelope(IntentCodexTask))
	if !strings.Contains(response.Response.OutputSpeech.Text, "did not confirm") {
		t.Fatal("unavailable controller claimed acceptance")
	}
}

func TestVoiceTaskStopStatusCancellationAndAutomaticAnswers(t *testing.T) {
	tasks := &fakeTaskApplication{job: &control.Job{State: control.CancelRequested}}
	options := testOptions()
	options.Tasks = tasks
	handler := NewHandler(options, &fakeApplication{})
	for _, intent := range []string{IntentStop, IntentCancel} {
		handler.Route(t.Context(), taskEnvelope(intent))
	}
	if len(tasks.calls) != 0 {
		t.Fatal("ending the voice session canceled durable work")
	}
	response := handler.Route(t.Context(), taskEnvelope(IntentCodexCancel))
	if tasks.calls[0].Action != control.ActionCancel || tasks.calls[0].Reference != "latest" || !strings.Contains(response.Response.OutputSpeech.Text, "not confirmed") {
		t.Fatal("cancellation was not scoped or confirmed accurately")
	}
	tasks.automatic = true
	tasks.job = &control.Job{State: control.Accepted}
	handler.Route(t.Context(), taskEnvelope(IntentAskGPT))
	if len(tasks.calls) != 2 || tasks.calls[1].Action != "" {
		t.Fatal("automatic question did not enter the controller lifecycle")
	}
	tasks.job = &control.Job{State: control.Completed, Result: "**Completed** the disposable task."}
	response = handler.Route(t.Context(), taskEnvelope(IntentCodexResult))
	if response.Response.OutputSpeech.Text != "Completed the disposable task." || tasks.calls[2].Action != "result" {
		t.Fatal("voice did not shape the saved full result")
	}
}

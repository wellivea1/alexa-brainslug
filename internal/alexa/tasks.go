package alexa

import (
	"context"
	"errors"

	"github.com/wellivea1/alexa-brainslug/internal/app"
	"github.com/wellivea1/alexa-brainslug/internal/control"
	"github.com/wellivea1/alexa-brainslug/internal/speech"
)

// Explicit Codex intents remain distinct from ending an Alexa voice session.
const (
	IntentCodexTask     = "CodexTaskIntent"
	IntentCodexStatus   = "CodexStatusIntent"
	IntentCodexResult   = "CodexResultIntent"
	IntentCodexContinue = "CodexContinueIntent"
	IntentCodexReply    = "CodexReplyIntent"
	IntentCodexCancel   = "CodexCancelTaskIntent"
	TaskReferenceSlot   = "Task"
)

// TaskApplication is the platform-neutral durable-task use case.
type TaskApplication interface {
	Automatic() bool
	Task(context.Context, app.TaskRequest) (*control.Job, error)
}

func (h *Handler) handleCodex(ctx context.Context, envelope *RequestEnvelope) ResponseEnvelope {
	intent := envelope.Request.Intent
	action := control.ActionNew
	reference := intent.SlotValue(TaskReferenceSlot)
	query := intent.SlotValue(slotQuery)
	switch intent.Name {
	case IntentCodexStatus:
		action = control.ActionStatus
	case IntentCodexResult:
		action = "result"
	case IntentCodexContinue:
		action = control.ActionContinue
	case IntentCodexReply:
		action = "reply"
	case IntentCodexCancel:
		action = control.ActionCancel
	}
	if action == control.ActionNew || action == control.ActionContinue || action == "reply" {
		if query == "" {
			return NewAsk("What should Codex do?", "Say, ask Codex to inspect your project.")
		}
	} else if reference == "" {
		reference = "latest"
	}
	return h.taskResponse(ctx, envelope, action, query, reference)
}

func (h *Handler) taskResponse(ctx context.Context, envelope *RequestEnvelope, action, prompt, reference string) ResponseEnvelope {
	if h.tasks == nil {
		return NewTell("Codex task control is not connected yet.")
	}
	job, err := h.tasks.Task(ctx, app.TaskRequest{Principal: principalForEnvelope(envelope), RequestID: envelope.Request.RequestID, Prompt: prompt, Action: action, Reference: reference, Source: "alexa"})
	if err != nil {
		switch {
		case errors.Is(err, control.ErrForbidden):
			return NewTell("This Alexa account has not been granted access to that Codex project.")
		case errors.Is(err, control.ErrNotFound):
			return NewTell("I could not find that Codex task.")
		case errors.Is(err, app.ErrAmbiguousTask):
			return NewAsk("Which Codex task do you mean?", "Ask for a task by its name, or say latest task.")
		case errors.Is(err, control.ErrQueueFull):
			return NewTell("The Codex task queue is full. Try again after a task finishes.")
		case errors.Is(err, control.ErrConflict), errors.Is(err, control.ErrInvalid):
			return NewTell("That task request conflicts with the registered project or conversation settings.")
		default:
			return NewTell("The task controller did not confirm saved acceptance. Check task status before submitting it again.")
		}
	}
	if job == nil {
		return NewTell("The task controller did not confirm this request.")
	}
	switch job.State {
	case control.Completed:
		text := speech.Summarize(job.Result, spokenWordLimit)
		if text == "" {
			text = "The Codex task completed. Its details are available in the dashboard."
		}
		return NewTell(text)
	case control.Accepted, control.Routing:
		return NewTell("Your request is saved. Ask for the latest task status later.")
	case control.Queued:
		return NewTell("Your task is saved and queued for its selected machine.")
	case control.Dispatching:
		return NewTell("The saved task is being sent to its selected machine.")
	case control.Running:
		return NewTell("The last saved status says Codex is working on the task.")
	case control.AwaitingInput:
		return NewAsk(job.Decision.Clarification, "Say, reply to Codex, followed by your answer.")
	case control.AwaitingApproval:
		return NewTell("The task is waiting for owner approval in the private dashboard.")
	case control.CancelRequested:
		return NewTell("Cancellation was requested. The worker has not confirmed that it stopped yet.")
	case control.Canceled:
		return NewTell("The task cancellation is confirmed.")
	case control.NeedsAttention:
		return NewTell("The task's execution is uncertain. Check its saved conversation in the dashboard before starting more work.")
	default:
		return NewTell("The Codex task failed. Check its details in the dashboard.")
	}
}

package app

import (
	"context"
	"errors"
	"strings"

	"github.com/wellivea1/alexa-brainslug/internal/control"
)

// ErrAmbiguousTask requires an explicit conversation or task reference.
var ErrAmbiguousTask = errors.New("which Codex task should I use")

// TaskActionReply and TaskActionResult are application actions handled without routing inference.
const (
	TaskActionReply     = "reply"
	TaskActionResult    = "result"
	taskReferenceLatest = "latest"
	taskReferenceLast   = "last"
)

// TaskController is the gateway's durable orchestration port, separate from Provider.
type TaskController interface {
	Mode() string
	Submit(context.Context, control.Request) (*control.Job, error)
	Get(context.Context, control.Principal, string) (*control.Job, error)
	List(context.Context, control.Principal) ([]*control.Job, error)
	Cancel(context.Context, control.Principal, string) (*control.Job, error)
	Reply(context.Context, control.Principal, string, control.Request) (*control.Job, error)
}

// TaskRequest is a normalized verified ingress operation, independent of ASK.
type TaskRequest struct {
	Principal Principal
	RequestID string
	Prompt    string
	Action    string
	Reference string
	Source    string
}

// TaskService implements scoped voice task actions without storing a second queue.
type TaskService struct{ controller TaskController }

// NewTaskService supplies a remote controller port.
func NewTaskService(controller TaskController) *TaskService {
	return &TaskService{controller: controller}
}

// Automatic reports whether ordinary questions belong to the controller lifecycle.
func (s *TaskService) Automatic() bool {
	return s != nil && s.controller != nil && s.controller.Mode() == control.ModeAutomatic
}

// Task performs an explicitly scoped submission, status, result, follow-up, reply or cancellation.
func (s *TaskService) Task(ctx context.Context, request TaskRequest) (*control.Job, error) {
	if s == nil || s.controller == nil || s.controller.Mode() == "disabled" {
		return nil, control.ErrUnavailable
	}
	owner := control.Principal{Namespace: request.Principal.Namespace, Subject: request.Principal.Subject}
	if !owner.Valid() {
		return nil, ErrInvalidPrincipal
	}
	if request.Source == "" {
		request.Source = "alexa"
	}
	command := control.Request{Principal: owner, Source: request.Source, RequestID: request.RequestID, Prompt: request.Prompt, Mode: s.controller.Mode(), Action: request.Action}
	switch request.Action {
	case control.ActionStatus, TaskActionResult, control.ActionCancel, control.ActionContinue, TaskActionReply:
		job, err := s.resolve(ctx, owner, request.Reference, request.Action)
		if err != nil {
			if errors.Is(err, ErrAmbiguousTask) && (request.Action == control.ActionContinue || request.Action == TaskActionReply) {
				command.Action = control.ActionContinue
				return s.controller.Submit(ctx, command)
			}
			return nil, err
		}
		switch request.Action {
		case control.ActionStatus, TaskActionResult:
			return job, nil
		case control.ActionCancel:
			return s.controller.Cancel(ctx, owner, job.ID)
		case TaskActionReply:
			command.Action = ""
			return s.controller.Reply(ctx, owner, job.ID, command)
		case control.ActionContinue:
			command.ConversationID = job.ConversationID
			if command.ConversationID == "" {
				return nil, ErrAmbiguousTask
			}
		}
	}
	return s.controller.Submit(ctx, command)
}

func (s *TaskService) resolve(ctx context.Context, owner control.Principal, reference, action string) (*control.Job, error) {
	jobs, err := s.controller.List(ctx, owner)
	if err != nil {
		return nil, err
	}
	reference = strings.TrimSpace(strings.ToLower(reference))
	var candidates []*control.Job
	seenConversations := make(map[string]bool)
	for _, job := range jobs {
		if action == TaskActionReply && job.State != control.AwaitingInput {
			continue
		}
		if action == control.ActionContinue && job.ConversationID == "" {
			continue
		}
		if action == control.ActionContinue && (reference == "" || reference == taskReferenceLatest || reference == taskReferenceLast) {
			if seenConversations[job.ConversationID] {
				continue
			}
			seenConversations[job.ConversationID] = true
		}
		if reference != "" && reference != taskReferenceLatest && reference != taskReferenceLast {
			name := strings.TrimSuffix(reference, " task")
			if strings.EqualFold(job.ID, reference) || strings.EqualFold(job.Decision.Title, reference) || (len(name) >= 3 && strings.Contains(strings.ToLower(job.Decision.Title), name)) {
				candidates = append(candidates, job)
			}
			continue
		}
		candidates = append(candidates, job)
	}
	if len(candidates) == 0 {
		return nil, control.ErrNotFound
	}
	if reference == "" && action == control.ActionContinue {
		var active []*control.Job
		for _, candidate := range candidates {
			if !control.Terminal(candidate.State) && candidate.ThreadID != "" {
				active = append(active, candidate)
			}
		}
		if len(active) > 0 {
			candidates = active
		}
	}
	if reference == taskReferenceLatest || reference == taskReferenceLast {
		latest := candidates[0]
		for _, candidate := range candidates {
			if candidate.CreatedAt.After(latest.CreatedAt) {
				latest = candidate
			}
		}
		return s.controller.Get(ctx, owner, latest.ID)
	}
	if len(candidates) != 1 {
		return nil, ErrAmbiguousTask
	}
	return s.controller.Get(ctx, owner, candidates[0].ID)
}

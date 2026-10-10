package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/control"
)

type taskPort struct {
	mode      string
	jobs      []*control.Job
	submitted []control.Request
	readID    string
}

func (p *taskPort) Mode() string { return p.mode }
func (p *taskPort) Submit(_ context.Context, r control.Request) (*control.Job, error) {
	p.submitted = append(p.submitted, r)
	return &control.Job{State: control.Accepted, Request: r}, nil
}

func (p *taskPort) Get(_ context.Context, owner control.Principal, id string) (*control.Job, error) {
	p.readID = id
	for _, job := range p.jobs {
		if job.ID == id && job.Request.Principal == owner {
			full := *job
			full.Result = strings.Repeat("full result ", 100)
			return &full, nil
		}
	}
	return nil, control.ErrForbidden
}

func (p *taskPort) List(_ context.Context, owner control.Principal) ([]*control.Job, error) {
	var jobs []*control.Job
	for _, job := range p.jobs {
		if job.Request.Principal == owner {
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

func (p *taskPort) Cancel(ctx context.Context, owner control.Principal, id string) (*control.Job, error) {
	return p.Get(ctx, owner, id)
}

func (p *taskPort) Reply(_ context.Context, owner control.Principal, id string, r control.Request) (*control.Job, error) {
	r.Principal = owner
	r.ReplyTo = id
	p.submitted = append(p.submitted, r)
	return &control.Job{State: control.Accepted}, nil
}

func TestTasksResolveOwnedActiveConversationAndRetrieveFullResult(t *testing.T) {
	owner := control.Principal{Namespace: "alexa", Subject: "account"}
	p := &taskPort{mode: control.ModeManual, jobs: []*control.Job{{ID: "active", State: control.Running, CreatedAt: time.Now(), ThreadID: "thread-a", ConversationID: "conversation-a", Request: control.Request{Principal: owner}, Decision: control.Decision{Title: "Inspect startup"}}, {ID: "older", State: control.Completed, CreatedAt: time.Now().Add(-time.Hour), ConversationID: "conversation-b", Request: control.Request{Principal: owner}, Decision: control.Decision{Title: "Other task"}}, {ID: "another-user", Request: control.Request{Principal: control.Principal{Namespace: "alexa", Subject: "other"}}}}}
	service := NewTaskService(p)
	request := TaskRequest{Principal: Principal{Namespace: "alexa", Subject: "account"}, RequestID: "verified", Prompt: "add tests", Action: control.ActionContinue}
	if _, err := service.Task(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if len(p.submitted) != 1 || p.submitted[0].ConversationID != "conversation-a" || p.submitted[0].RequestID != "verified" || p.submitted[0].Principal != owner {
		t.Fatal("follow-up was not scoped to the active conversation")
	}
	request.Action = TaskActionResult
	request.Reference = "startup task"
	result, err := service.Task(t.Context(), request)
	if err != nil || p.readID != "active" || len(result.Result) <= 512 {
		t.Fatal("voice result used a truncated list summary")
	}
	request.Reference = "another-user"
	if _, err := service.Task(t.Context(), request); !errors.Is(err, control.ErrNotFound) {
		t.Fatal("task crossed principal scope")
	}
	p.jobs[1].State = control.Running
	p.jobs[1].ThreadID = "thread-b"
	request.Action = control.ActionContinue
	request.Reference = ""
	if _, err := service.Task(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if len(p.submitted) != 2 || p.submitted[1].ConversationID != "" {
		t.Fatal("ambiguous follow-up silently selected a thread")
	}
}

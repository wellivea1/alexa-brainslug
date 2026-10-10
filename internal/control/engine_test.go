package control_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wellivea1/alexa-brainslug/internal/control"
	"github.com/wellivea1/alexa-brainslug/internal/controlstore"
)

type fakeDispatcher struct {
	mu       sync.Mutex
	commands []control.Command
	hosts    []control.Host
	err      error
}

func (d *fakeDispatcher) Send(_ context.Context, _ string, command control.Command) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.commands = append(d.commands, command)
	return d.err
}

func (d *fakeDispatcher) Hosts() []control.Host {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]control.Host(nil), d.hosts...)
}

func policy() control.Options {
	pair := control.ModelPair{Model: "allowed-model", Effort: "medium"}
	return control.Options{
		PolicyVersion: "test-1", QueueLimit: 8, Concurrency: 2, RoutingSeconds: 2, RetentionDays: 7, DefaultProject: "project-a",
		Hosts: []control.Host{{ID: "host-a", CodexVersion: "fake-1", Models: []control.ModelPair{pair}}, {ID: "host-b", CodexVersion: "fake-1", Models: []control.ModelPair{pair}}},
		Projects: []control.Project{
			{ID: "project-a", Name: "Same name", HostID: "host-a", Principals: []string{"owner:alice"}, DefaultProfile: "default", Profiles: map[string]control.Profile{"default": {ModelPair: pair, Permission: "read-only", MaxSeconds: 60, MaxTokens: 64000}}},
			{ID: "project-b", Name: "Same name", HostID: "host-b", Principals: []string{"owner:alice"}, DefaultProfile: "default", Profiles: map[string]control.Profile{"default": {ModelPair: pair, Permission: "read-only", MaxSeconds: 60, MaxTokens: 64000}}},
		},
	}
}

func setup(t *testing.T) (*control.Engine, *controlstore.Store, *fakeDispatcher, control.Options) {
	t.Helper()
	store, err := controlstore.Open(t.Context(), filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	options := policy()
	dispatcher := &fakeDispatcher{hosts: []control.Host{{ID: "host-a", Online: true, ProjectIDs: []string{"project-a"}, Models: options.Hosts[0].Models}, {ID: "host-b", Online: true, ProjectIDs: []string{"project-b"}, Models: options.Hosts[1].Models}}}
	engine, err := control.NewEngine(t.Context(), store, &options, dispatcher, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return engine, store, dispatcher, options
}

func request(id string) control.Request {
	return control.Request{Principal: control.Principal{Namespace: "owner", Subject: "alice"}, Source: "test", RequestID: id, Prompt: "inspect startup", Mode: control.ModeManual, ProjectID: "project-a"}
}

func TestAcceptanceIsDurableIdempotentAndScoped(t *testing.T) {
	engine, store, dispatcher, _ := setup(t)
	job, err := engine.Submit(t.Context(), request("request-1"))
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := engine.Submit(t.Context(), request("request-1"))
	if err != nil || duplicate.ID != job.ID {
		t.Fatalf("duplicate = %+v / %v", duplicate, err)
	}
	if job.State != control.Accepted || len(dispatcher.commands) != 0 {
		t.Fatal("acceptance must precede worker execution")
	}
	saved, err := store.Get(t.Context(), job.ID)
	if err != nil || saved.State != control.Accepted {
		t.Fatal("acceptance was not persisted")
	}
	changed := request("request-1")
	changed.Prompt = "different work"
	if _, err := engine.Submit(t.Context(), changed); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("changed duplicate = %v", err)
	}
	if _, err := engine.Get(t.Context(), control.Principal{Namespace: "owner", Subject: "bob"}, job.ID); !errors.Is(err, control.ErrForbidden) {
		t.Fatalf("cross-owner read = %v", err)
	}
	if _, err := engine.Cancel(t.Context(), control.Principal{Namespace: "owner", Subject: "bob"}, job.ID); !errors.Is(err, control.ErrForbidden) {
		t.Fatalf("cross-owner cancellation = %v", err)
	}
	if err := engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.commands) != 1 || dispatcher.commands[0].AttemptID == "" {
		t.Fatal("one persisted dispatch attempt is required")
	}
	if err := engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.commands) != 1 {
		t.Fatal("the same task was dispatched twice")
	}
}

func TestWorkerReplayRecoveryAndPinnedContinuation(t *testing.T) {
	engine, store, dispatcher, options := setup(t)
	job, err := engine.Submit(t.Context(), request("initial"))
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	command := dispatcher.commands[0]
	running := control.WorkerEvent{JobID: job.ID, AttemptID: command.AttemptID, Sequence: 1, State: control.Running, ThreadID: "thread-a", TurnID: "turn-a"}
	if _, err := engine.WorkerUpdate(t.Context(), "host-b", running); !errors.Is(err, control.ErrForbidden) {
		t.Fatalf("wrong worker = %v", err)
	}
	if _, err := engine.WorkerUpdate(t.Context(), "host-a", running); err != nil {
		t.Fatal(err)
	}
	restarted, err := control.NewEngine(t.Context(), store, &options, dispatcher, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := restarted.Get(t.Context(), job.Request.Principal, job.ID)
	if err != nil || saved.State != control.NeedsAttention {
		t.Fatalf("recovery = %+v / %v", saved, err)
	}
	if err := restarted.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.commands) != 1 {
		t.Fatal("restart replayed an uncertain mutating turn")
	}
	if ack, err := restarted.WorkerUpdate(t.Context(), "host-a", running); err != nil || ack != 1 {
		t.Fatalf("duplicate event = %d / %v", ack, err)
	}
	completed := running
	completed.Sequence = 2
	completed.State = control.Completed
	completed.Text = "Saved result."
	if _, err := restarted.WorkerUpdate(t.Context(), "host-a", completed); err != nil {
		t.Fatal(err)
	}
	finished, err := restarted.Get(t.Context(), job.Request.Principal, job.ID)
	if err != nil || finished.Result != "Saved result." {
		t.Fatal("replayed result was not retained")
	}
	followup := request("follow-up")
	followup.Action = control.ActionContinue
	followup.ConversationID = finished.ConversationID
	followup.ProjectID = ""
	followup.Prompt = "add tests"
	continued, err := restarted.Submit(t.Context(), followup)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	command = dispatcher.commands[len(dispatcher.commands)-1]
	if command.JobID != continued.ID || command.ProjectID != "project-a" || command.ThreadID != "thread-a" || command.Profile.Model != "allowed-model" {
		t.Fatalf("continuation moved: %+v", command)
	}
	followup.RequestID = "wrong-host"
	followup.HostID = "host-b"
	if _, err := restarted.Submit(t.Context(), followup); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("continuation host conflict = %v", err)
	}
}

func TestCancellationAndQueueLimitsAreConfirmed(t *testing.T) {
	_, store, dispatcher, options := setup(t)
	options.QueueLimit = 1
	engine, err := control.NewEngine(t.Context(), store, &options, dispatcher, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	job, err := engine.Submit(t.Context(), request("one"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Submit(t.Context(), request("two")); !errors.Is(err, control.ErrQueueFull) {
		t.Fatalf("queue admission = %v", err)
	}
	if _, err := engine.Submit(t.Context(), request("one")); err != nil {
		t.Fatal("duplicate must survive full queue", err)
	}
	if err := engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	canceled, err := engine.Cancel(t.Context(), job.Request.Principal, job.ID)
	if err != nil || canceled.State != control.CancelRequested {
		t.Fatalf("cancel = %+v / %v", canceled, err)
	}
	command := dispatcher.commands[0]
	if _, err := engine.WorkerUpdate(t.Context(), "host-a", control.WorkerEvent{JobID: job.ID, AttemptID: command.AttemptID, Sequence: 1, State: control.Canceled}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Submit(t.Context(), request("two")); err != nil {
		t.Fatal("confirmed cancellation must release capacity", err)
	}
}

func TestDispatchLossNeverMovesWorkToAnotherHost(t *testing.T) {
	engine, _, dispatcher, _ := setup(t)
	dispatcher.err = control.ErrUnavailable
	job, err := engine.Submit(t.Context(), request("uncertain"))
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved, err := engine.Get(t.Context(), job.Request.Principal, job.ID)
	if err != nil || saved.State != control.NeedsAttention || len(dispatcher.commands) != 1 {
		t.Fatalf("uncertain dispatch = %+v / %v", saved, err)
	}
}

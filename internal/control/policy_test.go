package control_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/control"
)

type fixedRouter struct {
	decision control.Decision
	calls    atomic.Int32
}

func (r *fixedRouter) Propose(_ context.Context, _ string, _ control.Catalog) (control.Decision, control.Usage, error) {
	r.calls.Add(1)
	return r.decision, control.Usage{RoutingTokens: 12}, nil
}

func TestShadowRejectsHostileRouteWithoutChangingManualAuthority(t *testing.T) {
	_, store, dispatcher, options := setup(t)
	router := &fixedRouter{decision: control.Decision{Action: control.ActionNew, HostID: "attacker", ProjectID: "unregistered", Model: "unavailable", Effort: "ultra", Profile: "default"}}
	engine, err := control.NewEngine(t.Context(), store, &options, dispatcher, router, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := request("shadow")
	req.Mode = control.ModeShadow
	job, err := engine.Submit(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved, err := engine.Get(t.Context(), req.Principal, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Shadow != nil || saved.Decision.ProjectID != "project-a" || saved.Usage.RoutingTokens != 12 || len(dispatcher.commands) != 1 || router.calls.Load() != 1 {
		t.Fatalf("shadow changed authority: %+v", saved)
	}
}

func TestAutomaticInvalidDestinationAndExplicitOverrides(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid-proposal", true: "explicit-bypass"}[explicit], func(t *testing.T) {
			_, store, dispatcher, options := setup(t)
			options.AutomaticEnabled = true
			options.RouterEvaluationApproved = true
			options.RoutingAccuracyTarget = .95
			router := &fixedRouter{decision: control.Decision{Action: control.ActionNew, HostID: "host-b", ProjectID: "project-b", Profile: "default", Model: "unavailable", Effort: "high"}}
			engine, err := control.NewEngine(t.Context(), store, &options, dispatcher, router, nil)
			if err != nil {
				t.Fatal(err)
			}
			req := request("automatic")
			req.Mode = control.ModeAutomatic
			if !explicit {
				req.ProjectID = ""
			}
			job, err := engine.Submit(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			if err := engine.Tick(t.Context()); err != nil {
				t.Fatal(err)
			}
			saved, err := engine.Get(t.Context(), req.Principal, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if explicit {
				if len(dispatcher.commands) != 1 || saved.Decision.ProjectID != "project-a" || router.calls.Load() != 0 {
					t.Fatal("explicit selection was rerouted")
				}
			} else if saved.State != control.Failed || len(dispatcher.commands) != 0 {
				t.Fatal("invalid model was dispatched")
			}
		})
	}
}

func TestClarificationReplyIsAtomicAndIdempotentAtCapacity(t *testing.T) {
	_, store, dispatcher, options := setup(t)
	options.DefaultProject = ""
	options.QueueLimit = 1
	engine, err := control.NewEngine(t.Context(), store, &options, dispatcher, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := request("clarify")
	req.ProjectID = ""
	job, err := engine.Submit(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	bad := request("reply")
	bad.ProjectID = "unknown"
	if _, err := engine.Reply(t.Context(), req.Principal, job.ID, bad); !errors.Is(err, control.ErrForbidden) {
		t.Fatalf("bad reply = %v", err)
	}
	saved, err := engine.Get(t.Context(), req.Principal, job.ID)
	if err != nil || saved.State != control.AwaitingInput {
		t.Fatal("failed reply destroyed the clarification")
	}
	good := request("reply")
	good.Prompt = "project-a"
	newJob, err := engine.Reply(t.Context(), req.Principal, job.ID, good)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := engine.Reply(t.Context(), req.Principal, job.ID, good)
	if err != nil || duplicate.ID != newJob.ID {
		t.Fatalf("reply duplicate = %+v / %v", duplicate, err)
	}
	saved, err = engine.Get(t.Context(), req.Principal, job.ID)
	if err != nil || saved.State != control.Canceled {
		t.Fatal("accepted reply did not supersede clarification")
	}
}

func TestPolicySnapshotNeverExpandsAndRevocationStopsDispatch(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "budget-snapshot", true: "revoked"}[revoke], func(t *testing.T) {
			engine, _, dispatcher, options := setup(t)
			job, err := engine.Submit(t.Context(), request("saved"))
			if err != nil {
				t.Fatal(err)
			}
			p := options.Projects[0].Profiles["default"]
			p.MaxSeconds = 600
			p.MaxTokens = 100000
			p.Permission = "workspace-write"
			options.Projects[0].Profiles["default"] = p
			if revoke {
				options.Projects[0].Principals = []string{"owner:bob"}
			}
			if err := engine.ReplacePolicy(&options, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			if err := engine.Tick(t.Context()); err != nil {
				t.Fatal(err)
			}
			if revoke {
				if len(dispatcher.commands) != 0 {
					t.Fatal("revoked project was dispatched")
				}
			} else {
				command := dispatcher.commands[0]
				if command.Profile.MaxSeconds != 60 || command.Profile.MaxTokens != 64000 || command.Profile.Permission != "read-only" {
					t.Fatalf("accepted limits expanded: %+v", command.Profile)
				}
			}
			if saved, err := engine.Get(t.Context(), request("saved").Principal, job.ID); !revoke && (err != nil || saved.PolicyVersion != "test-1") {
				t.Fatal("accepted policy version was lost")
			}
		})
	}
}

type waitingAnswerer struct {
	entered chan struct{}
	release chan struct{}
}

func (a *waitingAnswerer) Answer(ctx context.Context, _ string) (string, error) {
	close(a.entered)
	select {
	case <-a.release:
		return "answer", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestAnswerInferenceDoesNotBlockStatusOrCancellation(t *testing.T) {
	_, store, dispatcher, options := setup(t)
	answerer := &waitingAnswerer{entered: make(chan struct{}), release: make(chan struct{})}
	engine, err := control.NewEngine(t.Context(), store, &options, dispatcher, nil, answerer)
	if err != nil {
		t.Fatal(err)
	}
	req := request("answer")
	req.Action = control.ActionAnswer
	req.ProjectID = ""
	job, err := engine.Submit(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- engine.Tick(t.Context()) }()
	select {
	case <-answerer.entered:
	case <-time.After(time.Second):
		t.Fatal("answer did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	saved, err := engine.Cancel(ctx, req.Principal, job.ID)
	if err != nil || saved.State != control.Canceled {
		t.Fatal("answer blocked cancellation")
	}
	close(answerer.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	saved, err = engine.Get(t.Context(), req.Principal, job.ID)
	if err != nil || saved.State != control.Canceled || saved.Result != "" {
		t.Fatal("late answer overwrote cancellation")
	}
}

func TestControllerRetentionWaitsForDurableWorkerReceipt(t *testing.T) {
	engine, store, dispatcher, _ := setup(t)
	job, err := engine.Submit(t.Context(), request("retention"))
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	command := dispatcher.commands[0]
	event := control.WorkerEvent{JobID: job.ID, AttemptID: command.AttemptID, Sequence: 1, State: control.Completed, ThreadID: "saved-thread", TurnID: "saved-turn", Text: "saved result"}
	if _, err := engine.WorkerUpdate(t.Context(), "host-a", event); err != nil {
		t.Fatal(err)
	}
	if count, err := store.Purge(t.Context(), time.Now().Add(time.Hour)); err != nil || count != 0 {
		t.Fatal("controller retention removed a result before receipt confirmation")
	}
	if _, err := engine.WorkerUpdate(t.Context(), "host-a", event); err != nil {
		t.Fatal("lost result acknowledgement could not replay")
	}
	if err := engine.ConfirmReceipt(t.Context(), "host-b", command.AttemptID, 1); !errors.Is(err, control.ErrForbidden) {
		t.Fatal("another host confirmed receipt")
	}
	if err := engine.ConfirmReceipt(t.Context(), "host-a", command.AttemptID, 1); err != nil {
		t.Fatal(err)
	}
	if count, err := store.Purge(t.Context(), time.Now().Add(time.Hour)); err != nil || count != 1 {
		t.Fatal("confirmed terminal result did not purge")
	}
}

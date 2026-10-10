package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/control"
)

// This child is a disposable protocol peer, never the installed Codex binary.
func TestProtocolProcess(_ *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--fake-codex" {
			index = i
		}
	}
	if index < 0 {
		return
	}
	mode, journal := os.Args[index+1], os.Args[index+2]
	root := ""
	initialized := false
	turnID := "fake-turn"
	status := "inProgress"
	send := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			os.Exit(2)
		}
		_, _ = fmt.Fprintln(os.Stdout, string(data))
	}
	save := func() {
		data, _ := json.Marshal(protocolThread{ID: "fake-thread", Cwd: root, Turns: []protocolTurn{{ID: turnID, Status: status, Items: []protocolItem{{Type: "agentMessage", Text: "protocol result", Phase: "final_answer"}}}}})
		if err := os.WriteFile(journal+".pending", data, 0o600); err != nil {
			os.Exit(2)
		}
		if err := os.Rename(journal+".pending", journal); err != nil {
			os.Exit(2)
		}
	}
	finish := func(final string) {
		status = final
		save()
		send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "fake-thread", "turn": protocolTurn{ID: turnID, Status: status, Items: []protocolItem{{Type: "agentMessage", Text: "protocol result", Phase: "final_answer"}}}}})
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 65536), 4<<20)
	for scanner.Scan() {
		var message RPCMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			os.Exit(2)
		}
		var params map[string]any
		_ = json.Unmarshal(message.Params, &params)
		reply := func(result any) { send(map[string]any{"id": message.ID, "result": result}) }
		if message.Method == "initialized" {
			initialized = true
			continue
		}
		if message.Method == "" && string(message.ID) == "900" {
			var response struct {
				Decision string `json:"decision"`
			}
			_ = json.Unmarshal(message.Result, &response)
			if response.Decision != "accept" && response.Decision != "decline" {
				os.Exit(2)
			}
			finish("completed")
			continue
		}
		if message.Method != "initialize" && !initialized {
			os.Exit(2)
		}
		switch message.Method {
		case "initialize":
			reply(map[string]string{"userAgent": "fake-codex"})
		case "account/read":
			reply(map[string]any{"account": map[string]string{"type": "apiKey"}})
		case "model/list":
			if params["cursor"] == nil {
				reply(map[string]any{"data": []any{map[string]any{"model": "fake-model", "supportedReasoningEfforts": []any{map[string]string{"reasoningEffort": "medium"}}}}, "nextCursor": "page-2"})
			} else {
				reply(map[string]any{"data": []any{map[string]any{"model": "fake-model", "supportedReasoningEfforts": []any{map[string]string{"reasoningEffort": "high"}}}}, "nextCursor": nil})
			}
		case "thread/start", "thread/resume":
			root, _ = params["cwd"].(string)
			if params["model"] != "fake-model" || params["approvalPolicy"] != "on-request" || root == "" {
				os.Exit(2)
			}
			if message.Method == "thread/resume" && params["threadId"] != "fake-thread" {
				os.Exit(2)
			}
			save()
			reply(map[string]any{"thread": protocolThread{ID: "fake-thread", Cwd: root}})
		case "thread/read":
			data, err := os.ReadFile(journal)
			if err != nil {
				send(map[string]any{"id": message.ID, "error": map[string]any{"code": 404, "message": "private-path-secret"}})
				continue
			}
			var thread protocolThread
			_ = json.Unmarshal(data, &thread)
			reply(map[string]any{"thread": thread})
		case "turn/start":
			if params["threadId"] != "fake-thread" || params["model"] != "fake-model" || params["effort"] != "medium" || params["cwd"] != root {
				os.Exit(2)
			}
			input, ok := params["input"].([]any)
			if !ok || len(input) != 1 {
				os.Exit(2)
			}
			text, ok := input[0].(map[string]any)
			if !ok || text["type"] != "text" || text["text_elements"] == nil {
				os.Exit(2)
			}
			sandbox, ok := params["sandboxPolicy"].(map[string]any)
			if !ok || sandbox["networkAccess"] != false {
				os.Exit(2)
			}
			if mode == "death" {
				os.Exit(3)
			}
			if mode == "rpc-error" {
				send(map[string]any{"id": message.ID, "error": map[string]any{"code": 123, "message": "private-path-secret"}})
				continue
			}
			if mode == "lost-reply" {
				status = "completed"
				save()
				continue
			}
			reply(map[string]any{"turn": protocolTurn{ID: turnID, Status: "inProgress"}})
			if mode == "approval" || mode == "outside-approval" || mode == "expire-approval" {
				grant := root
				if mode == "outside-approval" {
					grant = filepath.Dir(root)
				}
				send(map[string]any{"id": 900, "method": "item/fileChange/requestApproval", "params": map[string]any{"threadId": "fake-thread", "turnId": turnID, "itemId": "item-1", "grantRoot": grant, "reason": "fake bounded change"}})
				continue
			}
			if mode == "wait-cancel" {
				continue
			}
			if mode == "unsupported" {
				send(map[string]any{"id": 900, "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "fake-thread", "turnId": turnID}})
				continue
			}
			send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "another-thread", "turn": protocolTurn{ID: "other-turn", Status: "completed"}}})
			send(map[string]any{"method": "thread/tokenUsage/updated", "params": map[string]any{"threadId": "fake-thread", "turnId": turnID, "tokenUsage": map[string]any{"last": map[string]int{"totalTokens": 42}}}})
			if mode == "failed" {
				finish("failed")
			} else {
				finish("completed")
			}
		case "turn/interrupt":
			status = "interrupted"
			save()
			reply(map[string]any{})
			finish("interrupted")
		default:
			os.Exit(2)
		}
	}
	os.Exit(0)
}

func fakeProcessOptions(t *testing.T, mode string) ProcessOptions {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return ProcessOptions{Executable: executable, ExpectedVersion: "test-only", Arguments: []string{"-test.run=^TestProtocolProcess$", "--", "--fake-codex", mode, filepath.Join(t.TempDir(), "thread.json")}, testSkipVersion: true}
}

func fakeProcess(t *testing.T, mode string) *Process {
	t.Helper()
	process, err := StartProcess(t.Context(), fakeProcessOptions(t, mode))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := process.Close(); err != nil {
			t.Error(err)
		}
	})
	return process
}

func runtimeCommand() control.Command {
	return control.Command{Kind: "execute", JobID: "job-1", AttemptID: "attempt-1", ProjectID: "registered", Prompt: "inspect disposable files", Profile: control.Profile{ModelPair: control.ModelPair{Model: "fake-model", Effort: "medium"}, Permission: "read-only", MaxSeconds: 10, MaxTokens: 1000}, Deadline: time.Now().Add(10 * time.Second)}
}

func TestAppServerHandshakeDiscoveryTurnAndContinuation(t *testing.T) {
	process := fakeProcess(t, "complete")
	runtime := NewAppServer(process)
	models, err := runtime.Models(t.Context())
	if err != nil || len(models) != 2 {
		t.Fatalf("paginated models = %v / %v", models, err)
	}
	root := t.TempDir()
	command := runtimeCommand()
	var events []control.WorkerEvent
	if err := runtime.Execute(t.Context(), command, root, func(event control.WorkerEvent) error { events = append(events, event); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].ThreadID != "fake-thread" || events[1].State != control.Running || events[2].Tokens != 42 || events[3].State != control.Completed || events[3].Text != "protocol result" {
		t.Fatalf("turn facts = %+v", events)
	}
	command.ThreadID = "fake-thread"
	command.AttemptID = "attempt-2"
	if err := runtime.Execute(t.Context(), command, root, func(control.WorkerEvent) error { return nil }); err != nil {
		t.Fatal("host-local continuation failed", err)
	}
	if err := runtime.Execute(t.Context(), command, t.TempDir(), func(control.WorkerEvent) error { return nil }); !errors.Is(err, control.ErrForbidden) {
		t.Fatal("continuation accepted another directory", err)
	}
}

func TestAppServerApprovalIsBoundedScopedAndExpires(t *testing.T) {
	for _, mode := range []string{"approval", "outside-approval", "expire-approval"} {
		t.Run(mode, func(t *testing.T) {
			process := fakeProcess(t, mode)
			runtime := NewAppServer(process)
			command := runtimeCommand()
			command.Profile.Permission = "workspace-write"
			if mode == "expire-approval" {
				command.Deadline = time.Now().Add(100 * time.Millisecond)
			}
			var final string
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
			defer cancel()
			err := runtime.Execute(ctx, command, t.TempDir(), func(event control.WorkerEvent) error {
				if event.State == control.Completed {
					final = event.State
				}
				if event.Approval == nil {
					return nil
				}
				if mode == "expire-approval" {
					return nil
				}
				answer := command
				answer.Kind = "approve"
				answer.ThreadID = event.ThreadID
				answer.TurnID = event.TurnID
				answer.ApprovalID = event.Approval.ID
				answer.ApprovalDecision = "accept"
				wrong := answer
				wrong.AttemptID = "another-attempt"
				if err := runtime.Respond(ctx, wrong); !errors.Is(err, control.ErrForbidden) {
					t.Fatal("cross-attempt approval accepted")
				}
				if mode == "outside-approval" {
					if event.Approval.CanAccept {
						t.Fatal("approval escaped registered root")
					}
					if err := runtime.Respond(ctx, answer); !errors.Is(err, control.ErrForbidden) {
						t.Fatal("outside approval accepted")
					}
					answer.ApprovalDecision = "decline"
				}
				return runtime.Respond(ctx, answer)
			})
			if err != nil || final != control.Completed {
				t.Fatalf("approval flow = %s / %v", final, err)
			}
		})
	}
}

func TestAppServerCancellationUsesConfirmedSavedTurn(t *testing.T) {
	options := fakeProcessOptions(t, "wait-cancel")
	runtime, err := NewSupervisor(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	root := t.TempDir()
	command := runtimeCommand()
	record := &Record{Command: command, Root: root}
	err = runtime.Execute(ctx, command, root, func(event control.WorkerEvent) error {
		if event.State == control.Running {
			record.ThreadID, record.TurnID = event.ThreadID, event.TurnID
			cancel()
		}
		return nil
	})
	if err == nil {
		t.Fatal("canceled execution must reconcile instead of guessing completion")
	}
	event, err := runtime.Reconcile(t.Context(), record)
	if err != nil || event.State != control.Canceled {
		t.Fatalf("confirmed saved cancellation = %+v / %v", event, err)
	}
}

func TestAppServerDeathLostReplyAndProtocolErrorsNeverReplay(t *testing.T) {
	for _, mode := range []string{"death", "lost-reply", "rpc-error", "unsupported", "failed"} {
		t.Run(mode, func(t *testing.T) {
			process := fakeProcess(t, mode)
			runtime := NewAppServer(process)
			command := runtimeCommand()
			root := t.TempDir()
			record := &Record{Command: command, Root: root}
			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()
			var final string
			err := runtime.Execute(ctx, command, root, func(event control.WorkerEvent) error {
				if event.ThreadID != "" {
					record.ThreadID = event.ThreadID
				}
				if event.TurnID != "" {
					record.TurnID = event.TurnID
				}
				final = event.State
				return nil
			})
			if mode == "failed" {
				if err != nil || final != control.Failed {
					t.Fatal("failed turn was reported as success")
				}
				return
			}
			if err == nil {
				t.Fatal("uncertain protocol execution was reported as success")
			}
			if strings.Contains(err.Error(), "private-path-secret") {
				t.Fatal("raw protocol diagnostics escaped")
			}
			if mode == "lost-reply" {
				event, readErr := runtime.Reconcile(t.Context(), record)
				if (readErr != nil && !errors.Is(readErr, control.ErrUnavailable)) || event.State != control.NeedsAttention || record.TurnID != "" {
					t.Fatal("a local request ID was treated as Codex turn deduplication")
				}
			}
		})
	}
}

func TestPinnedVersionCannotBeBypassedByCustomArguments(t *testing.T) {
	options := fakeProcessOptions(t, "complete")
	options.testSkipVersion = false
	if process, err := StartProcess(t.Context(), options); err == nil {
		_ = process.Close()
		t.Fatal("custom arguments bypassed exact version verification")
	}
}

type (
	uncertainRuntime struct{ calls atomicCounter }
	atomicCounter    struct {
		mu    sync.Mutex
		value int
	}
)

func (r *uncertainRuntime) Models(context.Context) ([]control.ModelPair, error) {
	return []control.ModelPair{{Model: "fake-model", Effort: "medium"}}, nil
}

func (r *uncertainRuntime) Execute(context.Context, control.Command, string, func(control.WorkerEvent) error) error {
	r.calls.mu.Lock()
	r.calls.value++
	r.calls.mu.Unlock()
	return errors.New("must not execute during recovery")
}
func (*uncertainRuntime) Respond(context.Context, control.Command) error { return nil }
func (*uncertainRuntime) Reconcile(_ context.Context, record *Record) (control.WorkerEvent, error) {
	return control.WorkerEvent{State: control.NeedsAttention, ThreadID: record.ThreadID, TurnID: record.TurnID, Text: "uncertain saved execution"}, nil
}

func TestWorkerLedgerRecoveryDeduplicationAndResultRetention(t *testing.T) {
	ledger, err := OpenLedger(t.Context(), filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ledger.Close() }()
	root := t.TempDir()
	command := runtimeCommand()
	if _, created, err := ledger.Accept(t.Context(), command, root); err != nil || !created {
		t.Fatal(err)
	}
	if _, created, err := ledger.Accept(t.Context(), command, root); err != nil || created {
		t.Fatal("duplicate attempt was accepted again")
	}
	changed := command
	changed.Prompt = "different"
	if _, _, err := ledger.Accept(t.Context(), changed, root); !errors.Is(err, control.ErrConflict) {
		t.Fatal("conflicting attempt payload accepted")
	}
	if _, err := ledger.Append(t.Context(), command.AttemptID, control.WorkerEvent{State: "progress", ThreadID: "thread-before-lost-turn-reply"}); err != nil {
		t.Fatal(err)
	}
	runtime := &uncertainRuntime{}
	worker, err := NewWorker(t.Context(), ledger, WorkerOptions{HostID: "fake", CodexVersion: "test", RetentionDays: 1, Projects: []LocalProject{{ID: command.ProjectID, Root: root, Profiles: []control.Profile{command.Profile}}}}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runtime.calls.value != 0 {
		t.Fatal("worker restart replayed a possibly mutating operation")
	}
	record, err := ledger.Get(t.Context(), command.AttemptID)
	if err != nil || record.Stage != control.NeedsAttention {
		t.Fatal("uncertain state was not durable")
	}
	if count, err := ledger.Purge(t.Context(), time.Now().Add(time.Hour)); err != nil || count != 0 {
		t.Fatal("retention removed uncertain work")
	}
	if _, err := ledger.Append(t.Context(), command.AttemptID, control.WorkerEvent{State: control.Completed, ThreadID: record.ThreadID, TurnID: "confirmed-turn", Text: "retained result"}); err != nil {
		t.Fatal(err)
	}
	if count, err := ledger.Purge(t.Context(), time.Now().Add(time.Hour)); err != nil || count != 0 {
		t.Fatal("retention removed an unacknowledged result")
	}
	record, err = ledger.Get(t.Context(), command.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Ack(t.Context(), command.AttemptID, record.Sequence); err != nil {
		t.Fatal(err)
	}
	if count, err := ledger.Purge(t.Context(), time.Now().Add(time.Hour)); err != nil || count != 1 {
		t.Fatal("acknowledged terminal result was not purged")
	}
}

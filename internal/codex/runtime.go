package codex

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/control"
)

const (
	fieldType        = "type"
	fieldThreadID    = "threadId"
	fieldTurnID      = "turnId"
	itemAgentMessage = "agentMessage"
)

// Runtime is the worker's bounded agent-runtime port. Test implementations
// execute no user repositories and consume no live inference credentials.
type Runtime interface {
	Models(context.Context) ([]control.ModelPair, error)
	Execute(context.Context, control.Command, string, func(control.WorkerEvent) error) error
	Respond(context.Context, control.Command) error
	Reconcile(context.Context, *Record) (control.WorkerEvent, error)
}

type pendingApproval struct {
	id        json.RawMessage
	command   control.Command
	canAccept bool
	expires   time.Time
}

// AppServer translates the installed stable JSON protocol into scoped worker events.
type AppServer struct {
	process   *Process
	mu        sync.Mutex
	approvals map[string]*pendingApproval
}

// NewAppServer adapts one supervised local process.
func NewAppServer(process *Process) *AppServer {
	return &AppServer{process: process, approvals: make(map[string]*pendingApproval)}
}

// Models discovers exact model/effort combinations without implying inference entitlement.
func (a *AppServer) Models(ctx context.Context) ([]control.ModelPair, error) {
	var account struct {
		Account json.RawMessage `json:"account"`
	}
	if err := a.process.Call(ctx, "account/read", map[string]bool{"refreshToken": false}, &account); err != nil {
		return nil, err
	}
	if len(account.Account) == 0 || string(account.Account) == "null" {
		return nil, errors.New("codex requires host-local sign-in")
	}
	var response struct {
		Data []struct {
			Model   string `json:"model"`
			Efforts []struct {
				Effort string `json:"reasoningEffort"`
			} `json:"supportedReasoningEfforts"`
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}
	var pairs []control.ModelPair
	params := map[string]any{"limit": 100}
	for page := 0; page < 10; page++ {
		if err := a.process.Call(ctx, "model/list", params, &response); err != nil {
			return nil, err
		}
		for _, model := range response.Data {
			for _, effort := range model.Efforts {
				pairs = append(pairs, control.ModelPair{Model: model.Model, Effort: effort.Effort})
			}
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			return pairs, nil
		}
		params["cursor"] = *response.NextCursor
	}
	return nil, errors.New("codex model catalog exceeds the worker bound")
}

type protocolTurn struct {
	ID     string         `json:"id"`
	Status string         `json:"status"`
	Items  []protocolItem `json:"items"`
}
type protocolItem struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Phase string `json:"phase"`
}
type protocolThread struct {
	ID       string         `json:"id"`
	Cwd      string         `json:"cwd"`
	Turns    []protocolTurn `json:"turns"`
	Archived bool           `json:"archived"`
}

// Execute persists the thread binding before turn/start, then waits for confirmed final status.
func (a *AppServer) Execute(ctx context.Context, command control.Command, root string, emit func(control.WorkerEvent) error) (executionErr error) {
	// An uncertain turn is never replayed. Stop the entire owned process tree
	// before recovery reads its saved history through a fresh app-server.
	defer func() {
		if executionErr != nil {
			_ = a.process.Close()
		}
	}()
	sandbox := control.PermissionReadOnly
	sandboxPolicy := map[string]any{fieldType: "readOnly", "networkAccess": false}
	if command.Profile.Permission == control.PermissionWorkspaceWrite {
		sandbox = control.PermissionWorkspaceWrite
		sandboxPolicy = map[string]any{fieldType: "workspaceWrite", "writableRoots": []string{root}, "networkAccess": false}
	}
	method := "thread/start"
	params := map[string]any{"cwd": root, "model": command.Profile.Model, "sandbox": sandbox, "approvalPolicy": "on-request"}
	if command.ThreadID != "" {
		var read struct {
			Thread protocolThread `json:"thread"`
		}
		if err := a.process.Call(ctx, "thread/read", map[string]any{fieldThreadID: command.ThreadID, "includeTurns": true}, &read); err != nil {
			return err
		}
		if !sameRoot(read.Thread.Cwd, root) || read.Thread.Archived {
			return control.ErrForbidden
		}
		for _, turn := range read.Thread.Turns {
			if turn.Status == "inProgress" {
				return errors.New("codex conversation already has an active turn")
			}
		}
		method = "thread/resume"
		params[fieldThreadID] = command.ThreadID
	}
	var threadResponse struct {
		Thread protocolThread `json:"thread"`
	}
	if err := a.process.Call(ctx, method, params, &threadResponse); err != nil {
		return err
	}
	if threadResponse.Thread.ID == "" || !sameRoot(threadResponse.Thread.Cwd, root) {
		return errors.New("codex did not confirm the registered project directory")
	}
	command.ThreadID = threadResponse.Thread.ID
	if err := emit(control.WorkerEvent{State: control.Progress, ThreadID: command.ThreadID, Text: "Conversation saved; turn submission is starting"}); err != nil {
		return err
	}
	var started struct {
		Turn protocolTurn `json:"turn"`
	}
	if err := a.process.Call(ctx, "turn/start", map[string]any{fieldThreadID: command.ThreadID, "cwd": root, "input": []map[string]any{{fieldType: "text", "text": command.Prompt, "text_elements": []any{}}}, "model": command.Profile.Model, "effort": command.Profile.Effort, "approvalPolicy": "on-request", "sandboxPolicy": sandboxPolicy}, &started); err != nil {
		return err
	}
	if started.Turn.ID == "" {
		return errors.New("codex did not confirm a turn ID")
	}
	command.TurnID = started.Turn.ID
	if err := emit(control.WorkerEvent{State: control.Running, ThreadID: command.ThreadID, TurnID: command.TurnID, Text: "Codex turn accepted"}); err != nil {
		return err
	}
	defer func() {
		a.mu.Lock()
		for id, pending := range a.approvals {
			if pending.command.AttemptID == command.AttemptID {
				delete(a.approvals, id)
			}
		}
		a.mu.Unlock()
	}()
	result := ""
	expiry := time.NewTicker(time.Second)
	defer expiry.Stop()
	for {
		select {
		case <-ctx.Done():
			interruptCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = a.process.Call(interruptCtx, "turn/interrupt", map[string]string{fieldThreadID: command.ThreadID, fieldTurnID: command.TurnID}, nil)
			cancel()
			return errors.New("execution deadline or worker shutdown interrupted the turn; final state requires reconciliation")
		case <-a.process.Done():
			return errors.New("codex process ended before the turn's final status was confirmed")
		case <-expiry.C:
			a.mu.Lock()
			for id, pending := range a.approvals {
				if time.Now().After(pending.expires) {
					_ = a.process.Respond(pending.id, map[string]string{"decision": "decline"})
					delete(a.approvals, id)
				}
			}
			a.mu.Unlock()
		case message := <-a.process.Events():
			var event struct {
				ThreadID   string       `json:"threadId"`
				TurnID     string       `json:"turnId"`
				Turn       protocolTurn `json:"turn"`
				Item       protocolItem `json:"item"`
				TokenUsage struct {
					Last struct {
						Total int64 `json:"totalTokens"`
					} `json:"last"`
				} `json:"tokenUsage"`
			}
			if err := json.Unmarshal(message.Params, &event); err != nil {
				continue
			}
			if event.ThreadID != command.ThreadID || (event.TurnID != "" && event.TurnID != command.TurnID) {
				continue
			}
			if len(message.ID) != 0 {
				if message.Method != "item/commandExecution/requestApproval" && message.Method != "item/fileChange/requestApproval" {
					_ = a.process.Reject(message.ID)
					return errors.New("codex requested an unsupported interaction; owner attention is required")
				}
				var details struct {
					Command   string `json:"command"`
					Reason    string `json:"reason"`
					GrantRoot string `json:"grantRoot"`
					Cwd       string `json:"cwd"`
				}
				_ = json.Unmarshal(message.Params, &details)
				canAccept := message.Method == "item/fileChange/requestApproval" && command.Profile.Permission == control.PermissionWorkspaceWrite && details.GrantRoot != "" && withinRoot(details.GrantRoot, root)
				id := rand.Text()
				expires := minTime(command.Deadline, time.Now().UTC().Add(5*time.Minute))
				approval := &control.Approval{ID: id, Method: message.Method, Description: strings.TrimSpace(details.Command + " " + details.Reason), CanAccept: canAccept, ExpiresAt: expires}
				if len(approval.Description) > 2048 {
					approval.Description = approval.Description[:2048]
				}
				a.mu.Lock()
				a.approvals[id] = &pendingApproval{id: message.ID, command: command, canAccept: canAccept, expires: expires}
				a.mu.Unlock()
				if err := emit(control.WorkerEvent{State: control.AwaitingApproval, ThreadID: command.ThreadID, TurnID: command.TurnID, Approval: approval, Text: "Owner approval is required"}); err != nil {
					return err
				}
				continue
			}
			switch message.Method {
			case "item/completed":
				if event.Item.Type == itemAgentMessage && (event.Item.Phase == "final_answer" || event.Item.Phase == "") {
					result = event.Item.Text
				}
			case "thread/tokenUsage/updated":
				if err := emit(control.WorkerEvent{State: control.Progress, ThreadID: command.ThreadID, TurnID: command.TurnID, Tokens: event.TokenUsage.Last.Total, Text: "Execution usage updated"}); err != nil {
					return err
				}
			case "turn/completed":
				if event.Turn.ID != command.TurnID {
					continue
				}
				state := control.Failed
				switch event.Turn.Status {
				case "completed":
					state = control.Completed
					for _, item := range event.Turn.Items {
						if item.Type == itemAgentMessage && (item.Phase == "final_answer" || item.Phase == "") {
							result = item.Text
						}
					}
				case "interrupted":
					state = control.Canceled
					result = "Codex turn interruption confirmed"
				default:
					result = "Codex turn failed"
				}
				return emit(control.WorkerEvent{State: state, ThreadID: command.ThreadID, TurnID: command.TurnID, Text: result})
			}
		}
	}
}

// Respond handles exact pending approvals and scoped cancellation.
func (a *AppServer) Respond(ctx context.Context, command control.Command) error {
	if command.Kind == control.CommandCancel {
		return a.process.Call(ctx, "turn/interrupt", map[string]string{fieldThreadID: command.ThreadID, fieldTurnID: command.TurnID}, nil)
	}
	if command.Kind != control.CommandApprove {
		return control.ErrInvalid
	}
	a.mu.Lock()
	pending, ok := a.approvals[command.ApprovalID]
	a.mu.Unlock()
	if !ok || pending.command.AttemptID != command.AttemptID || pending.command.JobID != command.JobID || pending.command.ThreadID != command.ThreadID || pending.command.TurnID != command.TurnID {
		return control.ErrForbidden
	}
	decision := command.ApprovalDecision
	if decision != "accept" && decision != "decline" {
		return control.ErrInvalid
	}
	if decision == "accept" && (!pending.canAccept || time.Now().After(pending.expires)) {
		return control.ErrForbidden
	}
	if err := a.process.Respond(pending.id, map[string]string{"decision": decision}); err != nil {
		return err
	}
	a.mu.Lock()
	delete(a.approvals, command.ApprovalID)
	a.mu.Unlock()
	return nil
}

// Reconcile reads existing thread evidence and never starts or resumes a turn.
func (a *AppServer) Reconcile(ctx context.Context, record *Record) (control.WorkerEvent, error) {
	event := control.WorkerEvent{ThreadID: record.ThreadID, TurnID: record.TurnID, State: control.NeedsAttention, Text: "Execution is uncertain; the saved turn was not replayed"}
	if record.ThreadID == "" || record.TurnID == "" {
		return event, nil
	}
	var response struct {
		Thread protocolThread `json:"thread"`
	}
	if err := a.process.Call(ctx, "thread/read", map[string]any{fieldThreadID: record.ThreadID, "includeTurns": true}, &response); err != nil {
		return event, control.ErrUnavailable
	}
	if response.Thread.ID != record.ThreadID || !sameRoot(response.Thread.Cwd, record.Root) {
		return event, nil
	}
	for _, turn := range response.Thread.Turns {
		if turn.ID == record.TurnID {
			switch turn.Status {
			case "completed":
				event.State = control.Completed
				event.Text = ""
				for _, item := range turn.Items {
					if item.Type == itemAgentMessage {
						event.Text = item.Text
					}
				}
			case "interrupted":
				event.State = control.Canceled
				event.Text = "Interrupted turn confirmed from saved Codex history"
			case "failed":
				event.State = control.Failed
				event.Text = "Failed turn confirmed from saved Codex history"
			}
			return event, nil
		}
	}
	return event, nil
}

func sameRoot(left, right string) bool {
	a, err := filepath.EvalSymlinks(left)
	if err != nil {
		return false
	}
	b, err := filepath.EvalSymlinks(right)
	if err != nil {
		return false
	}
	return equalPath(a, b)
}

func equalPath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func withinRoot(path, root string) bool {
	a, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	b, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(b, a)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

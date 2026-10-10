package codex

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wellivea1/alexa-brainslug/internal/control"
	"github.com/wellivea1/alexa-brainslug/internal/controlapi"
)

// LocalProject grants only owner-registered roots and bounded execution profiles.
type LocalProject struct {
	ID       string            `json:"id"`
	Root     string            `json:"root"`
	Profiles []control.Profile `json:"profiles"`
}

// WorkerOptions contains host-local policy; roots and credentials never leave this host.
type WorkerOptions struct {
	HostID        string         `json:"host_id"`
	ControllerURL string         `json:"controller_url"`
	CodexVersion  string         `json:"codex_version"`
	RetentionDays int            `json:"retention_days"`
	Projects      []LocalProject `json:"projects"`
}

type activeExecution struct {
	attemptID string
	cancel    context.CancelFunc
}

// Worker owns execution, deadline enforcement, and result spooling across LAN loss.
type Worker struct {
	mu      sync.Mutex
	ledger  *Ledger
	options WorkerOptions
	runtime Runtime
	host    control.Host
	active  *activeExecution
	wg      sync.WaitGroup
}

// NewWorker validates and canonicalizes local roots before advertising authority.
func NewWorker(ctx context.Context, ledger *Ledger, options WorkerOptions, runtime Runtime) (*Worker, error) {
	if ledger == nil || runtime == nil || options.HostID == "" || options.CodexVersion == "" || len(options.Projects) == 0 || options.RetentionDays < 1 || options.RetentionDays > 365 {
		return nil, control.ErrInvalid
	}
	models, err := runtime.Models(ctx)
	if err != nil {
		return nil, err
	}
	worker := &Worker{ledger: ledger, options: options, runtime: runtime, host: control.Host{ID: options.HostID, CodexVersion: options.CodexVersion}}
	seen := make(map[string]bool)
	for i := range worker.options.Projects {
		project := &worker.options.Projects[i]
		if project.ID == "" || seen[project.ID] || len(project.Profiles) == 0 || !filepath.IsAbs(project.Root) {
			return nil, control.ErrInvalid
		}
		root, err := filepath.EvalSymlinks(project.Root)
		if err != nil {
			return nil, errors.New("registered worker project is unavailable")
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return nil, control.ErrInvalid
		}
		project.Root = filepath.Clean(root)
		seen[project.ID] = true
		worker.host.ProjectIDs = append(worker.host.ProjectIDs, project.ID)
		for _, profile := range project.Profiles {
			if profile.MaxSeconds < 1 || profile.MaxSeconds > 3600 || profile.MaxTokens < 1 || profile.MaxTokens > 1000000 ||
				(profile.Permission != control.PermissionReadOnly && profile.Permission != control.PermissionWorkspaceWrite) || !slices.Contains(models, profile.ModelPair) {
				return nil, errors.New("worker profile is outside the local runtime catalog or permission bounds")
			}
			if !slices.Contains(worker.host.Models, profile.ModelPair) {
				worker.host.Models = append(worker.host.Models, profile.ModelPair)
			}
		}
	}
	return worker, nil
}

func (w *Worker) project(id string) *LocalProject {
	for i := range w.options.Projects {
		if w.options.Projects[i].ID == id {
			return &w.options.Projects[i]
		}
	}
	return nil
}

func (w *Worker) validate(command *control.Command) (string, error) {
	project := w.project(command.ProjectID)
	if project == nil || command.JobID == "" || command.AttemptID == "" || len(command.Prompt) > 16384 {
		return "", control.ErrForbidden
	}
	canonical, err := filepath.EvalSymlinks(project.Root)
	if err != nil || !equalPath(canonical, project.Root) {
		return "", control.ErrUnavailable
	}
	if command.Kind != control.CommandExecute {
		return project.Root, nil
	}
	if command.Prompt == "" || command.Deadline.IsZero() || !command.Deadline.After(time.Now()) || command.Deadline.After(time.Now().Add(time.Hour+5*time.Second)) {
		return "", control.ErrInvalid
	}
	for _, allowed := range project.Profiles {
		if allowed.ModelPair == command.Profile.ModelPair && command.Profile.MaxSeconds > 0 && command.Profile.MaxSeconds <= allowed.MaxSeconds &&
			command.Profile.MaxTokens > 0 && command.Profile.MaxTokens <= allowed.MaxTokens &&
			(command.Profile.Permission == control.PermissionReadOnly || command.Profile.Permission == allowed.Permission) &&
			command.Deadline.Before(time.Now().Add(time.Duration(command.Profile.MaxSeconds)*time.Second+5*time.Second)) {
			return project.Root, nil
		}
	}
	return "", control.ErrForbidden
}

// Handle accepts only typed operations from an authenticated live controller session.
func (w *Worker) Handle(ctx context.Context, command control.Command) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	root, err := w.validate(&command)
	if err != nil {
		return err
	}
	if command.Kind == control.CommandExecute {
		if w.active != nil && w.active.attemptID != command.AttemptID {
			return control.ErrQueueFull
		}
		record, created, err := w.ledger.Accept(ctx, command, root)
		if err != nil || !created {
			return err
		}
		execCtx, cancel := context.WithDeadline(ctx, command.Deadline)
		w.active = &activeExecution{attemptID: command.AttemptID, cancel: cancel}
		w.wg.Add(1)
		go w.execute(execCtx, record, cancel)
		return nil
	}
	record, err := w.ledger.Get(ctx, command.AttemptID)
	if errors.Is(err, control.ErrNotFound) && command.Kind == control.CommandResolve {
		// Record a terminal tombstone before acknowledging an unknown attempt,
		// so a delayed original dispatch cannot later execute it.
		original := command
		original.Kind = control.CommandExecute
		if _, _, err := w.ledger.Accept(ctx, original, root); err != nil {
			return err
		}
		_, err := w.ledger.Append(ctx, command.AttemptID, control.WorkerEvent{State: control.Failed, Text: "Owner acknowledged an uncertain dispatch absent from this worker ledger; verify project files before continuing"})
		return err
	}
	if err != nil {
		return err
	}
	if record.Command.JobID != command.JobID || record.Command.ProjectID != command.ProjectID ||
		(command.ThreadID != "" && record.ThreadID != command.ThreadID) || (command.TurnID != "" && record.TurnID != command.TurnID) {
		return control.ErrForbidden
	}
	if control.Terminal(record.Stage) {
		return nil
	}
	if command.Kind == control.CommandResolve {
		if record.Stage != control.NeedsAttention || w.active != nil {
			return control.ErrConflict
		}
		_, err := w.ledger.Append(ctx, command.AttemptID, control.WorkerEvent{State: control.Failed, ThreadID: record.ThreadID, TurnID: record.TurnID, Text: "Owner acknowledged uncertain execution after the worker stopped its process; files may have changed"})
		return err
	}
	if command.Kind != control.CommandCancel && command.Kind != control.CommandApprove {
		return control.ErrInvalid
	}
	// Use locally saved IDs, never a caller's replacement execution handle.
	command.ThreadID, command.TurnID = record.ThreadID, record.TurnID
	if command.Kind == control.CommandCancel {
		if w.active != nil && w.active.attemptID == command.AttemptID {
			w.active.cancel()
			return nil
		}
		// Recovery has already stopped the owned process. Keep its uncertain
		// evidence available for spooling rather than breaking reconnection.
		return nil
	}
	responseCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := w.runtime.Respond(responseCtx, command); err != nil {
		return err
	}
	_, err = w.ledger.Append(ctx, command.AttemptID, control.WorkerEvent{State: control.Running, ThreadID: record.ThreadID, TurnID: record.TurnID, Text: "Bound approval response recorded"})
	return err
}

func (w *Worker) execute(ctx context.Context, record *Record, cancel context.CancelFunc) {
	defer w.wg.Done()
	defer cancel()
	err := w.runtime.Execute(ctx, record.Command, record.Root, func(event control.WorkerEvent) error {
		persistCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer done()
		_, saveErr := w.ledger.Append(persistCtx, record.Command.AttemptID, event)
		if saveErr == nil && event.Tokens > record.Command.Profile.MaxTokens {
			cancel()
		}
		return saveErr
	})
	if err != nil {
		readCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		_ = w.reconcile(readCtx, record.Command.AttemptID)
		done()
	}
	w.mu.Lock()
	w.active = nil
	w.mu.Unlock()
}

func (w *Worker) reconcile(ctx context.Context, id string) error {
	record, err := w.ledger.Get(ctx, id)
	if err != nil || control.Terminal(record.Stage) {
		return err
	}
	event := control.WorkerEvent{State: control.NeedsAttention, ThreadID: record.ThreadID, TurnID: record.TurnID, Text: "Execution is uncertain; the original attempt was not replayed"}
	project := w.project(record.Command.ProjectID)
	if project != nil && sameRoot(record.Root, project.Root) {
		if confirmed, readErr := w.runtime.Reconcile(ctx, record); readErr == nil {
			event = confirmed
		}
	}
	_, err = w.ledger.Append(ctx, id, event)
	return err
}

// Recover reads saved evidence before opening a controller connection; it never executes an old command.
func (w *Worker) Recover(ctx context.Context) error {
	records, err := w.ledger.Records(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if !control.Terminal(record.Stage) && record.Stage != control.NeedsAttention {
			if err := w.reconcile(ctx, record.Command.AttemptID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Run maintains an outbound mTLS session. Accepted work uses ctx, rather than the connection lifetime.
func (w *Worker) Run(ctx context.Context, configuration *tls.Config) error {
	if configuration == nil || configuration.InsecureSkipVerify || configuration.RootCAs == nil || len(configuration.Certificates) == 0 {
		return control.ErrInvalid
	}
	parsed, err := url.Parse(w.options.ControllerURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return control.ErrInvalid
	}
	if err := w.Recover(ctx); err != nil {
		return err
	}
	parsed.Scheme, parsed.Path = "wss", "/v1/worker"
	tlsOptions := configuration.Clone()
	tlsOptions.MinVersion = tls.VersionTLS13
	transport := &http.Transport{TLSClientConfig: tlsOptions}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return control.ErrForbidden }}
	defer func() {
		w.mu.Lock()
		if w.active != nil {
			w.active.cancel()
		}
		w.mu.Unlock()
		w.wg.Wait()
	}()
	backoff := time.Second
	for ctx.Err() == nil {
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		connection, response, dialErr := websocket.Dial(dialCtx, parsed.String(), &websocket.DialOptions{HTTPClient: client, Subprotocols: []string{"brainslug-worker.v1"}})
		cancel()
		if dialErr != nil && response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if dialErr == nil {
			connection.SetReadLimit(256 << 10)
			_ = w.session(ctx, connection)
			_ = connection.Close(websocket.StatusNormalClosure, "worker reconnecting")
			backoff = time.Second
		}
		_, _ = w.ledger.Purge(ctx, time.Now().Add(-time.Duration(w.options.RetentionDays)*24*time.Hour))
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	return nil
}

func (w *Worker) session(ctx context.Context, connection *websocket.Conn) error {
	if connection.Subprotocol() != "brainslug-worker.v1" {
		return control.ErrConflict
	}
	connectionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := w.write(connectionCtx, connection, controlapi.Frame{Version: control.ProtocolVersion, Type: "hello", Host: &w.host}); err != nil {
		return err
	}
	helloCtx, helloCancel := context.WithTimeout(connectionCtx, 5*time.Second)
	ready, err := controlapi.ReadFrame(helloCtx, connection)
	helloCancel()
	if err != nil || ready.Type != "ready" {
		return control.ErrConflict
	}
	frames := make(chan controlapi.Frame, 32)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer cancel()
		for {
			frame, err := controlapi.ReadFrame(connectionCtx, connection)
			if err != nil {
				return
			}
			select {
			case frames <- frame:
			case <-connectionCtx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); <-readerDone }()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	sent := make(map[string]int64)
	receipts := make(map[string]bool)
	initial := true
	lastHeartbeat := time.Time{}
	for {
		select {
		case <-connectionCtx.Done():
			return connectionCtx.Err()
		case frame := <-frames:
			if connectionCtx.Err() != nil {
				return connectionCtx.Err()
			}
			switch frame.Type {
			case "ack":
				if err := w.ledger.Ack(ctx, frame.AttemptID, frame.Sequence); err != nil {
					return err
				}
				record, err := w.ledger.Get(ctx, frame.AttemptID)
				if err != nil {
					return err
				}
				if control.Terminal(record.Stage) && record.Acknowledged == record.Sequence {
					if err := w.write(connectionCtx, connection, controlapi.Frame{Version: control.ProtocolVersion, Type: "receipt", AttemptID: frame.AttemptID, Sequence: frame.Sequence}); err != nil {
						return err
					}
					receipts[frame.AttemptID] = true
				}
			case "command":
				if frame.Command == nil {
					return control.ErrInvalid
				}
				if err := w.Handle(ctx, *frame.Command); err != nil {
					return err
				}
			default:
				return control.ErrInvalid
			}
		case <-ticker.C:
			if time.Since(lastHeartbeat) >= 5*time.Second {
				if err := w.write(connectionCtx, connection, controlapi.Frame{Version: control.ProtocolVersion, Type: "heartbeat"}); err != nil {
					return err
				}
				lastHeartbeat = time.Now()
			}
			var records []*Record
			var err error
			if initial {
				records, err = w.ledger.Records(ctx)
				initial = false
			} else {
				records, err = w.ledger.Pending(ctx)
			}
			if err != nil {
				return err
			}
			for _, record := range records {
				if control.Terminal(record.Stage) && record.Acknowledged == record.Sequence && !receipts[record.Command.AttemptID] {
					if err := w.write(connectionCtx, connection, controlapi.Frame{Version: control.ProtocolVersion, Type: "receipt", AttemptID: record.Command.AttemptID, Sequence: record.Sequence}); err != nil {
						return err
					}
					receipts[record.Command.AttemptID] = true
				}
				if record.Sequence <= max(sent[record.Command.AttemptID], record.Acknowledged) {
					continue
				}
				events, err := w.ledger.Unacked(ctx, record.Command.AttemptID, sent[record.Command.AttemptID])
				if err != nil {
					return err
				}
				for i := range events {
					if err := w.write(connectionCtx, connection, controlapi.Frame{Version: control.ProtocolVersion, Type: "event", Event: &events[i]}); err != nil {
						return err
					}
					sent[record.Command.AttemptID] = events[i].Sequence
				}
			}
		}
	}
}

func (*Worker) write(ctx context.Context, connection *websocket.Conn, frame controlapi.Frame) error {
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return controlapi.WriteFrame(writeCtx, connection, frame)
}

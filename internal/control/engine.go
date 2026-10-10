package control

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/security"
)

// Engine schedules durable work without tying its lifetime to an ingress request.
type Engine struct {
	mu         sync.Mutex
	options    Options
	store      Store
	dispatcher Dispatcher
	router     Router
	answerer   Answerer
	lastCancel map[string]time.Time
}

// NewEngine validates policy and restores controller state before accepting requests.
func NewEngine(ctx context.Context, store Store, options *Options, dispatcher Dispatcher, router Router, answerer Answerer) (*Engine, error) {
	if store == nil || options == nil || dispatcher == nil {
		return nil, ErrInvalid
	}
	if options.AutomaticEnabled && router == nil {
		return nil, ErrInvalid
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	var snapshot Options
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	snapshot.Secrets = append([]string(nil), options.Secrets...)
	if err := store.Recover(ctx); err != nil {
		return nil, err
	}
	return &Engine{options: snapshot, store: store, dispatcher: dispatcher, router: router, answerer: answerer, lastCancel: make(map[string]time.Time)}, nil
}

// Submit durably accepts one authorized request. Duplicate delivery returns the saved job.
func (e *Engine) Submit(ctx context.Context, request Request) (*Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.submitLocked(ctx, &request)
}

func (e *Engine) submitLocked(ctx context.Context, request *Request) (*Job, error) {
	if len(request.HostID) > 64 || len(request.ProjectID) > 64 || len(request.Profile) > 64 || len(request.Model) > 128 || len(request.Effort) > 32 || len(request.ConversationID) > 256 || len(request.Title) > 256 || len(request.TargetJobID) > 256 {
		return nil, ErrInvalid
	}
	request.Principal.Namespace = strings.TrimSpace(request.Principal.Namespace)
	request.Principal.Subject = strings.TrimSpace(request.Principal.Subject)
	request.Prompt = strings.TrimSpace(request.Prompt)
	if !request.Principal.Valid() || request.Source == "" || len(request.Source) > 64 || request.RequestID == "" || len(request.RequestID) > 256 || request.Prompt == "" || len(request.Prompt) > 16384 {
		return nil, ErrInvalid
	}
	if !e.authorized(request.Principal) {
		return nil, ErrForbidden
	}
	if request.Mode == "" {
		request.Mode = ModeManual
	}
	if request.Mode != ModeManual && request.Mode != ModeShadow && request.Mode != ModeAutomatic {
		return nil, ErrInvalid
	}
	if request.Mode == ModeAutomatic && (!e.options.AutomaticEnabled || e.router == nil) {
		return nil, fmt.Errorf("%w: automatic routing is not enabled", ErrForbidden)
	}
	if request.Mode == ModeManual || request.Mode == ModeShadow {
		decision, err := e.explicitDecision(ctx, request)
		if err != nil {
			return nil, err
		}
		if _, err := e.validateDecision(ctx, request.Principal, &decision); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(&e.options)
	if err != nil {
		return nil, err
	}
	var policy Options
	if err := json.Unmarshal(data, &policy); err != nil {
		return nil, err
	}
	policy.Projects = slices.DeleteFunc(policy.Projects, func(project Project) bool { return !permits(&project, request.Principal) })
	for i := range policy.Projects {
		policy.Projects[i].Principals = []string{request.Principal.Key()}
	}
	allowedHosts := make(map[string]bool)
	for i := range policy.Projects {
		allowedHosts[policy.Projects[i].HostID] = true
	}
	policy.Hosts = slices.DeleteFunc(policy.Hosts, func(host Host) bool { return !allowedHosts[host.ID] })
	for i := range policy.Hosts {
		policy.Hosts[i].ProjectIDs = nil
		for j := range policy.Projects {
			if policy.Projects[j].HostID == policy.Hosts[i].ID {
				policy.Hosts[i].ProjectIDs = append(policy.Hosts[i].ProjectIDs, policy.Projects[j].ID)
			}
		}
	}
	job, _, err := e.store.Accept(ctx, request, &policy)
	return job, err
}

// Get enforces ownership and current project grants for every state/result read.
func (e *Engine) Get(ctx context.Context, owner Principal, id string) (*Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.getOwned(ctx, owner, id)
}

func (e *Engine) getOwned(ctx context.Context, owner Principal, id string) (*Job, error) {
	job, err := e.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if job.Request.Principal != owner || !e.authorized(owner) {
		return nil, ErrForbidden
	}
	if job.Decision.ProjectID != "" && !permits(e.project(job.Decision.ProjectID), owner) {
		return nil, ErrForbidden
	}
	return job, nil
}

// List returns only caller-owned operational tasks.
func (e *Engine) List(ctx context.Context, owner Principal) ([]*Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.authorized(owner) {
		return nil, ErrForbidden
	}
	jobs, err := e.store.List(ctx, &owner, 20)
	if err != nil {
		return nil, err
	}
	active, err := e.store.Active(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, job := range jobs {
		seen[job.ID] = true
	}
	for _, job := range active {
		if job.Request.Principal == owner && job.AttemptID != "" && !seen[job.ID] {
			jobs = append(jobs, job)
		}
	}
	visible := make([]*Job, 0, len(jobs))
	for _, job := range jobs {
		if job.Decision.ProjectID == "" || permits(e.project(job.Decision.ProjectID), owner) {
			visible = append(visible, job)
		}
	}
	return visible, nil
}

// Catalog supplies only authorized projects, hosts, and registered conversations.
func (e *Engine) Catalog(ctx context.Context, owner Principal) (Catalog, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.catalog(ctx, owner)
}

func (e *Engine) catalog(ctx context.Context, owner Principal) (Catalog, error) {
	if !e.authorized(owner) {
		return Catalog{}, ErrForbidden
	}
	var catalog Catalog
	hosts := make(map[string]bool)
	for i := range e.options.Projects {
		project := &e.options.Projects[i]
		if permits(project, owner) {
			catalog.Projects = append(catalog.Projects, *project)
			hosts[project.HostID] = true
		}
	}
	for i := range e.options.Hosts {
		host := e.options.Hosts[i]
		if hosts[host.ID] {
			liveHosts := e.dispatcher.Hosts()
			for j := range liveHosts {
				live := &liveHosts[j]
				if live.ID == host.ID {
					host.Online, host.LastContact, host.CodexVersion = live.Online, live.LastContact, live.CodexVersion
				}
			}
			catalog.Hosts = append(catalog.Hosts, host)
		}
	}
	conversations, err := e.store.Conversations(ctx, owner)
	if err != nil {
		return Catalog{}, err
	}
	for i := range conversations {
		conversation := &conversations[i]
		if permits(e.project(conversation.ProjectID), owner) {
			catalog.Conversations = append(catalog.Conversations, *conversation)
		}
	}
	jobs, err := e.store.List(ctx, &owner, 20)
	if err != nil {
		return Catalog{}, err
	}
	for _, job := range jobs {
		if job.Decision.ProjectID == "" || permits(e.project(job.Decision.ProjectID), owner) {
			catalog.Tasks = append(catalog.Tasks, TaskSummary{ID: job.ID, Title: job.Decision.Title, State: job.State, ProjectID: job.Decision.ProjectID, ConversationID: job.ConversationID})
		}
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		return Catalog{}, err
	}
	var copied Catalog
	err = json.Unmarshal(data, &copied)
	return copied, err
}

// Events enforces job ownership before exposing a resumable event page.
func (e *Engine) Events(ctx context.Context, owner Principal, id string, after int64) ([]Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.getOwned(ctx, owner, id); err != nil {
		return nil, err
	}
	return e.store.Events(ctx, id, after, 128)
}

// Cancel confirms cancellation only before dispatch or after a worker acknowledgement.
func (e *Engine) Cancel(ctx context.Context, owner Principal, id string) (*Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, err := e.getOwned(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	return e.cancelLocked(ctx, job)
}

func (e *Engine) cancelLocked(ctx context.Context, job *Job) (*Job, error) {
	if Terminal(job.State) || job.State == CancelRequested {
		return job, nil
	}
	if job.AttemptID == "" {
		job.State = Canceled
	} else {
		job.State = CancelRequested
	}
	if err := e.update(ctx, job, "Cancellation requested"); err != nil {
		return nil, err
	}
	if job.State == CancelRequested {
		_ = e.dispatcher.Send(ctx, job.Decision.HostID, commandFor(job, CommandCancel))
	}
	return job, nil
}

// Reply replaces an awaiting clarification using a fresh idempotent ingress request.
func (e *Engine) Reply(ctx context.Context, owner Principal, id string, request Request) (*Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, err := e.getOwned(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	// Store acceptance and superseding the clarification share one transaction.
	// Retries can still retrieve an already accepted reply after the old job is canceled.
	if job.State != AwaitingInput && job.State != Canceled {
		return nil, ErrConflict
	}
	request.Principal = owner
	request.ReplyTo = id
	if request.Action == "" {
		request.Action = job.Request.Action
	}
	if request.HostID == "" {
		request.HostID = job.Request.HostID
	}
	if request.ProjectID == "" {
		request.ProjectID = job.Request.ProjectID
	}
	if request.Profile == "" {
		request.Profile = job.Request.Profile
	}
	if request.Model == "" {
		request.Model = job.Request.Model
	}
	if request.Effort == "" {
		request.Effort = job.Request.Effort
	}
	if request.Mode == "" {
		request.Mode = job.Request.Mode
	}
	if request.ProjectID == "" {
		for i := range e.options.Projects {
			project := &e.options.Projects[i]
			if !permits(project, owner) {
				continue
			}
			for _, name := range append([]string{project.ID, project.Name}, project.Aliases...) {
				if strings.EqualFold(strings.TrimSpace(request.Prompt), name) {
					if request.ProjectID != "" && request.ProjectID != project.ID {
						return nil, ErrConflict
					}
					request.ProjectID = project.ID
				}
			}
		}
	}
	if request.Action == ActionContinue && request.ConversationID == "" {
		conversations, err := e.store.Conversations(ctx, owner)
		if err != nil {
			return nil, err
		}
		for i := range conversations {
			conversation := &conversations[i]
			if strings.EqualFold(request.Prompt, conversation.ID) || strings.EqualFold(request.Prompt, conversation.Title) {
				if request.ConversationID != "" && request.ConversationID != conversation.ID {
					return nil, ErrConflict
				}
				request.ConversationID = conversation.ID
			}
		}
	}
	request.Prompt = job.Request.Prompt + "\nClarification reply: " + request.Prompt
	return e.submitLocked(ctx, &request)
}

// Approve responds only to the exact pending action and never grants session-wide authority.
func (e *Engine) Approve(ctx context.Context, owner Principal, id, approvalID, decision string) (*Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, err := e.getOwned(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	if job.State != AwaitingApproval || job.Approval == nil || job.Approval.ID != approvalID || time.Now().After(job.Approval.ExpiresAt) {
		return nil, ErrConflict
	}
	if decision != "decline" && decision != "accept" {
		return nil, ErrInvalid
	}
	if decision == "accept" && !job.Approval.CanAccept {
		return nil, ErrForbidden
	}
	command := commandFor(job, CommandApprove)
	command.ApprovalID = approvalID
	command.ApprovalDecision = decision
	if err := e.dispatcher.Send(ctx, job.Decision.HostID, command); err != nil {
		return nil, err
	}
	return job, nil
}

func commandFor(job *Job, kind string) Command {
	return Command{Kind: kind, JobID: job.ID, AttemptID: job.AttemptID, ProjectID: job.Decision.ProjectID, ThreadID: job.ThreadID, TurnID: job.TurnID, Prompt: job.Request.Prompt, Profile: job.Profile, Deadline: job.Deadline}
}

func (e *Engine) update(ctx context.Context, job *Job, message string) error {
	job.Problem = security.RedactString(job.Problem, e.options.Secrets...)
	return e.store.Update(ctx, job, job.Revision, security.RedactString(message, e.options.Secrets...))
}

// Run owns routing, scheduling, retention, and shutdown under one process context.
func (e *Engine) Run(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	purge := time.NewTicker(time.Hour)
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := e.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.WarnContext(ctx, "controller scheduler", "error", security.RedactString(err.Error(), e.options.Secrets...))
			}
		case <-purge.C:
			if _, err := e.store.Purge(ctx, time.Now().Add(-time.Duration(e.options.RetentionDays)*24*time.Hour)); err != nil {
				return err
			}
		}
	}
}

// Tick performs one bounded scheduler pass; it is also the deterministic test seam.
func (e *Engine) Tick(ctx context.Context) error {
	e.mu.Lock()
	jobs, err := e.store.Active(ctx)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	var next *Job
	for _, job := range jobs {
		if job.State == Accepted {
			next = job
			break
		}
	}
	if next != nil {
		next.State = Routing
		err = e.update(ctx, next, "Routing saved request")
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if next != nil {
		if err := e.route(ctx, next); err != nil {
			return err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	jobs, err = e.store.Active(ctx)
	if err != nil {
		return err
	}
	busyHosts := make(map[string]bool)
	busyThreads := make(map[string]bool)
	active := 0
	for _, job := range jobs {
		if job.AttemptID != "" && !Terminal(job.State) {
			busyHosts[job.Decision.HostID] = true
			if job.ThreadID != "" {
				busyThreads[job.ThreadID] = true
			}
			active++
		}
	}
	for _, job := range jobs {
		if job.State == AwaitingApproval && job.Approval != nil && time.Now().After(job.Approval.ExpiresAt) {
			command := commandFor(job, CommandApprove)
			command.ApprovalID = job.Approval.ID
			command.ApprovalDecision = "decline"
			_ = e.dispatcher.Send(ctx, job.Decision.HostID, command)
		}
		if job.AttemptID != "" && job.State != CancelRequested && job.State != NeedsAttention {
			live := false
			liveHosts := e.dispatcher.Hosts()
			for i := range liveHosts {
				host := &liveHosts[i]
				if host.ID == job.Decision.HostID && host.Online {
					live = true
				}
			}
			if !live {
				job.State = NeedsAttention
				job.Problem = "Selected host disconnected; work may continue within its saved limits"
				if err := e.update(ctx, job, job.Problem); err != nil {
					return err
				}
			}
		}
		if job.State == CancelRequested && time.Since(e.lastCancel[job.AttemptID]) >= 3*time.Second {
			if err := e.dispatcher.Send(ctx, job.Decision.HostID, commandFor(job, CommandCancel)); err == nil {
				e.lastCancel[job.AttemptID] = time.Now()
			}
		}
		if job.AttemptID != "" && !job.Deadline.IsZero() && time.Now().After(job.Deadline) && job.State != CancelRequested {
			job.State = CancelRequested
			if err := e.update(ctx, job, "Execution deadline reached; cancellation awaits worker confirmation"); err != nil {
				return err
			}
			_ = e.dispatcher.Send(ctx, job.Decision.HostID, commandFor(job, CommandCancel))
		}
		if job.State != Queued || active >= e.options.Concurrency || busyHosts[job.Decision.HostID] || busyThreads[job.ThreadID] {
			continue
		}
		profile, err := e.validateDecision(ctx, job.Request.Principal, &job.Decision)
		if err != nil {
			job.State = Failed
			job.Problem = "Project authorization or policy changed"
			if err := e.update(ctx, job, job.Problem); err != nil {
				return err
			}
			continue
		}
		online := false
		liveHosts := e.dispatcher.Hosts()
		for i := range liveHosts {
			host := &liveHosts[i]
			if host.ID == job.Decision.HostID && host.Online && slices.Contains(host.ProjectIDs, job.Decision.ProjectID) {
				for _, pair := range host.Models {
					if pair == profile.ModelPair {
						online = true
					}
				}
			}
		}
		if !online {
			continue
		}
		// Revocation takes effect immediately; accepted authority never expands.
		profile.MaxSeconds = min(profile.MaxSeconds, job.Profile.MaxSeconds)
		profile.MaxTokens = min(profile.MaxTokens, job.Profile.MaxTokens)
		if job.Profile.Permission == PermissionReadOnly {
			profile.Permission = PermissionReadOnly
		}
		job.Profile = profile
		job.AttemptID = rand.Text()
		job.Deadline = time.Now().UTC().Add(time.Duration(profile.MaxSeconds) * time.Second)
		job.State = Dispatching
		if err := e.update(ctx, job, "Dispatch attempt saved before transmission"); err != nil {
			return err
		}
		if err := e.dispatcher.Send(ctx, job.Decision.HostID, commandFor(job, CommandExecute)); err != nil {
			job.State = NeedsAttention
			job.Problem = "Dispatch acknowledgement is uncertain; worker reconciliation is required"
			if err := e.update(ctx, job, job.Problem); err != nil {
				return err
			}
		}
		busyHosts[job.Decision.HostID] = true
		if job.ThreadID != "" {
			busyThreads[job.ThreadID] = true
		}
		active++
	}
	return nil
}

func (e *Engine) route(ctx context.Context, job *Job) error {
	e.mu.Lock()
	policy := e.options
	if job.PolicySnapshot != nil {
		policy = *job.PolicySnapshot
	}
	resolver := &Engine{options: policy, store: e.store, dispatcher: e.dispatcher}
	decision, err := resolver.explicitDecision(ctx, &job.Request)
	catalog, catalogErr := resolver.catalog(ctx, job.Request.Principal)
	e.mu.Unlock()
	if err == nil {
		err = catalogErr
	}
	var proposal *Decision
	var usage Usage
	explicit := job.Request.Action != "" || job.Request.ProjectID != "" || job.Request.ConversationID != "" || decision.Reason == reasonRegistered
	if err == nil && (job.Request.Mode == ModeShadow || (job.Request.Mode == ModeAutomatic && !explicit)) && e.router != nil {
		routeCtx, cancel := context.WithTimeout(ctx, time.Duration(policy.RoutingSeconds)*time.Second)
		started := time.Now()
		candidate, routingUsage, routeErr := e.router.Propose(routeCtx, job.Request.Prompt, catalog)
		cancel()
		usage = routingUsage
		usage.RoutingMillis = time.Since(started).Milliseconds()
		if routeErr == nil {
			e.mu.Lock()
			if job.Request.Mode == ModeAutomatic {
				candidate, routeErr = resolver.proposalDecision(ctx, &job.Request, &candidate)
			}
			if routeErr == nil {
				_, routeErr = resolver.validateDecision(ctx, job.Request.Principal, &candidate)
			}
			e.mu.Unlock()
		}
		if routeErr == nil {
			proposal = &candidate
		}
		if job.Request.Mode == ModeAutomatic {
			if routeErr != nil {
				err = routeErr
			} else {
				decision = candidate
			}
		}
	}
	// Ordinary answers run without holding the scheduler's mutex, so cancellation
	// and status remain responsive while the independent provider is waiting.
	var answer string
	var answerErr error
	if err == nil && decision.Action == ActionAnswer {
		if e.answerer == nil {
			answerErr = ErrUnavailable
		} else {
			answerCtx, cancel := context.WithTimeout(ctx, time.Duration(policy.RoutingSeconds)*time.Second)
			answer, answerErr = e.answerer.Answer(answerCtx, job.Request.Prompt)
			cancel()
			if len(answer) > 32768 {
				answer = answer[:32768]
			}
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	current, loadErr := e.store.Get(ctx, job.ID)
	if loadErr != nil {
		return loadErr
	}
	if current.State != Routing {
		return nil
	}
	current.Shadow, current.Usage = proposal, usage
	if err != nil {
		current.State = Failed
		current.Problem = "The routing destination could not be validated"
		return e.update(ctx, current, current.Problem)
	}
	profile, err := resolver.validateDecision(ctx, current.Request.Principal, &decision)
	if err == nil {
		_, err = e.validateDecision(ctx, current.Request.Principal, &decision)
	}
	if err != nil {
		current.State = Failed
		current.Problem = "Routing selection is not authorized"
		return e.update(ctx, current, current.Problem)
	}
	current.Decision, current.Profile = decision, profile
	switch decision.Action {
	case ActionClarify:
		current.State = AwaitingInput
	case ActionAnswer:
		if answerErr != nil {
			current.State = Failed
			current.Problem = "Controller answer failed"
		} else {
			current.State = Completed
			current.Result = answer
		}
	case ActionStatus, ActionCancel:
		target, err := e.getOwned(ctx, current.Request.Principal, decision.TargetJobID)
		if err != nil {
			return err
		}
		if decision.Action == ActionCancel {
			target, err = e.cancelLocked(ctx, target)
			if err != nil {
				return err
			}
		}
		current.State = Completed
		current.Result = "Task " + target.ID + ": " + target.State
	default:
		current.State = Queued
		if decision.Action == ActionContinue {
			conversation, err := e.store.Conversation(ctx, decision.ConversationID)
			if err != nil {
				return err
			}
			current.ConversationID, current.ThreadID = conversation.ID, conversation.ThreadID
		} else {
			current.ConversationID = rand.Text()
		}
	}
	return e.update(ctx, current, "Routing decision recorded")
}

// WorkerUpdate accepts only ordered facts from the exact host and saved attempt.
func (e *Engine) WorkerUpdate(ctx context.Context, hostID string, event WorkerEvent) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, err := e.store.Get(ctx, event.JobID)
	if err != nil {
		return 0, err
	}
	if job.AttemptID == "" || job.AttemptID != event.AttemptID || job.Decision.HostID != hostID {
		return 0, ErrForbidden
	}
	if event.Sequence <= job.WorkerSequence {
		return job.WorkerSequence, nil
	}
	if event.Sequence != job.WorkerSequence+1 {
		return job.WorkerSequence, ErrConflict
	}
	if len(event.Text) > 131072 {
		return job.WorkerSequence, ErrInvalid
	}
	if event.Tokens < 0 || len(event.ThreadID) > 256 || len(event.TurnID) > 256 {
		return job.WorkerSequence, ErrInvalid
	}
	if (job.ThreadID != "" && event.ThreadID != "" && job.ThreadID != event.ThreadID) || (job.TurnID != "" && event.TurnID != "" && job.TurnID != event.TurnID) {
		return job.WorkerSequence, ErrConflict
	}
	if Terminal(job.State) {
		return job.WorkerSequence, ErrConflict
	}
	job.WorkerSequence = event.Sequence
	if event.ThreadID != "" {
		job.ThreadID = event.ThreadID
	}
	if event.TurnID != "" {
		job.TurnID = event.TurnID
	}
	job.Usage.ExecutionTokens = max(job.Usage.ExecutionTokens, event.Tokens)
	switch event.State {
	case Running:
		if job.State != CancelRequested {
			job.State = Running
		}
		job.Approval = nil
	case AwaitingApproval:
		if event.Approval == nil || event.Approval.ID == "" || len(event.Approval.ID) > 64 || len(event.Approval.Method) > 128 || len(event.Approval.Description) > 2048 || event.Approval.ExpiresAt.After(job.Deadline) {
			return 0, ErrInvalid
		}
		job.Approval = event.Approval
		if job.State != CancelRequested {
			job.State = AwaitingApproval
		}
	case Completed:
		job.State = Completed
		job.Result = event.Text
		job.Problem = ""
		job.Approval = nil
	case Canceled:
		job.State = Canceled
		job.Approval = nil
	case Failed, NeedsAttention:
		job.State = event.State
		job.Problem = event.Text
		job.Approval = nil
	case Progress:
	default:
		return 0, ErrInvalid
	}
	if err := e.update(ctx, job, event.Text); err != nil {
		return 0, err
	}
	if Terminal(job.State) {
		delete(e.lastCancel, job.AttemptID)
	}
	if job.Usage.ExecutionTokens > job.Profile.MaxTokens && !Terminal(job.State) {
		job.State = CancelRequested
		if err := e.update(ctx, job, "Observed execution token limit reached"); err != nil {
			return 0, err
		}
		_ = e.dispatcher.Send(ctx, hostID, commandFor(job, CommandCancel))
	}
	return job.WorkerSequence, nil
}

// ConfirmReceipt allows retention only after the worker has durably dropped its
// acknowledged result spool. A lost acknowledgement can therefore still replay.
func (e *Engine) ConfirmReceipt(ctx context.Context, hostID, attemptID string, sequence int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, err := e.store.Attempt(ctx, attemptID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if job.Decision.HostID != hostID || sequence != job.WorkerSequence {
		return ErrForbidden
	}
	if !Terminal(job.State) || job.WorkerAcknowledged {
		return nil
	}
	job.WorkerAcknowledged = true
	return e.update(ctx, job, "Worker confirmed durable result receipt")
}

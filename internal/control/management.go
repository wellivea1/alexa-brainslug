package control

import (
	"context"
	"crypto/rand"
	"encoding/json"
)

// Policy returns a defensive snapshot for the authenticated owner registry editor.
func (e *Engine) Policy() (*Options, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	data, err := json.Marshal(&e.options)
	if err != nil {
		return nil, err
	}
	var snapshot Options
	err = json.Unmarshal(data, &snapshot)
	return &snapshot, err
}

// ReplacePolicy persists a complete validated registry before changing live authority.
// Existing jobs retain their accepted policy snapshot and are reauthorized at dispatch.
func (e *Engine) ReplacePolicy(next *Options, persist func() error) error {
	if next == nil || persist == nil {
		return ErrInvalid
	}
	if next.AutomaticEnabled && e.router == nil {
		return ErrUnavailable
	}
	if err := next.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	var snapshot Options
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := persist(); err != nil {
		return err
	}
	snapshot.Secrets = e.options.Secrets
	e.options = snapshot
	return nil
}

// ImportConversation records an explicitly owner-approved thread. The worker
// verifies its root, archive state, and idle turn before any continuation starts.
func (e *Engine) ImportConversation(ctx context.Context, conversation *Conversation) (*Conversation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if conversation == nil || !conversation.Principal.Valid() || conversation.ThreadID == "" || len(conversation.ThreadID) > 256 || conversation.Archived {
		return nil, ErrInvalid
	}
	decision, err := e.explicitDecision(ctx, &Request{Principal: conversation.Principal, Action: ActionNew, HostID: conversation.HostID, ProjectID: conversation.ProjectID, Profile: conversation.Profile, Model: conversation.Model, Effort: conversation.Effort})
	if err != nil {
		return nil, err
	}
	if _, err := e.validateDecision(ctx, conversation.Principal, &decision); err != nil {
		return nil, err
	}
	snapshot := *conversation
	snapshot.ID = rand.Text()
	snapshot.HostID, snapshot.ProjectID, snapshot.Profile = decision.HostID, decision.ProjectID, decision.Profile
	snapshot.Model, snapshot.Effort = decision.Model, decision.Effort
	if len(snapshot.Title) > 256 {
		return nil, ErrInvalid
	}
	if err := e.store.SaveConversation(ctx, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// Resolve asks the exact worker to release an uncertain attempt only after the
// owner acknowledges that stopped work may already have changed project files.
func (e *Engine) Resolve(ctx context.Context, owner Principal, id string) (*Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, err := e.getOwned(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	if job.State != NeedsAttention || job.AttemptID == "" {
		return nil, ErrConflict
	}
	if err := e.dispatcher.Send(ctx, job.Decision.HostID, commandFor(job, CommandResolve)); err != nil {
		return nil, err
	}
	return job, nil
}

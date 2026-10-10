package control

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Options is explicit controller policy, separate from gateway inference settings.
type Options struct {
	PolicyVersion            string    `json:"policy_version"`
	QueueLimit               int       `json:"queue_limit"`
	Concurrency              int       `json:"concurrency"`
	RoutingSeconds           int       `json:"routing_seconds"`
	RetentionDays            int       `json:"retention_days"`
	AutomaticEnabled         bool      `json:"automatic_enabled"`
	RouterEvaluationApproved bool      `json:"router_evaluation_approved"`
	RoutingAccuracyTarget    float64   `json:"routing_accuracy_target"`
	DefaultProject           string    `json:"default_project"`
	Hosts                    []Host    `json:"hosts"`
	Projects                 []Project `json:"projects"`
	Secrets                  []string  `json:"-"`
}

const reasonRegistered = "Registered destination"

func hasPhrase(text, phrase string) bool {
	text = strings.NewReplacer(".", " ", ",", " ", "?", " ", "!", " ", ";", " ").Replace(strings.ToLower(text))
	return strings.Contains(" "+strings.Join(strings.Fields(text), " ")+" ", " "+strings.ToLower(phrase)+" ")
}

// Validate rejects incomplete policy before listeners or workers start.
func (o *Options) Validate() error {
	encoded, err := json.Marshal(o)
	if err != nil || len(encoded) > 65536 || len(o.Hosts) > 32 || len(o.Projects) > 64 {
		return ErrInvalid
	}
	if o.AutomaticEnabled && (!o.RouterEvaluationApproved || o.RoutingAccuracyTarget < 0.8 || o.RoutingAccuracyTarget > 1) {
		return fmt.Errorf("%w: automatic routing requires an owner-approved evaluation and accuracy target", ErrInvalid)
	}
	if o.PolicyVersion == "" || o.QueueLimit < 1 || o.QueueLimit > 128 || o.Concurrency < 1 || o.Concurrency > 16 || o.RoutingSeconds < 1 || o.RoutingSeconds > 30 || o.RetentionDays < 1 || o.RetentionDays > 365 {
		return fmt.Errorf("%w: policy version and bounded queue, concurrency, routing, and retention are required", ErrInvalid)
	}
	ids := make(map[string]bool)
	for i := range o.Hosts {
		host := &o.Hosts[i]
		host.ProjectIDs = nil
		if host.ID == "" || len(host.ID) > 64 || ids[host.ID] || len(host.Models) == 0 || len(host.Models) > 64 || host.CodexVersion == "" {
			return fmt.Errorf("%w: host IDs must be unique with explicit model pairs", ErrInvalid)
		}
		ids[host.ID] = true
		for _, pair := range host.Models {
			if pair.Model == "" || pair.Effort == "" {
				return fmt.Errorf("%w: model and effort must be explicit", ErrInvalid)
			}
		}
	}
	projects := make(map[string]bool)
	for i := range o.Projects {
		project := &o.Projects[i]
		if project.ID == "" || len(project.ID) > 64 || len(project.Description) > 1024 || len(project.Profiles) > 8 || projects[project.ID] || !ids[project.HostID] || len(project.Principals) == 0 || len(project.Principals) > 128 {
			return fmt.Errorf("%w: project requires a unique ID, enrolled host, and principals", ErrInvalid)
		}
		projects[project.ID] = true
		for j := range o.Hosts {
			if o.Hosts[j].ID == project.HostID && !slices.Contains(o.Hosts[j].ProjectIDs, project.ID) {
				o.Hosts[j].ProjectIDs = append(o.Hosts[j].ProjectIDs, project.ID)
			}
		}
		if _, ok := project.Profiles[project.DefaultProfile]; !ok {
			return fmt.Errorf("%w: default project profile is missing", ErrInvalid)
		}
		for _, profile := range project.Profiles {
			if !o.hostAllows(project.HostID, profile.ModelPair) || profile.MaxSeconds < 1 || profile.MaxSeconds > 3600 || profile.MaxTokens < 1 || profile.MaxTokens > 1000000 ||
				(profile.Permission != PermissionReadOnly && profile.Permission != PermissionWorkspaceWrite) {
				return fmt.Errorf("%w: project profile exceeds host or execution policy", ErrInvalid)
			}
		}
	}
	if o.DefaultProject != "" && !projects[o.DefaultProject] {
		return fmt.Errorf("%w: default project is not registered", ErrInvalid)
	}
	return nil
}

func (o *Options) hostAllows(id string, pair ModelPair) bool {
	for i := range o.Hosts {
		host := &o.Hosts[i]
		if host.ID == id {
			for _, allowed := range host.Models {
				if allowed == pair {
					return true
				}
			}
		}
	}
	return false
}

func (e *Engine) project(id string) *Project {
	for i := range e.options.Projects {
		if e.options.Projects[i].ID == id {
			return &e.options.Projects[i]
		}
	}
	return nil
}

func permits(project *Project, owner Principal) bool {
	if project == nil || !owner.Valid() {
		return false
	}
	for _, key := range project.Principals {
		if key == owner.Key() {
			return true
		}
	}
	return false
}

func (e *Engine) authorized(owner Principal) bool {
	for i := range e.options.Projects {
		if permits(&e.options.Projects[i], owner) {
			return true
		}
	}
	return false
}

func (e *Engine) explicitDecision(ctx context.Context, request *Request) (Decision, error) {
	decision := Decision{Action: request.Action, HostID: request.HostID, ProjectID: request.ProjectID, ConversationID: request.ConversationID, Profile: request.Profile, Model: request.Model, Effort: request.Effort, Title: request.Title, TargetJobID: request.TargetJobID, Reason: "Explicit owner selection"}
	if decision.Action == "" {
		decision.Action = ActionNew
	}
	if decision.Action == ActionAnswer || decision.Action == ActionStatus || decision.Action == ActionCancel {
		return decision, nil
	}
	prompt := strings.ToLower(request.Prompt)
	if decision.HostID == "" {
		for i := range e.options.Hosts {
			host := &e.options.Hosts[i]
			for _, name := range []string{host.ID, host.Name} {
				if name != "" && (hasPhrase(prompt, "on "+name) || hasPhrase(prompt, "use my "+name)) {
					if decision.HostID != "" && decision.HostID != host.ID {
						return Decision{Action: ActionClarify, Clarification: "Which registered machine should I use?", Reason: reasonRegistered}, nil
					}
					decision.HostID = host.ID
					decision.Reason = reasonRegistered
				}
			}
		}
	}
	if decision.Action == ActionContinue {
		if request.ConversationID == "" {
			return Decision{Action: ActionClarify, Clarification: "Which Codex conversation should I continue?"}, nil
		}
		conversation, err := e.store.Conversation(ctx, request.ConversationID)
		if err != nil {
			return Decision{}, err
		}
		if conversation.Principal != request.Principal || conversation.Archived {
			return Decision{}, ErrForbidden
		}
		if (decision.HostID != "" && decision.HostID != conversation.HostID) || (decision.ProjectID != "" && decision.ProjectID != conversation.ProjectID) {
			return Decision{}, fmt.Errorf("%w: continuation is pinned to its saved host and project", ErrConflict)
		}
		decision.HostID, decision.ProjectID = conversation.HostID, conversation.ProjectID
		if decision.Profile == "" {
			decision.Profile = conversation.Profile
		}
		if decision.Model == "" && request.Profile == "" {
			decision.Model = conversation.Model
		}
		if decision.Effort == "" && request.Profile == "" {
			decision.Effort = conversation.Effort
		}
	}
	if decision.ProjectID == "" {
		for i := range e.options.Projects {
			project := &e.options.Projects[i]
			if !permits(project, request.Principal) || (decision.HostID != "" && project.HostID != decision.HostID) {
				continue
			}
			matched := false
			for _, alias := range append([]string{project.ID}, project.Aliases...) {
				if hasPhrase(request.Prompt, "in "+alias) {
					matched = true
				}
			}
			if matched {
				if decision.ProjectID != "" && decision.ProjectID != project.ID {
					return Decision{Action: ActionClarify, Clarification: "Which project should I use?", Reason: reasonRegistered}, nil
				}
				decision.ProjectID = project.ID
				decision.Reason = reasonRegistered
			}
		}
		if decision.ProjectID == "" {
			decision.ProjectID = e.options.DefaultProject
			if !permits(e.project(decision.ProjectID), request.Principal) {
				decision.ProjectID = ""
			}
		}
		if decision.ProjectID == "" {
			return Decision{Action: ActionClarify, Clarification: "Which registered project should I use?"}, nil
		}
	}
	project := e.project(decision.ProjectID)
	if !permits(project, request.Principal) {
		return Decision{}, ErrForbidden
	}
	if decision.HostID == "" {
		decision.HostID = project.HostID
	}
	if request.Profile == "" {
		for name := range project.Profiles {
			if hasPhrase(prompt, "using profile "+name) || hasPhrase(prompt, "using "+name+" reasoning") {
				decision.Profile = name
				if request.Model == "" {
					decision.Model = project.Profiles[name].Model
				}
				if request.Effort == "" {
					decision.Effort = project.Profiles[name].Effort
				}
			}
		}
	}
	if decision.Profile == "" {
		decision.Profile = project.DefaultProfile
	}
	profile, ok := project.Profiles[decision.Profile]
	if !ok {
		return Decision{}, fmt.Errorf("%w: profile is not allowed for this project", ErrForbidden)
	}
	if decision.Model == "" {
		decision.Model = profile.Model
	}
	if decision.Effort == "" {
		decision.Effort = profile.Effort
	}
	if decision.Title == "" {
		decision.Title = strings.TrimSpace(request.Prompt)
		if len(decision.Title) > 100 {
			decision.Title = decision.Title[:100]
		}
	}
	return decision, nil
}

func (e *Engine) validateDecision(ctx context.Context, owner Principal, decision *Decision) (Profile, error) {
	if len(decision.Title) > 256 || len(decision.Reason) > 2048 || len(decision.HostID) > 64 || len(decision.ProjectID) > 64 || len(decision.Model) > 128 || len(decision.Effort) > 32 || len(decision.Profile) > 64 || len(decision.ConversationID) > 256 || len(decision.TargetJobID) > 256 {
		return Profile{}, ErrInvalid
	}
	switch decision.Action {
	case ActionClarify:
		if strings.TrimSpace(decision.Clarification) == "" || len(decision.Clarification) > 512 {
			return Profile{}, ErrInvalid
		}
		return Profile{}, nil
	case ActionAnswer:
		if decision.HostID != "" || decision.ProjectID != "" || decision.ConversationID != "" || decision.Model != "" || decision.Effort != "" || decision.Profile != "" || decision.TargetJobID != "" {
			return Profile{}, ErrInvalid
		}
		return Profile{}, nil
	case ActionStatus, ActionCancel:
		if decision.TargetJobID == "" || decision.HostID != "" || decision.ProjectID != "" || decision.ConversationID != "" {
			return Profile{}, ErrInvalid
		}
		_, err := e.getOwned(ctx, owner, decision.TargetJobID)
		return Profile{}, err
	case ActionNew, ActionContinue:
	default:
		return Profile{}, ErrInvalid
	}
	project := e.project(decision.ProjectID)
	if !permits(project, owner) || decision.HostID != project.HostID {
		return Profile{}, ErrForbidden
	}
	profile, ok := project.Profiles[decision.Profile]
	if !ok {
		return Profile{}, ErrForbidden
	}
	chosen := ModelPair{Model: decision.Model, Effort: decision.Effort}
	if !e.options.hostAllows(project.HostID, chosen) {
		return Profile{}, ErrForbidden
	}
	if project.LockProfile && chosen != profile.ModelPair {
		return Profile{}, fmt.Errorf("%w: project profile is locked", ErrConflict)
	}
	if chosen != profile.ModelPair {
		allowed := false
		for _, other := range project.Profiles {
			if other.ModelPair != chosen {
				continue
			}
			allowed = true
			profile.MaxSeconds = min(profile.MaxSeconds, other.MaxSeconds)
			profile.MaxTokens = min(profile.MaxTokens, other.MaxTokens)
			if other.Permission == PermissionReadOnly {
				profile.Permission = PermissionReadOnly
			}
		}
		if !allowed {
			return Profile{}, ErrForbidden
		}
	}
	profile.ModelPair = chosen
	if decision.Action == ActionContinue {
		conversation, err := e.store.Conversation(ctx, decision.ConversationID)
		if err != nil {
			return Profile{}, err
		}
		if conversation.Principal != owner || conversation.Archived || conversation.HostID != decision.HostID || conversation.ProjectID != decision.ProjectID {
			return Profile{}, ErrForbidden
		}
	} else if decision.ConversationID != "" {
		return Profile{}, ErrInvalid
	}
	return profile, nil
}

// proposalDecision applies explicit overrides and pins continuations before validation.
func (e *Engine) proposalDecision(ctx context.Context, request *Request, candidate *Decision) (Decision, error) {
	if candidate.Action == ActionClarify && request.ProjectID == "" && request.ConversationID == "" {
		return *candidate, nil
	}
	selected := *request
	if selected.Action == "" {
		selected.Action = candidate.Action
	}
	if selected.HostID == "" {
		selected.HostID = candidate.HostID
	}
	if selected.ProjectID == "" {
		selected.ProjectID = candidate.ProjectID
	}
	if selected.ConversationID == "" {
		selected.ConversationID = candidate.ConversationID
	}
	if selected.TargetJobID == "" {
		selected.TargetJobID = candidate.TargetJobID
	}
	// An existing conversation's profile wins over an automatic proposal.
	if selected.Action != ActionContinue {
		if selected.Profile == "" {
			selected.Profile = candidate.Profile
		}
		if selected.Model == "" {
			selected.Model = candidate.Model
		}
		if selected.Effort == "" {
			selected.Effort = candidate.Effort
		}
	}
	if selected.Title == "" {
		selected.Title = candidate.Title
	}
	decision, err := e.explicitDecision(ctx, &selected)
	decision.Reason = candidate.Reason
	return decision, err
}

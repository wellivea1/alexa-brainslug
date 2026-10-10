// Package control owns durable Codex tasks, routing policy, and authorization.
// It contains no Alexa, HTTP, WebSocket, or Codex protocol types.
package control

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ProtocolVersion is the version of the typed controller/worker contract.
const ProtocolVersion = 1

// Execution permissions, progress facts, and scoped operations are shared by the ports.
const (
	PermissionReadOnly       = "read-only"
	PermissionWorkspaceWrite = "workspace-write"
	Progress                 = "progress"
	CommandExecute           = "execute"
	CommandCancel            = "cancel"
	CommandApprove           = "approve"
	CommandResolve           = "resolve"
)

// Stable task failures are presented by inbound adapters without private diagnostics.
var (
	ErrInvalid     = errors.New("invalid task request")
	ErrForbidden   = errors.New("task access denied")
	ErrNotFound    = errors.New("task not found")
	ErrConflict    = errors.New("task request conflicts with saved state")
	ErrQueueFull   = errors.New("task queue is full")
	ErrUnavailable = errors.New("task controller or worker is unavailable")
)

// Principal scopes every task, result, conversation, and mutation.
type Principal struct {
	Namespace string `json:"namespace"`
	Subject   string `json:"subject"`
}

// Valid reports whether ownership can be enforced.
func (p Principal) Valid() bool {
	return p.Namespace != "" && p.Subject != "" && !strings.Contains(p.Namespace, ":") &&
		p.Namespace == strings.TrimSpace(p.Namespace) && p.Subject == strings.TrimSpace(p.Subject) &&
		len(p.Namespace) <= 64 && len(p.Subject) <= 512
}

// Key is a stable catalog grant key; subjects may themselves contain colons.
func (p Principal) Key() string { return p.Namespace + ":" + p.Subject }

// Task states describe confirmed facts, including uncertain execution.
const (
	Accepted         = "accepted"
	Routing          = "routing"
	Queued           = "queued"
	Dispatching      = "dispatching"
	Running          = "running"
	AwaitingInput    = "awaiting_input"
	AwaitingApproval = "awaiting_approval"
	CancelRequested  = "cancel_requested"
	NeedsAttention   = "needs_attention"
	Completed        = "completed"
	Failed           = "failed"
	Canceled         = "canceled"
)

// Terminal reports states that no longer own worker capacity.
func Terminal(state string) bool { return state == Completed || state == Failed || state == Canceled }

// Routing actions are validated before any execution destination is resolved.
const (
	ActionNew      = "codex_new"
	ActionContinue = "codex_continue"
	ActionAnswer   = "answer"
	ActionClarify  = "clarify"
	ActionStatus   = "status"
	ActionCancel   = "cancel"
	ModeManual     = "manual"
	ModeShadow     = "shadow"
	ModeAutomatic  = "automatic"
)

// ModelPair is one explicitly allowed model and reasoning-effort combination.
type ModelPair struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// Profile combines an execution choice with bounded worker permissions.
type Profile struct {
	ModelPair
	Permission string `json:"permission"`
	MaxSeconds int    `json:"max_seconds"`
	MaxTokens  int64  `json:"max_tokens"`
}

// Project is owner-registered policy. Local filesystem roots stay on workers.
type Project struct {
	ID             string             `json:"id"`
	Name           string             `json:"name"`
	HostID         string             `json:"host_id"`
	Aliases        []string           `json:"aliases"`
	Description    string             `json:"description"`
	Principals     []string           `json:"principals"`
	Profiles       map[string]Profile `json:"profiles"`
	DefaultProfile string             `json:"default_profile"`
	LockProfile    bool               `json:"lock_profile"`
}

// Host declares owner-approved model pairs and its last advertised availability.
type Host struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Models       []ModelPair `json:"models"`
	ProjectIDs   []string    `json:"project_ids"`
	Online       bool        `json:"online"`
	LastContact  time.Time   `json:"last_contact"`
	CodexVersion string      `json:"codex_version"`
}

// Catalog is the authorized routing and dashboard view.
type Catalog struct {
	Hosts         []Host         `json:"hosts"`
	Projects      []Project      `json:"projects"`
	Conversations []Conversation `json:"conversations"`
	Tasks         []TaskSummary  `json:"tasks"`
}

// TaskSummary provides authorized IDs and short titles for status/cancel routing.
type TaskSummary struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	State          string `json:"state"`
	ProjectID      string `json:"project_id"`
	ConversationID string `json:"conversation_id"`
}

// Request records explicit ingress intent. A verified request ID provides idempotency.
type Request struct {
	Principal      Principal `json:"principal"`
	Source         string    `json:"source"`
	RequestID      string    `json:"request_id"`
	Prompt         string    `json:"prompt"`
	Mode           string    `json:"mode"`
	Action         string    `json:"action"`
	HostID         string    `json:"host_id"`
	ProjectID      string    `json:"project_id"`
	ConversationID string    `json:"conversation_id"`
	Profile        string    `json:"profile"`
	Model          string    `json:"model"`
	Effort         string    `json:"effort"`
	Title          string    `json:"title"`
	ReplyTo        string    `json:"reply_to,omitempty"`
	TargetJobID    string    `json:"target_job_id,omitempty"`
}

// Decision is a tool-free router proposal, never execution authority.
type Decision struct {
	Action         string `json:"action"`
	HostID         string `json:"host_id"`
	ProjectID      string `json:"project_id"`
	ConversationID string `json:"conversation_id"`
	Profile        string `json:"profile"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	Title          string `json:"title"`
	Reason         string `json:"reason"`
	Clarification  string `json:"clarification"`
	TargetJobID    string `json:"target_job_id"`
}

// Conversation pins a Brainslug-managed thread to one owner, host, and project.
type Conversation struct {
	ID        string    `json:"id"`
	Principal Principal `json:"principal"`
	HostID    string    `json:"host_id"`
	ProjectID string    `json:"project_id"`
	ThreadID  string    `json:"thread_id"`
	Profile   string    `json:"profile"`
	ModelPair
	Title    string `json:"title"`
	Archived bool   `json:"archived"`
}

// Approval binds one decision to one pending host, job, attempt, thread, and turn.
type Approval struct {
	ID          string    `json:"id"`
	Method      string    `json:"method"`
	Description string    `json:"description"`
	ExpiresAt   time.Time `json:"expires_at"`
	CanAccept   bool      `json:"can_accept"`
}

// Usage separates routing costs from observed execution usage.
type Usage struct {
	RoutingTokens   int64 `json:"routing_tokens"`
	ExecutionTokens int64 `json:"execution_tokens"`
	RoutingMillis   int64 `json:"routing_millis"`
}

// Job is the controller's authoritative operational record.
type Job struct {
	ID                 string    `json:"id"`
	Request            Request   `json:"request"`
	State              string    `json:"state"`
	Revision           int64     `json:"revision"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	Deadline           time.Time `json:"deadline"`
	Decision           Decision  `json:"decision"`
	Shadow             *Decision `json:"shadow,omitempty"`
	PolicyVersion      string    `json:"policy_version"`
	PolicySnapshot     *Options  `json:"policy_snapshot,omitempty"`
	Profile            Profile   `json:"execution_profile"`
	ConversationID     string    `json:"conversation_id"`
	ThreadID           string    `json:"thread_id"`
	TurnID             string    `json:"turn_id"`
	AttemptID          string    `json:"attempt_id"`
	WorkerSequence     int64     `json:"worker_sequence"`
	WorkerAcknowledged bool      `json:"worker_acknowledged"`
	Result             string    `json:"result"`
	Problem            string    `json:"problem"`
	Approval           *Approval `json:"approval,omitempty"`
	Usage              Usage     `json:"usage"`
}

// Event is a bounded, resumable history item.
type Event struct {
	Sequence  int64     `json:"sequence"`
	JobID     string    `json:"job_id"`
	State     string    `json:"state"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// Store owns durable acceptance, compare-and-swap updates, and ownership queries.
type Store interface {
	Accept(context.Context, *Request, *Options) (*Job, bool, error)
	Get(context.Context, string) (*Job, error)
	Attempt(context.Context, string) (*Job, error)
	List(context.Context, *Principal, int) ([]*Job, error)
	Active(context.Context) ([]*Job, error)
	Update(context.Context, *Job, int64, string) error
	Events(context.Context, string, int64, int) ([]Event, error)
	Conversations(context.Context, Principal) ([]Conversation, error)
	Conversation(context.Context, string) (*Conversation, error)
	SaveConversation(context.Context, *Conversation) error
	Recover(context.Context) error
	Purge(context.Context, time.Time) (int64, error)
}

// Router proposes a destination using only authorized catalog data.
type Router interface {
	Propose(context.Context, string, Catalog) (Decision, Usage, error)
}

// Answerer supplies the controller's independently configured ordinary-answer path.
type Answerer interface {
	Answer(context.Context, string) (string, error)
}

// Command is a scoped worker operation, with no arbitrary path or shell command.
type Command struct {
	Kind             string    `json:"kind"`
	JobID            string    `json:"job_id"`
	AttemptID        string    `json:"attempt_id"`
	ProjectID        string    `json:"project_id"`
	ThreadID         string    `json:"thread_id"`
	TurnID           string    `json:"turn_id"`
	Prompt           string    `json:"prompt"`
	Profile          Profile   `json:"profile"`
	Deadline         time.Time `json:"deadline"`
	ApprovalID       string    `json:"approval_id"`
	ApprovalDecision string    `json:"approval_decision"`
}

// WorkerEvent reports a durable attempt fact that can be replayed after reconnect.
type WorkerEvent struct {
	JobID     string    `json:"job_id"`
	AttemptID string    `json:"attempt_id"`
	Sequence  int64     `json:"sequence"`
	State     string    `json:"state"`
	ThreadID  string    `json:"thread_id"`
	TurnID    string    `json:"turn_id"`
	Text      string    `json:"text"`
	Approval  *Approval `json:"approval,omitempty"`
	Tokens    int64     `json:"tokens"`
}

// Dispatcher provides the controller's narrow worker-transport boundary.
type Dispatcher interface {
	Send(context.Context, string, Command) error
	Hosts() []Host
}

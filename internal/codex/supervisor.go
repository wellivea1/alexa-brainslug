package codex

import (
	"context"
	"sync"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/control"
)

// Supervisor owns process restart. Restart creates no thread or turn by itself.
type Supervisor struct {
	mu      sync.Mutex
	ctx     context.Context
	options ProcessOptions
	current *AppServer
}

// NewSupervisor starts the pinned runtime under the worker's process lifetime.
func NewSupervisor(ctx context.Context, options ProcessOptions) (*Supervisor, error) {
	s := &Supervisor{ctx: ctx, options: options}
	_, err := s.runtime()
	return s, err
}

func (s *Supervisor) runtime() (*AppServer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		select {
		case <-s.current.process.Done():
			_ = s.current.process.Close()
		default:
			return s.current, nil
		}
	}
	// Process ownership uses the lifetime context. The initialization timeout
	// belongs to the RPC, rather than terminating a successfully initialized child.
	process, err := StartProcess(s.ctx, s.options)
	if err != nil {
		return nil, err
	}
	s.current = NewAppServer(process)
	return s.current, nil
}

// Models checks host-local login and the protocol's advertised catalog.
func (s *Supervisor) Models(ctx context.Context) ([]control.ModelPair, error) {
	a, err := s.runtime()
	if err != nil {
		return nil, err
	}
	return a.Models(ctx)
}

// Execute delegates one turn; a failed turn closes its process before reconciliation.
func (s *Supervisor) Execute(ctx context.Context, command control.Command, root string, emit func(control.WorkerEvent) error) error {
	a, err := s.runtime()
	if err != nil {
		return err
	}
	return a.Execute(ctx, command, root, emit)
}

// Respond never restarts a dead process to satisfy an old approval.
func (s *Supervisor) Respond(ctx context.Context, command control.Command) error {
	s.mu.Lock()
	a := s.current
	s.mu.Unlock()
	if a == nil {
		return control.ErrUnavailable
	}
	select {
	case <-a.process.Done():
		return control.ErrUnavailable
	default:
	}
	return a.Respond(ctx, command)
}

// Reconcile may restart the process, but reads only the saved thread and turn.
func (s *Supervisor) Reconcile(ctx context.Context, record *Record) (control.WorkerEvent, error) {
	a, err := s.runtime()
	if err != nil {
		return control.WorkerEvent{}, err
	}
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return a.Reconcile(readCtx, record)
}

// Close contains and reaps the owned process tree.
func (s *Supervisor) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil
	}
	return s.current.process.Close()
}

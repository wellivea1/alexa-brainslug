// Package taskgateway manages the voice gateway's enrolled controller client.
// It has no queue; saved acceptance and all execution authority belong to the controller.
package taskgateway

import (
	"context"
	"sync"

	"github.com/wellivea1/alexa-brainslug/internal/config"
	"github.com/wellivea1/alexa-brainslug/internal/control"
	"github.com/wellivea1/alexa-brainslug/internal/controlapi"
)

// Manager swaps only the controller connection, independently from inference credentials.
type Manager struct {
	mu      sync.RWMutex
	config  config.CodexConfig
	client  *controlapi.Client
	persist func(config.CodexConfig) error
}

// New constructs an optional verified connection without submitting work.
func New(settings config.CodexConfig, persist func(config.CodexConfig) error) (*Manager, error) {
	client, err := connect(settings)
	if err != nil {
		return nil, err
	}
	return &Manager{config: settings, client: client, persist: persist}, nil
}

func connect(settings config.CodexConfig) (*controlapi.Client, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return nil, nil
	}
	tlsConfig, err := controlapi.LoadTLS(controlapi.TLSFiles{Certificate: settings.Certificate, Key: settings.Key, CA: settings.CA, ServerName: settings.ServerName}, false)
	if err != nil {
		return nil, err
	}
	return controlapi.NewClient(settings.URL, tlsConfig)
}

// Settings contains only owner-supplied connection references and mode.
func (m *Manager) Settings() config.CodexConfig { m.mu.RLock(); defer m.mu.RUnlock(); return m.config }

// Mode reports disabled when no enrolled controller is configured.
func (m *Manager) Mode() string {
	settings := m.Settings()
	if !settings.Enabled {
		return "disabled"
	}
	if settings.Mode == "" {
		return control.ModeManual
	}
	return settings.Mode
}

// Update validates certificates and persists before changing the active connection.
func (m *Manager) Update(settings config.CodexConfig) error {
	next, err := connect(settings)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.persist == nil {
		if next != nil {
			next.Close()
		}
		return control.ErrUnavailable
	}
	if err := m.persist(settings); err != nil {
		if next != nil {
			next.Close()
		}
		return err
	}
	previous := m.client
	m.config, m.client = settings, next
	if previous != nil {
		previous.Close()
	}
	return nil
}

// Client returns the internal service client for authenticated inbound adapters.
// It is never serialized or provided to a browser.
func (m *Manager) Client() (*controlapi.Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.client == nil {
		return nil, control.ErrUnavailable
	}
	return m.client, nil
}

// Close releases idle transport connections without canceling controller jobs.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != nil {
		m.client.Close()
	}
}

// Submit forwards an authorized application request using its original idempotency key.
func (m *Manager) Submit(ctx context.Context, request control.Request) (*control.Job, error) {
	c, err := m.Client()
	if err != nil {
		return nil, err
	}
	return c.Submit(ctx, request)
}

// Get reads one scoped task.
func (m *Manager) Get(ctx context.Context, owner control.Principal, id string) (*control.Job, error) {
	c, err := m.Client()
	if err != nil {
		return nil, err
	}
	return c.Get(ctx, owner, id)
}

// List reads recent scoped tasks.
func (m *Manager) List(ctx context.Context, owner control.Principal) ([]*control.Job, error) {
	c, err := m.Client()
	if err != nil {
		return nil, err
	}
	return c.List(ctx, owner)
}

// Cancel forwards exact task cancellation.
func (m *Manager) Cancel(ctx context.Context, owner control.Principal, id string) (*control.Job, error) {
	c, err := m.Client()
	if err != nil {
		return nil, err
	}
	return c.Cancel(ctx, owner, id)
}

// Reply forwards clarification with a fresh verified ingress key.
func (m *Manager) Reply(ctx context.Context, owner control.Principal, id string, request control.Request) (*control.Job, error) {
	c, err := m.Client()
	if err != nil {
		return nil, err
	}
	return c.Reply(ctx, owner, id, request)
}

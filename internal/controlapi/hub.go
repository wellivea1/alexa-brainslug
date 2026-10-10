package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wellivea1/alexa-brainslug/internal/control"
)

// Frame is the bounded, typed worker transport envelope.
type Frame struct {
	Version   int                  `json:"version"`
	Type      string               `json:"type"`
	Host      *control.Host        `json:"host,omitempty"`
	Command   *control.Command     `json:"command,omitempty"`
	Event     *control.WorkerEvent `json:"event,omitempty"`
	AttemptID string               `json:"attempt_id,omitempty"`
	Sequence  int64                `json:"sequence,omitempty"`
}

type workerSession struct {
	host       control.Host
	connection *websocket.Conn
	send       chan Frame
}

// Hub owns the connected worker sessions and refuses duplicate live ownership.
type Hub struct {
	mu            sync.Mutex
	expected      map[string]control.Host
	sessions      map[string]*workerSession
	engine        *control.Engine
	validIdentity func(Identity) bool
}

// UpdateRegistry replaces registered capabilities and closes sessions whose
// identity was revoked or whose host policy changed. Reconnection renegotiates.
func (h *Hub) UpdateRegistry(hosts []control.Host) {
	h.mu.Lock()
	h.expected = make(map[string]control.Host)
	for i := range hosts {
		h.expected[hosts[i].ID] = hosts[i]
	}
	var connections []*websocket.Conn
	for _, session := range h.sessions {
		// All sessions renegotiate changed policy, including reduced model grants.
		connections = append(connections, session.connection)
	}
	h.mu.Unlock()
	for _, connection := range connections {
		_ = connection.CloseNow()
	}
}

// SetIdentityValidator rechecks enrollment after the potentially slow handshake.
func (h *Hub) SetIdentityValidator(check func(Identity) bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.validIdentity = check
}

// NewHub builds the explicit host-enrollment boundary.
func NewHub(hosts []control.Host) *Hub {
	hub := &Hub{expected: make(map[string]control.Host), sessions: make(map[string]*workerSession)}
	for i := range hosts {
		hub.expected[hosts[i].ID] = hosts[i]
	}
	return hub
}

// AttachEngine completes process wiring before the listener starts.
func (h *Hub) AttachEngine(engine *control.Engine) { h.engine = engine }

// Close ends only LAN sessions. Already accepted worker jobs keep their own deadline.
func (h *Hub) Close() {
	h.mu.Lock()
	var connections []*websocket.Conn
	for _, session := range h.sessions {
		connections = append(connections, session.connection)
	}
	h.mu.Unlock()
	for _, connection := range connections {
		_ = connection.CloseNow()
	}
}

// Hosts returns live availability without inventing results from a stale lease.
func (h *Hub) Hosts() []control.Host {
	h.mu.Lock()
	defer h.mu.Unlock()
	hosts := make([]control.Host, 0, len(h.expected))
	for id := range h.expected {
		expected := h.expected[id]
		host := expected
		if session := h.sessions[id]; session != nil {
			host = session.host
			host.Online = time.Since(host.LastContact) < 20*time.Second
		}
		hosts = append(hosts, host)
	}
	return hosts
}

// Send queues one scoped command. An uncertain send never selects another host.
func (h *Hub) Send(ctx context.Context, hostID string, command control.Command) error {
	h.mu.Lock()
	session := h.sessions[hostID]
	alive := session != nil && time.Since(session.host.LastContact) < 20*time.Second
	h.mu.Unlock()
	if !alive {
		return control.ErrUnavailable
	}
	select {
	case session.send <- Frame{Version: control.ProtocolVersion, Type: "command", Command: &command}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return control.ErrUnavailable
	}
}

// ServeWorker establishes a mutually authenticated, version-negotiated outbound session.
func (h *Hub) ServeWorker(w http.ResponseWriter, r *http.Request, identity Identity) {
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"brainslug-worker.v1"}})
	if err != nil {
		return
	}
	defer func() { _ = connection.Close(websocket.StatusNormalClosure, "worker session ended") }()
	connection.SetReadLimit(maxMessageBytes)
	if connection.Subprotocol() != "brainslug-worker.v1" {
		_ = connection.Close(websocket.StatusPolicyViolation, "protocol required")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, 5*time.Second)
	hello, err := readFrame(handshakeCtx, connection)
	handshakeCancel()
	if err != nil || hello.Type != "hello" || hello.Host == nil || hello.Host.ID != identity.HostID {
		return
	}
	h.mu.Lock()
	expected, known := h.expected[identity.HostID]
	if !known || h.sessions[identity.HostID] != nil || expected.CodexVersion == "" || hello.Host.CodexVersion != expected.CodexVersion || (h.validIdentity != nil && !h.validIdentity(identity)) {
		h.mu.Unlock()
		return
	}
	for _, project := range hello.Host.ProjectIDs {
		allowed := false
		for _, id := range expected.ProjectIDs {
			if id == project {
				allowed = true
			}
		}
		if !allowed {
			h.mu.Unlock()
			return
		}
	}
	models := make([]control.ModelPair, 0)
	for _, pair := range hello.Host.Models {
		for _, allowed := range expected.Models {
			if pair == allowed {
				models = append(models, pair)
			}
		}
	}
	if len(models) == 0 || len(hello.Host.ProjectIDs) == 0 {
		h.mu.Unlock()
		return
	}
	host := *hello.Host
	host.Models = models
	host.Online = true
	host.LastContact = time.Now().UTC()
	session := &workerSession{host: host, connection: connection, send: make(chan Frame, 32)}
	h.sessions[host.ID] = session
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if h.sessions[host.ID] == session {
			delete(h.sessions, host.ID)
		}
		h.mu.Unlock()
	}()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case frame := <-session.send:
				writeCtx, done := context.WithTimeout(ctx, 5*time.Second)
				err := writeFrame(writeCtx, connection, frame)
				done()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-writerDone }()
	session.send <- Frame{Version: control.ProtocolVersion, Type: "ready"}
	for {
		frame, err := readFrame(ctx, connection)
		if err != nil {
			return
		}
		h.mu.Lock()
		session.host.LastContact = time.Now().UTC()
		h.mu.Unlock()
		switch frame.Type {
		case "heartbeat":
		case "receipt":
			if h.engine == nil || h.engine.ConfirmReceipt(ctx, host.ID, frame.AttemptID, frame.Sequence) != nil {
				return
			}
		case "event":
			if frame.Event == nil || h.engine == nil {
				return
			}
			sequence, err := h.engine.WorkerUpdate(ctx, host.ID, *frame.Event)
			if err != nil {
				return
			}
			select {
			case session.send <- Frame{Version: control.ProtocolVersion, Type: "ack", AttemptID: frame.Event.AttemptID, Sequence: sequence}:
			case <-ctx.Done():
				return
			}
		default:
			return
		}
	}
}

// ReadFrame decodes one bounded worker message and verifies protocol version.
func ReadFrame(ctx context.Context, connection *websocket.Conn) (Frame, error) {
	return readFrame(ctx, connection)
}

// WriteFrame sends one typed worker message.
func WriteFrame(ctx context.Context, connection *websocket.Conn, frame Frame) error {
	return writeFrame(ctx, connection, frame)
}

func readFrame(ctx context.Context, connection *websocket.Conn) (Frame, error) {
	kind, data, err := connection.Read(ctx)
	if err != nil {
		return Frame{}, err
	}
	if kind != websocket.MessageText || len(data) > maxMessageBytes {
		return Frame{}, control.ErrInvalid
	}
	var frame Frame
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&frame); err != nil {
		return Frame{}, err
	}
	if frame.Version != control.ProtocolVersion {
		return Frame{}, errors.New("incompatible worker protocol")
	}
	return frame, nil
}

func writeFrame(ctx context.Context, connection *websocket.Conn, frame Frame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if len(data) > maxMessageBytes {
		return control.ErrInvalid
	}
	return connection.Write(ctx, websocket.MessageText, data)
}

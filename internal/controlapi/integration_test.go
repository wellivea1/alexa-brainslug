package controlapi_test

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/wellivea1/alexa-brainslug/internal/codex"
	"github.com/wellivea1/alexa-brainslug/internal/control"
	"github.com/wellivea1/alexa-brainslug/internal/controlapi"
	"github.com/wellivea1/alexa-brainslug/internal/controlstore"
)

type testRuntime struct {
	pair    control.ModelPair
	host    string
	release chan struct{}
	calls   atomic.Int32
	mu      sync.Mutex
	threads []string
}

func (r *testRuntime) Models(context.Context) ([]control.ModelPair, error) {
	return []control.ModelPair{r.pair}, nil
}
func (r *testRuntime) Respond(context.Context, control.Command) error { return nil }
func (r *testRuntime) Reconcile(_ context.Context, record *codex.Record) (control.WorkerEvent, error) {
	return control.WorkerEvent{State: control.NeedsAttention, ThreadID: record.ThreadID, TurnID: record.TurnID, Text: "fake uncertain evidence"}, nil
}

func (r *testRuntime) Execute(ctx context.Context, command control.Command, _ string, emit func(control.WorkerEvent) error) error {
	r.calls.Add(1)
	thread := command.ThreadID
	if thread == "" {
		thread = r.host + "-thread"
	}
	r.mu.Lock()
	r.threads = append(r.threads, thread)
	r.mu.Unlock()
	if err := emit(control.WorkerEvent{State: "progress", ThreadID: thread, Text: "thread saved"}); err != nil {
		return err
	}
	turn := command.JobID + "-turn"
	if err := emit(control.WorkerEvent{State: control.Running, ThreadID: thread, TurnID: turn}); err != nil {
		return err
	}
	select {
	case <-r.release:
		return emit(control.WorkerEvent{State: control.Completed, ThreadID: thread, TurnID: turn, Text: "durable result on " + r.host, Tokens: 42})
	case <-ctx.Done():
		return emit(control.WorkerEvent{State: control.Canceled, ThreadID: thread, TurnID: turn, Text: "fake runtime confirmed interruption"})
	}
}

type transportFixture struct {
	engine *control.Engine
	hub    *controlapi.Hub
	api    *controlapi.API
	server *httptest.Server
	client *controlapi.Client
	pki    string
	policy control.Options
	pair   control.ModelPair
}

func setupTransport(t *testing.T) *transportFixture {
	t.Helper()
	pki := filepath.Join(t.TempDir(), "pki")
	identities, err := controlapi.CreatePairing(pki, "127.0.0.1", []string{"worker-a", "worker-b", "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	identities = identities[:3]
	identities[0].Principals = []string{"owner:alice", "owner:bob"}
	pair := control.ModelPair{Model: "test-model", Effort: "medium"}
	profile := control.Profile{ModelPair: pair, Permission: "read-only", MaxSeconds: 30, MaxTokens: 1000}
	policy := control.Options{PolicyVersion: "fixture", QueueLimit: 8, Concurrency: 2, RoutingSeconds: 2, RetentionDays: 1, Hosts: []control.Host{{ID: "worker-a", CodexVersion: "fake-1", Models: []control.ModelPair{pair}}, {ID: "worker-b", CodexVersion: "fake-1", Models: []control.ModelPair{pair}}}, Projects: []control.Project{{ID: "project-a", Name: "same", HostID: "worker-a", DefaultProfile: "pilot", Principals: []string{"owner:alice"}, Profiles: map[string]control.Profile{"pilot": profile}}, {ID: "project-b", Name: "same", HostID: "worker-b", DefaultProfile: "pilot", Principals: []string{"owner:alice"}, Profiles: map[string]control.Profile{"pilot": profile}}}}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	store, err := controlstore.Open(t.Context(), filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	hub := controlapi.NewHub(policy.Hosts)
	engine, err := control.NewEngine(t.Context(), store, &policy, hub, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hub.AttachEngine(engine)
	api := &controlapi.API{Engine: engine, Hub: hub, Identities: identities, SaveRegistry: func(controlapi.Registry) error { return nil }}
	server := httptest.NewUnstartedServer(api.Handler())
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS, err = controlapi.LoadTLS(tlsFiles(pki, "controller"), true)
	if err != nil {
		t.Fatal(err)
	}
	server.StartTLS()
	t.Cleanup(func() { hub.Close(); server.Close() })
	tlsConfig, err := controlapi.LoadTLS(tlsFiles(pki, "gateway"), false)
	if err != nil {
		t.Fatal(err)
	}
	client, err := controlapi.NewClient(server.URL, tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return &transportFixture{engine: engine, hub: hub, api: api, server: server, client: client, pki: pki, policy: policy, pair: pair}
}

func tlsFiles(pki, name string) controlapi.TLSFiles {
	return controlapi.TLSFiles{CA: filepath.Join(pki, "ca.pem"), Certificate: filepath.Join(pki, name+".pem"), Key: filepath.Join(pki, name+".key"), ServerName: "127.0.0.1"}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("bounded integration wait expired")
		case <-tick.C:
		}
	}
}

func (f *transportFixture) startWorker(t *testing.T, index int) *testRuntime {
	t.Helper()
	host := f.policy.Hosts[index].ID
	project := f.policy.Projects[index]
	runtime := &testRuntime{pair: f.pair, host: host, release: make(chan struct{})}
	ledger, err := codex.OpenLedger(t.Context(), filepath.Join(t.TempDir(), "worker.db"))
	if err != nil {
		t.Fatal(err)
	}
	worker, err := codex.NewWorker(t.Context(), ledger, codex.WorkerOptions{HostID: host, ControllerURL: f.server.URL, CodexVersion: "fake-1", RetentionDays: 1, Projects: []codex.LocalProject{{ID: project.ID, Root: t.TempDir(), Profiles: []control.Profile{project.Profiles["pilot"]}}}}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig, err := controlapi.LoadTLS(tlsFiles(f.pki, host), false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, tlsConfig) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(12 * time.Second):
			t.Error("worker did not drain")
		}
		if err := ledger.Close(); err != nil {
			t.Error(err)
		}
	})
	return runtime
}

func transportRequest(id, project string) control.Request {
	return control.Request{Principal: control.Principal{Namespace: "owner", Subject: "alice"}, Source: "fixture", RequestID: id, Prompt: "inspect a disposable project", Mode: control.ModeManual, ProjectID: project}
}

func TestTwoAuthenticatedWorkersReplayPartitionResultsAndStayPinned(t *testing.T) {
	f := setupTransport(t)
	a, b := f.startWorker(t, 0), f.startWorker(t, 1)
	waitFor(t, func() bool { hosts := f.hub.Hosts(); return len(hosts) == 2 && hosts[0].Online && hosts[1].Online })
	jobA, err := f.client.Submit(t.Context(), transportRequest("a", "project-a"))
	if err != nil {
		t.Fatal(err)
	}
	jobB, err := f.client.Submit(t.Context(), transportRequest("b", "project-b"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		aJob, _ := f.client.Get(t.Context(), jobA.Request.Principal, jobA.ID)
		bJob, _ := f.client.Get(t.Context(), jobB.Request.Principal, jobB.ID)
		return aJob != nil && bJob != nil && aJob.State == control.Running && bJob.State == control.Running
	})
	if a.calls.Load() != 1 || b.calls.Load() != 1 {
		t.Fatal("jobs did not reach distinct workers")
	}
	requested, err := f.client.Cancel(t.Context(), jobB.Request.Principal, jobB.ID)
	if err != nil || requested.State != control.CancelRequested {
		t.Fatal("cancel must wait for confirmation")
	}
	waitFor(t, func() bool {
		job, _ := f.client.Get(t.Context(), jobB.Request.Principal, jobB.ID)
		return job != nil && job.State == control.Canceled
	})
	f.hub.Close()
	close(a.release)
	waitFor(t, func() bool {
		job, _ := f.client.Get(t.Context(), jobA.Request.Principal, jobA.ID)
		return job != nil && job.State == control.Completed && job.Result == "durable result on worker-a"
	})
	duplicate, err := f.client.Submit(t.Context(), transportRequest("a", "project-a"))
	if err != nil || duplicate.ID != jobA.ID || a.calls.Load() != 1 {
		t.Fatal("lost reply or reconnect duplicated execution")
	}
	saved, err := f.client.Get(t.Context(), jobA.Request.Principal, jobA.ID)
	if err != nil {
		t.Fatal(err)
	}
	followup := transportRequest("followup", "")
	followup.Action = control.ActionContinue
	followup.ConversationID = saved.ConversationID
	followup.Prompt = "add tests to that task"
	continued, err := f.client.Submit(t.Context(), followup)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		job, _ := f.client.Get(t.Context(), followup.Principal, continued.ID)
		return job != nil && job.State == control.Completed
	})
	a.mu.Lock()
	threads := append([]string(nil), a.threads...)
	a.mu.Unlock()
	if len(threads) != 2 || threads[0] != threads[1] || b.calls.Load() != 1 {
		t.Fatalf("continuation moved: %v", threads)
	}
}

func TestTransportRejectsUnknownIdentityCrossPrincipalAndRevokedHost(t *testing.T) {
	f := setupTransport(t)
	unknownTLS, err := controlapi.LoadTLS(tlsFiles(f.pki, "unknown"), false)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := controlapi.NewClient(f.server.URL, unknownTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer unknown.Close()
	if _, err := unknown.Submit(t.Context(), transportRequest("unknown", "project-a")); !errors.Is(err, control.ErrForbidden) {
		t.Fatalf("unknown identity accepted: %v", err)
	}
	bob := transportRequest("bob", "project-a")
	bob.Principal.Subject = "bob"
	if _, err := f.client.Submit(t.Context(), bob); !errors.Is(err, control.ErrForbidden) {
		t.Fatalf("cross-principal submission = %v", err)
	}
	runtime := f.startWorker(t, 0)
	waitFor(t, func() bool {
		for _, host := range f.hub.Hosts() {
			if host.ID == "worker-a" {
				return host.Online
			}
		}
		return false
	})
	registry, err := f.client.Registry(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i := range registry.Identities {
		if registry.Identities[i].HostID == "worker-a" {
			registry.Identities[i].Enabled = false
		}
	}
	if err := f.client.ReplaceRegistry(t.Context(), registry); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, host := range f.hub.Hosts() {
			if host.ID == "worker-a" {
				return !host.Online
			}
		}
		return false
	})
	job, err := f.client.Submit(t.Context(), transportRequest("revoked", "project-a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved, err := f.client.Get(t.Context(), job.Request.Principal, job.ID)
	if err != nil || saved.State != control.Queued || runtime.calls.Load() != 0 {
		t.Fatal("revoked host received new authority")
	}
	if err := f.client.ReplaceRegistry(t.Context(), registry); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("stale registry update = %v", err)
	}
}

func TestWorkerProtocolAndCodexVersionAreNegotiatedBeforeOnline(t *testing.T) {
	for _, wrong := range []string{"protocol", "codex-version"} {
		t.Run(wrong, func(t *testing.T) {
			f := setupTransport(t)
			tlsConfig, err := controlapi.LoadTLS(tlsFiles(f.pki, "worker-a"), false)
			if err != nil {
				t.Fatal(err)
			}
			transport := &http.Transport{TLSClientConfig: tlsConfig}
			defer transport.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			connection, response, err := websocket.Dial(ctx, strings.Replace(f.server.URL, "https://", "wss://", 1)+"/v1/worker", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, Subprotocols: []string{"brainslug-worker.v1"}})
			if err != nil {
				if response != nil && response.Body != nil {
					_ = response.Body.Close()
				}
				t.Fatal(err)
			}
			defer func() { _ = connection.CloseNow() }()
			host := f.policy.Hosts[0]
			version := control.ProtocolVersion
			if wrong == "protocol" {
				version++
			} else {
				host.CodexVersion = "unapproved-version"
			}
			if err := controlapi.WriteFrame(ctx, connection, controlapi.Frame{Version: version, Type: "hello", Host: &host}); err != nil {
				t.Fatal(err)
			}
			if _, err := controlapi.ReadFrame(ctx, connection); err == nil {
				t.Fatal("incompatible worker became ready")
			}
			for _, host := range f.hub.Hosts() {
				if host.Online {
					t.Fatal("incompatible worker advertised availability")
				}
			}
		})
	}
}

func TestLostAcceptanceReplyRetriesTheOriginalDurableRequest(t *testing.T) {
	f := setupTransport(t)
	handler := f.api.Handler()
	var calls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			if recorder.Code != http.StatusAccepted {
				t.Error("first acceptance was not persisted")
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("test response cannot close its connection")
				return
			}
			connection, _, err := hijacker.Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		handler.ServeHTTP(w, r)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = f.server.TLS.Clone()
	server.StartTLS()
	defer server.Close()
	tlsConfig, err := controlapi.LoadTLS(tlsFiles(f.pki, "gateway"), false)
	if err != nil {
		t.Fatal(err)
	}
	client, err := controlapi.NewClient(server.URL, tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	req := transportRequest("lost-acceptance", "project-a")
	job, err := client.Submit(t.Context(), req)
	if err != nil || job.State != control.Accepted || calls.Load() != 2 {
		t.Fatalf("lost response retry = %+v / %v", job, err)
	}
	jobs, err := f.engine.List(t.Context(), req.Principal)
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatal("retry created a second accepted request")
	}
}

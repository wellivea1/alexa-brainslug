package controlapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/wellivea1/alexa-brainslug/internal/control"
)

const maxMessageBytes = 256 << 10

// API serves only the scoped task protocol; no shell or Codex protocol is exposed.
type API struct {
	mu               sync.RWMutex
	manageMu         sync.Mutex
	Engine           *control.Engine
	Hub              *Hub
	Identities       []Identity
	SaveRegistry     func(Registry) error
	RegistryRevision int64
}

// Handler builds the isolated controller routes.
func (a *API) Handler() http.Handler {
	if a.Hub != nil {
		a.Hub.SetIdentityValidator(func(identity Identity) bool {
			a.mu.RLock()
			defer a.mu.RUnlock()
			for _, current := range a.Identities {
				if current.Enabled && current.Role == RoleWorker && current.HostID == identity.HostID && strings.EqualFold(current.Fingerprint, identity.Fingerprint) {
					return true
				}
			}
			return false
		})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.authenticate(r); !ok {
			http.Error(w, "identity is not enrolled", http.StatusForbidden)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "protocol_version": control.ProtocolVersion, "desktop_placement": "unverified"})
	})
	mux.HandleFunc("GET /v1/catalog", a.catalog)
	mux.HandleFunc("POST /v1/jobs", a.submit)
	mux.HandleFunc("GET /v1/jobs", a.list)
	mux.HandleFunc("GET /v1/jobs/{id}", a.get)
	mux.HandleFunc("GET /v1/jobs/{id}/events", a.events)
	mux.HandleFunc("POST /v1/jobs/{id}/cancel", a.cancel)
	mux.HandleFunc("POST /v1/jobs/{id}/approval", a.approval)
	mux.HandleFunc("POST /v1/jobs/{id}/reply", a.reply)
	mux.HandleFunc("GET /v1/owner/registry", a.registry)
	mux.HandleFunc("PUT /v1/owner/registry", a.replaceRegistry)
	mux.HandleFunc("POST /v1/owner/conversations", a.importConversation)
	mux.HandleFunc("POST /v1/owner/jobs/{id}/resolve", a.resolve)
	mux.HandleFunc("GET /v1/worker", func(w http.ResponseWriter, r *http.Request) {
		identity, ok := a.authenticate(r)
		if !ok || identity.Role != RoleWorker || a.Hub == nil {
			http.Error(w, "worker identity is not enrolled", http.StatusForbidden)
			return
		}
		a.Hub.ServeWorker(w, r, identity)
	})
	return mux
}

func (a *API) owner(w http.ResponseWriter, r *http.Request, principal control.Principal) bool {
	identity, ok := a.authenticate(r)
	if !ok || identity.Role != RoleGateway || !principal.Valid() || !permitsPrincipal(identity, principal.Key()) {
		http.Error(w, "task principal is not authorized", http.StatusForbidden)
		return false
	}
	if r.Header.Get("X-Brainslug-Protocol") != strconv.Itoa(control.ProtocolVersion) {
		http.Error(w, "incompatible task protocol", http.StatusConflict)
		return false
	}
	return true
}

func queryPrincipal(r *http.Request) control.Principal {
	return control.Principal{Namespace: r.URL.Query().Get("namespace"), Subject: r.URL.Query().Get("subject")}
}

func readJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxMessageBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		http.Error(w, "invalid task message", http.StatusBadRequest)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid task message", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	switch data := value.(type) {
	case *control.Job:
		value = publicJob(data, false)
	case []*control.Job:
		jobs := make([]*control.Job, 0, len(data))
		for _, job := range data {
			jobs = append(jobs, publicJob(job, true))
		}
		value = jobs
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func publicJob(job *control.Job, summary bool) *control.Job {
	if job == nil {
		return nil
	}
	snapshot := *job
	snapshot.PolicySnapshot = nil
	if summary {
		if snapshot.Shadow != nil {
			proposal := *snapshot.Shadow
			if len(proposal.Reason) > 256 {
				proposal.Reason = proposal.Reason[:256]
			}
			snapshot.Shadow = &proposal
		}
		if snapshot.Approval != nil {
			approval := *snapshot.Approval
			if len(approval.Description) > 512 {
				approval.Description = approval.Description[:512]
			}
			snapshot.Approval = &approval
		}
		if len(snapshot.Request.Prompt) > 256 {
			snapshot.Request.Prompt = snapshot.Request.Prompt[:256] + "..."
		}
		if len(snapshot.Result) > 512 {
			snapshot.Result = snapshot.Result[:512] + "..."
		}
		if len(snapshot.Decision.Reason) > 512 {
			snapshot.Decision.Reason = snapshot.Decision.Reason[:512]
		}
		if len(snapshot.Problem) > 512 {
			snapshot.Problem = snapshot.Problem[:512]
		}
	}
	return &snapshot
}

func taskError(w http.ResponseWriter, err error) {
	status, message := http.StatusInternalServerError, "task operation failed"
	switch {
	case errors.Is(err, control.ErrForbidden):
		status, message = http.StatusForbidden, control.ErrForbidden.Error()
	case errors.Is(err, control.ErrNotFound):
		status, message = http.StatusNotFound, control.ErrNotFound.Error()
	case errors.Is(err, control.ErrConflict):
		status, message = http.StatusConflict, control.ErrConflict.Error()
	case errors.Is(err, control.ErrQueueFull):
		status, message = http.StatusTooManyRequests, control.ErrQueueFull.Error()
	case errors.Is(err, control.ErrInvalid):
		status, message = http.StatusBadRequest, control.ErrInvalid.Error()
	case errors.Is(err, control.ErrUnavailable):
		status, message = http.StatusServiceUnavailable, control.ErrUnavailable.Error()
	}
	http.Error(w, message, status)
}

func (a *API) submit(w http.ResponseWriter, r *http.Request) {
	var request control.Request
	if !readJSON(w, r, &request) || !a.owner(w, r, request.Principal) {
		return
	}
	job, err := a.Engine.Submit(r.Context(), request)
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) catalog(w http.ResponseWriter, r *http.Request) {
	owner := queryPrincipal(r)
	if !a.owner(w, r, owner) {
		return
	}
	catalog, err := a.Engine.Catalog(r.Context(), owner)
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	owner := queryPrincipal(r)
	if !a.owner(w, r, owner) {
		return
	}
	jobs, err := a.Engine.List(r.Context(), owner)
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	owner := queryPrincipal(r)
	if !a.owner(w, r, owner) {
		return
	}
	job, err := a.Engine.Get(r.Context(), owner, r.PathValue("id"))
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	owner := queryPrincipal(r)
	if !a.owner(w, r, owner) {
		return
	}
	after, err := strconv.ParseInt(first(r.URL.Query().Get("after"), "0"), 10, 64)
	if err != nil || after < 0 {
		taskError(w, control.ErrInvalid)
		return
	}
	events, err := a.Engine.Events(r.Context(), owner, r.PathValue("id"), after)
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (a *API) cancel(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Principal control.Principal `json:"principal"`
	}
	if !readJSON(w, r, &request) || !a.owner(w, r, request.Principal) {
		return
	}
	job, err := a.Engine.Cancel(r.Context(), request.Principal, r.PathValue("id"))
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (a *API) approval(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Principal  control.Principal `json:"principal"`
		ApprovalID string            `json:"approval_id"`
		Decision   string            `json:"decision"`
	}
	if !readJSON(w, r, &request) || !a.owner(w, r, request.Principal) {
		return
	}
	job, err := a.Engine.Approve(r.Context(), request.Principal, r.PathValue("id"), request.ApprovalID, request.Decision)
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (a *API) reply(w http.ResponseWriter, r *http.Request) {
	var request control.Request
	if !readJSON(w, r, &request) || !a.owner(w, r, request.Principal) {
		return
	}
	job, err := a.Engine.Reply(r.Context(), request.Principal, r.PathValue("id"), request)
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

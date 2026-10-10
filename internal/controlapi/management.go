package controlapi

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/wellivea1/alexa-brainslug/internal/control"
)

// Registry is the owner-editable catalog and enrolled identities, without credentials or local roots.
type Registry struct {
	Revision   int64            `json:"revision"`
	Policy     *control.Options `json:"policy"`
	Identities []Identity       `json:"identities"`
}

// Validate requires explicit identity roles, fingerprints, and an owner gateway.
func (r *Registry) Validate() error {
	if r.Policy == nil {
		return control.ErrInvalid
	}
	if err := r.Policy.Validate(); err != nil {
		return err
	}
	seen := make(map[string]bool)
	owner := false
	for _, identity := range r.Identities {
		fingerprint := strings.ToLower(identity.Fingerprint)
		data, err := hex.DecodeString(fingerprint)
		if err != nil || len(data) != 32 || seen[fingerprint] || identity.Name == "" {
			return control.ErrInvalid
		}
		seen[fingerprint] = true
		switch identity.Role {
		case RoleGateway:
			if identity.HostID != "" || len(identity.Principals) == 0 {
				return control.ErrInvalid
			}
			owner = owner || (identity.Owner && identity.Enabled)
		case RoleWorker:
			if identity.Owner || identity.HostID == "" || len(identity.Principals) != 0 {
				return control.ErrInvalid
			}
			found := false
			for i := range r.Policy.Hosts {
				host := &r.Policy.Hosts[i]
				if host.ID == identity.HostID {
					found = true
				}
			}
			if !found {
				return control.ErrInvalid
			}
		default:
			return control.ErrInvalid
		}
	}
	if !owner {
		return control.ErrInvalid
	}
	return nil
}

func (a *API) authenticate(r *http.Request) (Identity, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return authenticated(r, a.Identities)
}

func (a *API) administrator(w http.ResponseWriter, r *http.Request) bool {
	identity, ok := a.authenticate(r)
	if !ok || identity.Role != RoleGateway || !identity.Owner {
		http.Error(w, "owner gateway identity required", http.StatusForbidden)
		return false
	}
	if r.Header.Get("X-Brainslug-Protocol") != strconv.Itoa(control.ProtocolVersion) {
		http.Error(w, "incompatible task protocol", http.StatusConflict)
		return false
	}
	return true
}

func (a *API) registry(w http.ResponseWriter, r *http.Request) {
	if !a.administrator(w, r) {
		return
	}
	a.manageMu.Lock()
	defer a.manageMu.Unlock()
	policy, err := a.Engine.Policy()
	if err != nil {
		taskError(w, err)
		return
	}
	a.mu.RLock()
	identities := append([]Identity(nil), a.Identities...)
	a.mu.RUnlock()
	a.mu.RLock()
	revision := a.RegistryRevision
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, Registry{Revision: revision, Policy: policy, Identities: identities})
}

func (a *API) replaceRegistry(w http.ResponseWriter, r *http.Request) {
	if !a.administrator(w, r) {
		return
	}
	var registry Registry
	if !readJSON(w, r, &registry) {
		return
	}
	if err := registry.Validate(); err != nil {
		taskError(w, err)
		return
	}
	if a.SaveRegistry == nil {
		taskError(w, control.ErrUnavailable)
		return
	}
	a.manageMu.Lock()
	defer a.manageMu.Unlock()
	a.mu.RLock()
	currentRevision := a.RegistryRevision
	a.mu.RUnlock()
	if registry.Revision != currentRevision {
		taskError(w, control.ErrConflict)
		return
	}
	registry.Revision++
	if err := a.Engine.ReplacePolicy(registry.Policy, func() error { return a.SaveRegistry(registry) }); err != nil {
		taskError(w, err)
		return
	}
	a.mu.Lock()
	a.Identities = registry.Identities
	a.RegistryRevision = registry.Revision
	a.mu.Unlock()
	a.Hub.UpdateRegistry(registry.Policy.Hosts)
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (a *API) importConversation(w http.ResponseWriter, r *http.Request) {
	if !a.administrator(w, r) {
		return
	}
	var conversation control.Conversation
	if !readJSON(w, r, &conversation) || !a.owner(w, r, conversation.Principal) {
		return
	}
	saved, err := a.Engine.ImportConversation(r.Context(), &conversation)
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (a *API) resolve(w http.ResponseWriter, r *http.Request) {
	if !a.administrator(w, r) {
		return
	}
	var request struct {
		Principal control.Principal `json:"principal"`
	}
	if !readJSON(w, r, &request) || !a.owner(w, r, request.Principal) {
		return
	}
	job, err := a.Engine.Resolve(r.Context(), request.Principal, r.PathValue("id"))
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// Registry reads owner management state through the enrolled gateway identity.
func (c *Client) Registry(ctx context.Context) (*Registry, error) {
	var registry Registry
	err := c.call(ctx, http.MethodGet, "/v1/owner/registry", nil, &registry)
	return &registry, err
}

// ReplaceRegistry atomically saves the owner catalog and credential enrollment.
func (c *Client) ReplaceRegistry(ctx context.Context, registry *Registry) error {
	return c.call(ctx, http.MethodPut, "/v1/owner/registry", registry, nil)
}

// ImportConversation approves a host-pinned existing thread for later verified continuation.
func (c *Client) ImportConversation(ctx context.Context, conversation *control.Conversation) (*control.Conversation, error) {
	var saved control.Conversation
	err := c.call(ctx, http.MethodPost, "/v1/owner/conversations", conversation, &saved)
	return &saved, err
}

// Resolve requests owner acknowledgement of an uncertain stopped attempt.
func (c *Client) Resolve(ctx context.Context, owner control.Principal, id string) (*control.Job, error) {
	var job control.Job
	err := c.call(ctx, http.MethodPost, "/v1/owner/jobs/"+url.PathEscape(id)+"/resolve", map[string]any{fieldPrincipal: owner}, &job)
	return &job, err
}

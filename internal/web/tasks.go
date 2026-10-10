package web

import (
	"context"
	"crypto/rand"
	_ "embed"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/config"
	"github.com/wellivea1/alexa-brainslug/internal/control"
	"github.com/wellivea1/alexa-brainslug/internal/controlapi"
)

//go:embed templates/tasks.html
var tasksTemplateSource string

var tasksTemplate = template.Must(template.New("tasks").Funcs(template.FuncMap{"terminal": control.Terminal, "when": func(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format("Jan 2, 15:04")
}}).Parse(tasksTemplateSource))

type taskPage struct {
	CSRFToken  string
	Settings   config.CodexConfig
	Message    string
	Problem    string
	Registry   *controlapi.Registry
	Catalog    control.Catalog
	Jobs       []*control.Job
	Principals []string
	Principal  string
	RequestID  string
}

// RegisterTaskRoutes installs owner-session routes only on the private listener.
func (d *Dashboard) RegisterTaskRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /codex", d.HandleTasks)
	mux.HandleFunc("POST /codex/connection", d.taskConnection)
	mux.HandleFunc("POST /codex/request", d.taskSubmit)
	mux.HandleFunc("POST /codex/job", d.taskJob)
	mux.HandleFunc("POST /codex/registry", d.taskRegistry)
	mux.HandleFunc("POST /codex/import", d.taskImport)
}

// HandleTasks presents saved operational state, separate from ordinary answer history.
func (d *Dashboard) HandleTasks(w http.ResponseWriter, r *http.Request) {
	data := taskPage{CSRFToken: d.csrfToken, RequestID: rand.Text(), Principal: r.URL.Query().Get("principal"), Message: r.URL.Query().Get("notice")}
	if d.tasks != nil {
		data.Settings = d.tasks.Settings()
		client, err := d.tasks.Client()
		if err == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			data.Registry, err = client.Registry(ctx)
			if err == nil {
				for _, identity := range data.Registry.Identities {
					if identity.Enabled && identity.Role == "gateway" {
						for _, key := range identity.Principals {
							if !slices.Contains(data.Principals, key) {
								data.Principals = append(data.Principals, key)
							}
						}
					}
				}
				if data.Principal == "" && len(data.Principals) > 0 {
					data.Principal = data.Principals[0]
				}
				owner, ownerErr := parseTaskPrincipal(data.Principal)
				if ownerErr == nil {
					catalogCh := make(chan struct {
						catalog control.Catalog
						err     error
					}, 1)
					go func() {
						catalog, err := client.Catalog(ctx, owner)
						catalogCh <- struct {
							catalog control.Catalog
							err     error
						}{catalog, err}
					}()
					data.Jobs, err = client.List(ctx, owner)
					result := <-catalogCh
					data.Catalog = result.catalog
					if err == nil {
						err = result.err
					}
					if selected := r.URL.Query().Get("job"); selected != "" && err == nil {
						job, loadErr := client.Get(ctx, owner, selected)
						if loadErr != nil {
							err = loadErr
						} else {
							found := false
							for i := range data.Jobs {
								if data.Jobs[i].ID == job.ID {
									data.Jobs[i] = job
									found = true
								}
							}
							if !found {
								data.Jobs = append([]*control.Job{job}, data.Jobs...)
							}
						}
					}
				} else {
					err = ownerErr
				}
			}
		}
		if err != nil && data.Settings.Enabled {
			data.Problem = taskProblem(err)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tasksTemplate.Execute(w, data); err != nil {
		return
	}
}

func parseTaskPrincipal(key string) (control.Principal, error) {
	namespace, subject, ok := strings.Cut(strings.TrimSpace(key), ":")
	owner := control.Principal{Namespace: namespace, Subject: subject}
	if !ok || !owner.Valid() {
		return control.Principal{}, control.ErrInvalid
	}
	return owner, nil
}

func taskProblem(err error) string {
	switch {
	case errors.Is(err, control.ErrForbidden):
		return "This gateway or principal has no grant for this operation. Check its enrollment and project grants."
	case errors.Is(err, control.ErrConflict):
		return "The saved state changed or conflicts with that selection. Refresh the page and review the current task or registry."
	case errors.Is(err, control.ErrQueueFull):
		return "The bounded task queue is full."
	case errors.Is(err, control.ErrInvalid):
		return "The task settings are invalid or outside the registered policy."
	default:
		return "The controller could not confirm this operation. Check its connection and saved task state."
	}
}

func (d *Dashboard) taskClient(w http.ResponseWriter, r *http.Request) (*controlapi.Client, bool) {
	if !d.authorizeMutation(w, r) {
		return nil, false
	}
	if d.tasks == nil {
		http.Error(w, "task control is unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	client, err := d.tasks.Client()
	if err != nil {
		http.Error(w, taskProblem(err), http.StatusServiceUnavailable)
		return nil, false
	}
	return client, true
}

func taskRedirect(w http.ResponseWriter, r *http.Request, key, notice string) {
	dashboardRedirect(w, r, "/codex?"+url.Values{"principal": {key}, "notice": {notice}}.Encode())
}

func (d *Dashboard) taskConnection(w http.ResponseWriter, r *http.Request) {
	if !d.authorizeMutation(w, r) {
		return
	}
	if d.tasks == nil {
		http.Error(w, "task management unavailable", http.StatusServiceUnavailable)
		return
	}
	settings := config.CodexConfig{Enabled: r.PostFormValue("enabled") == "on", Mode: r.PostFormValue("mode"), URL: strings.TrimSpace(r.PostFormValue("url")), Certificate: strings.TrimSpace(r.PostFormValue("certificate")), Key: strings.TrimSpace(r.PostFormValue("key")), CA: strings.TrimSpace(r.PostFormValue("ca")), ServerName: strings.TrimSpace(r.PostFormValue("server_name"))}
	if err := d.tasks.Update(settings); err != nil {
		http.Error(w, "Controller settings were not saved. Check the HTTPS origin and enrolled certificate files.", http.StatusBadRequest)
		return
	}
	taskRedirect(w, r, "", "Controller connection settings saved.")
}

func (d *Dashboard) taskSubmit(w http.ResponseWriter, r *http.Request) {
	client, ok := d.taskClient(w, r)
	if !ok {
		return
	}
	owner, err := parseTaskPrincipal(r.PostFormValue("principal"))
	if err != nil {
		http.Error(w, taskProblem(err), http.StatusBadRequest)
		return
	}
	request := control.Request{Principal: owner, Source: "dashboard", RequestID: r.PostFormValue("request_id"), Prompt: strings.TrimSpace(r.PostFormValue("prompt")), Mode: r.PostFormValue("mode"), Action: r.PostFormValue("action"), HostID: r.PostFormValue("host_id"), ProjectID: r.PostFormValue("project_id"), ConversationID: r.PostFormValue("conversation_id"), Profile: r.PostFormValue("profile"), Model: r.PostFormValue("model"), Effort: r.PostFormValue("effort"), Title: r.PostFormValue("title")}
	if _, err := client.Submit(r.Context(), request); err != nil {
		http.Error(w, taskProblem(err), http.StatusBadRequest)
		return
	}
	taskRedirect(w, r, owner.Key(), "Request saved by the controller.")
}

func (d *Dashboard) taskJob(w http.ResponseWriter, r *http.Request) {
	client, ok := d.taskClient(w, r)
	if !ok {
		return
	}
	owner, err := parseTaskPrincipal(r.PostFormValue("principal"))
	if err != nil {
		http.Error(w, taskProblem(err), http.StatusBadRequest)
		return
	}
	id := r.PostFormValue("job_id")
	switch r.PostFormValue("operation") {
	case "cancel":
		_, err = client.Cancel(r.Context(), owner, id)
	case "resolve":
		_, err = client.Resolve(r.Context(), owner, id)
	case "accept", "decline":
		_, err = client.Approve(r.Context(), owner, id, r.PostFormValue("approval_id"), r.PostFormValue("operation"))
	case "reply":
		_, err = client.Reply(r.Context(), owner, id, control.Request{Principal: owner, Source: "dashboard", RequestID: r.PostFormValue("request_id"), Prompt: r.PostFormValue("prompt"), ProjectID: r.PostFormValue("project_id"), ConversationID: r.PostFormValue("conversation_id")})
	default:
		err = control.ErrInvalid
	}
	if err != nil {
		http.Error(w, taskProblem(err), http.StatusBadRequest)
		return
	}
	taskRedirect(w, r, owner.Key(), "Scoped task action sent. Status reflects worker confirmation.")
}

func splitLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(text, ",", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func (d *Dashboard) taskRegistry(w http.ResponseWriter, r *http.Request) {
	client, ok := d.taskClient(w, r)
	if !ok {
		return
	}
	registry, err := client.Registry(r.Context())
	if err != nil {
		http.Error(w, taskProblem(err), http.StatusBadRequest)
		return
	}
	switch r.PostFormValue("kind") {
	case "host":
		host := control.Host{ID: strings.TrimSpace(r.PostFormValue("id")), Name: strings.TrimSpace(r.PostFormValue("name")), CodexVersion: strings.TrimSpace(r.PostFormValue("codex_version"))}
		for _, line := range splitLines(r.PostFormValue("pairs")) {
			model, effort, ok := strings.Cut(line, "/")
			if !ok {
				err = control.ErrInvalid
				break
			}
			host.Models = append(host.Models, control.ModelPair{Model: strings.TrimSpace(model), Effort: strings.TrimSpace(effort)})
		}
		found := false
		for i := range registry.Policy.Hosts {
			if registry.Policy.Hosts[i].ID == host.ID {
				registry.Policy.Hosts[i] = host
				found = true
			}
		}
		if !found {
			registry.Policy.Hosts = append(registry.Policy.Hosts, host)
		}
	case "project":
		seconds, secondsErr := strconv.Atoi(r.PostFormValue("max_seconds"))
		tokens, tokensErr := strconv.ParseInt(r.PostFormValue("max_tokens"), 10, 64)
		if secondsErr != nil || tokensErr != nil {
			err = control.ErrInvalid
			break
		}
		profile := control.Profile{ModelPair: control.ModelPair{Model: strings.TrimSpace(r.PostFormValue("model")), Effort: strings.TrimSpace(r.PostFormValue("effort"))}, Permission: r.PostFormValue("permission"), MaxSeconds: seconds, MaxTokens: tokens}
		id := strings.TrimSpace(r.PostFormValue("id"))
		name := strings.TrimSpace(r.PostFormValue("profile"))
		project := control.Project{ID: id, Name: r.PostFormValue("name"), HostID: r.PostFormValue("host_id"), Aliases: splitLines(r.PostFormValue("aliases")), Description: r.PostFormValue("description"), Principals: splitLines(r.PostFormValue("principals")), DefaultProfile: name, LockProfile: r.PostFormValue("locked") == "on", Profiles: map[string]control.Profile{name: profile}}
		found := false
		for i := range registry.Policy.Projects {
			if registry.Policy.Projects[i].ID == id {
				previous := registry.Policy.Projects[i]
				for key, value := range previous.Profiles {
					if key != name {
						project.Profiles[key] = value
					}
				}
				registry.Policy.Projects[i] = project
				found = true
			}
		}
		if !found {
			registry.Policy.Projects = append(registry.Policy.Projects, project)
		}
	case "identity":
		identity := controlapi.Identity{Name: r.PostFormValue("name"), Fingerprint: strings.TrimSpace(r.PostFormValue("fingerprint")), Role: r.PostFormValue("role"), HostID: strings.TrimSpace(r.PostFormValue("host_id")), Principals: splitLines(r.PostFormValue("principals")), Owner: r.PostFormValue("owner") == "on", Enabled: r.PostFormValue("enabled") == "on"}
		found := false
		for i := range registry.Identities {
			if strings.EqualFold(registry.Identities[i].Fingerprint, identity.Fingerprint) {
				registry.Identities[i] = identity
				found = true
			}
		}
		if !found {
			registry.Identities = append(registry.Identities, identity)
		}
	case "policy":
		retention, retentionErr := strconv.Atoi(r.PostFormValue("retention_days"))
		queue, queueErr := strconv.Atoi(r.PostFormValue("queue_limit"))
		concurrency, concurrencyErr := strconv.Atoi(r.PostFormValue("concurrency"))
		target, targetErr := strconv.ParseFloat(r.PostFormValue("accuracy_target"), 64)
		if retentionErr != nil || queueErr != nil || concurrencyErr != nil || targetErr != nil {
			err = control.ErrInvalid
			break
		}
		registry.Policy.RetentionDays, registry.Policy.QueueLimit, registry.Policy.Concurrency = retention, queue, concurrency
		registry.Policy.AutomaticEnabled = r.PostFormValue("automatic") == "on"
		registry.Policy.RouterEvaluationApproved = r.PostFormValue("evaluated") == "on"
		registry.Policy.RoutingAccuracyTarget = target
		registry.Policy.DefaultProject = r.PostFormValue("default_project")
	default:
		err = control.ErrInvalid
	}
	if err == nil {
		registry.Policy.PolicyVersion = "owner-" + rand.Text()
		err = client.ReplaceRegistry(r.Context(), registry)
	}
	if err != nil {
		http.Error(w, taskProblem(err), http.StatusBadRequest)
		return
	}
	taskRedirect(w, r, r.PostFormValue("principal"), "Registry saved. Workers reconnect and renegotiate current grants.")
}

func (d *Dashboard) taskImport(w http.ResponseWriter, r *http.Request) {
	client, ok := d.taskClient(w, r)
	if !ok {
		return
	}
	owner, err := parseTaskPrincipal(r.PostFormValue("principal"))
	if err != nil {
		http.Error(w, taskProblem(err), http.StatusBadRequest)
		return
	}
	_, err = client.ImportConversation(r.Context(), &control.Conversation{Principal: owner, HostID: r.PostFormValue("host_id"), ProjectID: r.PostFormValue("project_id"), ThreadID: strings.TrimSpace(r.PostFormValue("thread_id")), Title: r.PostFormValue("title"), Profile: r.PostFormValue("profile")})
	if err != nil {
		http.Error(w, taskProblem(err), http.StatusBadRequest)
		return
	}
	taskRedirect(w, r, owner.Key(), "Conversation registered. The worker verifies its directory and idle state before continuation.")
}

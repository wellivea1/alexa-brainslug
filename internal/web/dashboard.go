// Package web serves the loopback-only Brainslug management dashboard.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/inference"
	"github.com/wellivea1/alexa-brainslug/internal/store"
)

const (
	defaultLimit             = 25
	managementHistoryLimit   = 100
	overviewInteractionLimit = 5
	sectionOverview          = "overview"
	sectionActivity          = "activity"
	sectionInference         = "inference"
	providerOpenAI           = "openai"
	providerAnthropic        = "anthropic"
)

//go:embed templates/dashboard.html
var dashboardTemplateSource string

//go:embed assets/dashboard.css
var dashboardCSS []byte

// Store is the history capability used by the dashboard.
type Store interface {
	ListRecent(ctx context.Context, limit int) ([]store.Interaction, error)
}

// InferenceController is the secret-free management boundary exposed to the
// dashboard.
type InferenceController interface {
	Snapshot() inference.Snapshot
	Update(inference.Update) error
}

// Options describes fixed process state and optional test seams.
type Options struct {
	Inference               InferenceController
	StartedAt               time.Time
	AlexaVerifyRequests     bool
	HistoryEnabled          bool
	PublicBaseURLConfigured bool
	SkillIDConfigured       bool
	CSRFToken               string
}

// Dashboard renders operational state, history, and provider management.
type Dashboard struct {
	store                   Store
	inference               InferenceController
	startedAt               time.Time
	alexaVerifyRequests     bool
	historyEnabled          bool
	publicBaseURLConfigured bool
	skillIDConfigured       bool
	csrfToken               string
}

// NewDashboard builds a dashboard. The variadic option preserves the small
// constructor used by store-focused tests while production supplies exactly
// one complete Options value.
func NewDashboard(history Store, configured ...Options) *Dashboard {
	var options Options
	if len(configured) > 0 {
		options = configured[0]
	}
	if options.StartedAt.IsZero() {
		options.StartedAt = time.Now().UTC()
	}
	if options.CSRFToken == "" {
		options.CSRFToken = newCSRFToken()
	}
	return &Dashboard{
		store:                   history,
		inference:               options.Inference,
		startedAt:               options.StartedAt,
		alexaVerifyRequests:     options.AlexaVerifyRequests,
		historyEnabled:          options.HistoryEnabled,
		publicBaseURLConfigured: options.PublicBaseURLConfigured,
		skillIDConfigured:       options.SkillIDConfigured,
		csrfToken:               options.CSRFToken,
	}
}

// HandleIndex renders the selected dashboard section.
func (d *Dashboard) HandleIndex(w http.ResponseWriter, r *http.Request) {
	d.renderIndex(w, r, http.StatusOK, "")
}

// HandleStyles serves the embedded dashboard design system.
func (d *Dashboard) HandleStyles(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(dashboardCSS)
}

// HandleProviderUpdate validates and activates non-secret inference settings.
func (d *Dashboard) HandleProviderUpdate(w http.ResponseWriter, r *http.Request) {
	if d.inference == nil {
		http.Error(w, "inference management is unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		d.renderIndex(w, r, http.StatusBadRequest, "The provider form could not be read.")
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf_token")), []byte(d.csrfToken)) != 1 {
		http.Error(w, "invalid dashboard form token", http.StatusForbidden)
		return
	}
	if err := sameOrigin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	maxOutputTokens, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("max_output_tokens")))
	if err != nil {
		d.renderIndex(w, r, http.StatusBadRequest, "Maximum output tokens must be a whole number.")
		return
	}
	timeoutSeconds, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("timeout_seconds")))
	if err != nil {
		d.renderIndex(w, r, http.StatusBadRequest, "Timeout must be a whole number of seconds.")
		return
	}

	err = d.inference.Update(inference.Update{
		Provider:        r.PostFormValue("provider"),
		Model:           r.PostFormValue("model"),
		BaseURL:         r.PostFormValue("base_url"),
		MaxOutputTokens: maxOutputTokens,
		TimeoutSeconds:  timeoutSeconds,
		ReasoningEffort: r.PostFormValue("reasoning_effort"),
		TextVerbosity:   r.PostFormValue("text_verbosity"),
	})
	if err != nil {
		d.renderIndex(w, r, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, "/?section=inference&provider="+url.QueryEscape(r.PostFormValue("provider"))+"&saved=1", http.StatusSeeOther)
}

// HandleHistory serves bounded recent interactions as JSON.
func (d *Dashboard) HandleHistory(w http.ResponseWriter, r *http.Request) {
	limit := defaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}

	interactions, err := d.store.ListRecent(r.Context(), limit)
	if err != nil {
		slog.ErrorContext(r.Context(), "load dashboard history", "error", err)
		http.Error(w, "could not load history", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"interactions": interactions}); err != nil {
		slog.ErrorContext(r.Context(), "encode history", "error", err)
	}
}

type pageData struct {
	Section              string
	PageTitle            string
	PageDescription      string
	GeneratedAt          time.Time
	Uptime               string
	CSRFToken            string
	Notice               string
	FormError            string
	System               systemView
	Stats                dashboardStats
	Inference            inference.Snapshot
	ActiveProvider       inference.ProviderState
	Interactions         []store.Interaction
	OverviewInteractions []store.Interaction
	Query                string
	StatusFilter         string
	ProviderFilter       string
	ProviderTab          string
	OpenAIModels         []modelOption
}

type systemView struct {
	Verdict                  string
	Tone                     string
	Detail                   string
	AlexaVerification        bool
	HistoryEnabled           bool
	PublicEndpointConfigured bool
	SkillIDConfigured        bool
}

type dashboardStats struct {
	Total        int
	Successful   int
	Errors       int
	SuccessRate  string
	AverageMS    string
	P95MS        string
	LastActivity string
}

type modelOption struct {
	ID          string
	Name        string
	Description string
}

var openAIModels = []modelOption{
	{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", Description: "Highest capability"},
	{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", Description: "Balanced intelligence and cost"},
	{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", Description: "Lowest cost and fastest tier"},
}

func (d *Dashboard) renderIndex(w http.ResponseWriter, r *http.Request, status int, formError string) {
	interactions, err := d.store.ListRecent(r.Context(), managementHistoryLimit)
	if err != nil {
		slog.ErrorContext(r.Context(), "load dashboard history", "error", err)
		http.Error(w, "could not load history", http.StatusInternalServerError)
		return
	}

	section := normalizedSection(firstNonEmpty(r.FormValue("section"), r.URL.Query().Get("section")))
	query := strings.TrimSpace(firstNonEmpty(r.FormValue("query"), r.URL.Query().Get("query")))
	statusFilter := strings.ToLower(strings.TrimSpace(firstNonEmpty(r.FormValue("status"), r.URL.Query().Get("status"))))
	providerFilter := strings.ToLower(strings.TrimSpace(firstNonEmpty(r.FormValue("provider_filter"), r.URL.Query().Get("provider_filter"))))
	filtered := filterInteractions(interactions, query, statusFilter, providerFilter)

	var inferenceSnapshot inference.Snapshot
	if d.inference != nil {
		inferenceSnapshot = d.inference.Snapshot()
	}
	activeProvider := inferenceSnapshot.OpenAI
	if inferenceSnapshot.ActiveProvider == providerAnthropic {
		activeProvider = inferenceSnapshot.Anthropic
	}
	providerTab := normalizedProviderTab(firstNonEmpty(r.FormValue("provider"), r.URL.Query().Get("provider")), inferenceSnapshot.ActiveProvider)
	system := d.systemView(activeProvider)
	pageTitle, pageDescription := sectionCopy(section)
	overview := interactions
	if len(overview) > overviewInteractionLimit {
		overview = overview[:overviewInteractionLimit]
	}
	data := pageData{
		Section:              section,
		PageTitle:            pageTitle,
		PageDescription:      pageDescription,
		GeneratedAt:          time.Now().UTC(),
		Uptime:               formatUptime(time.Since(d.startedAt)),
		CSRFToken:            d.csrfToken,
		FormError:            formError,
		System:               system,
		Stats:                calculateStats(interactions),
		Inference:            inferenceSnapshot,
		ActiveProvider:       activeProvider,
		Interactions:         filtered,
		OverviewInteractions: overview,
		Query:                query,
		StatusFilter:         statusFilter,
		ProviderFilter:       providerFilter,
		ProviderTab:          providerTab,
		OpenAIModels:         openAIModels,
	}
	if r.URL.Query().Get("saved") == "1" {
		data.Notice = "Inference settings saved and activated."
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := dashboardTemplate.Execute(w, data); err != nil {
		slog.ErrorContext(r.Context(), "render dashboard", "error", err)
	}
}

func (d *Dashboard) systemView(active inference.ProviderState) systemView {
	view := systemView{
		Verdict:                  "Ready for Alexa",
		Tone:                     "ok",
		Detail:                   "The request boundary, inference provider, and local history are available.",
		AlexaVerification:        d.alexaVerifyRequests,
		HistoryEnabled:           d.historyEnabled,
		PublicEndpointConfigured: d.publicBaseURLConfigured,
		SkillIDConfigured:        d.skillIDConfigured,
	}
	var missing []string
	if !active.CredentialConfigured {
		missing = append(missing, "provider credential")
	}
	if strings.TrimSpace(active.Model) == "" {
		missing = append(missing, "model")
	}
	if !d.alexaVerifyRequests {
		missing = append(missing, "request verification")
	}
	if !d.skillIDConfigured {
		missing = append(missing, "allowed skill ID")
	}
	if !d.publicBaseURLConfigured {
		missing = append(missing, "public endpoint")
	}
	if len(missing) > 0 {
		view.Verdict = "Review setup"
		view.Tone = "warn"
		view.Detail = "Needs attention: " + strings.Join(missing, ", ") + "."
	}
	return view
}

func calculateStats(interactions []store.Interaction) dashboardStats {
	stats := dashboardStats{Total: len(interactions), LastActivity: "No activity yet", AverageMS: "—", P95MS: "—", SuccessRate: "—"}
	if len(interactions) == 0 {
		return stats
	}
	latencies := make([]int64, 0, len(interactions))
	var latencyTotal int64
	for index := range interactions {
		interaction := &interactions[index]
		if interaction.Status == "ok" {
			stats.Successful++
		} else {
			stats.Errors++
		}
		if interaction.LatencyMS > 0 {
			latencies = append(latencies, interaction.LatencyMS)
			latencyTotal += interaction.LatencyMS
		}
	}
	stats.SuccessRate = strconv.Itoa((stats.Successful*100+len(interactions)/2)/len(interactions)) + "%"
	if len(latencies) > 0 {
		stats.AverageMS = formatMilliseconds(latencyTotal / int64(len(latencies)))
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		index := (95*len(latencies) + 99) / 100
		stats.P95MS = formatMilliseconds(latencies[max(0, index-1)])
	}
	stats.LastActivity = formatRelative(time.Since(interactions[0].CreatedAt))
	return stats
}

func filterInteractions(interactions []store.Interaction, query, status, provider string) []store.Interaction {
	query = strings.ToLower(strings.TrimSpace(query))
	result := make([]store.Interaction, 0, len(interactions))
	for index := range interactions {
		interaction := &interactions[index]
		if status != "" && status != "all" && !strings.EqualFold(interaction.Status, status) {
			continue
		}
		if provider != "" && provider != "all" && !strings.EqualFold(interaction.Provider, provider) {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{
				interaction.Prompt, interaction.ShortAnswer, interaction.FullAnswer,
				interaction.ErrorMessage, interaction.Provider, interaction.Model,
			}, "\n"))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		result = append(result, *interaction)
	}
	return result
}

func normalizedSection(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case sectionActivity:
		return sectionActivity
	case sectionInference:
		return sectionInference
	default:
		return sectionOverview
	}
}

func normalizedProviderTab(value, active string) string {
	if strings.EqualFold(strings.TrimSpace(value), providerAnthropic) {
		return providerAnthropic
	}
	if strings.EqualFold(strings.TrimSpace(value), providerOpenAI) {
		return providerOpenAI
	}
	if strings.EqualFold(strings.TrimSpace(active), providerAnthropic) {
		return providerAnthropic
	}
	return providerOpenAI
}

func sectionCopy(section string) (title, description string) {
	switch section {
	case sectionActivity:
		return "Activity", "Search and inspect the latest stored Alexa questions and provider outcomes."
	case sectionInference:
		return "Inference", "Choose an explicit provider and model without exposing credentials to the dashboard."
	default:
		return "Overview", "The current operating state of your private Alexa gateway."
	}
}

func sameOrigin(r *http.Request) error {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return nil
	}
	parsed, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(parsed.Host, r.Host) {
		return errors.New("cross-origin dashboard update rejected")
	}
	return nil
}

func newCSRFToken() string {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		panic("generate dashboard form token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

func formatUptime(value time.Duration) string {
	if value < time.Minute {
		return "under a minute"
	}
	if value < time.Hour {
		return strconv.Itoa(int(value/time.Minute)) + " min"
	}
	if value < 24*time.Hour {
		return strconv.Itoa(int(value/time.Hour)) + " hr"
	}
	return strconv.Itoa(int(value/(24*time.Hour))) + " days"
}

func formatMilliseconds(value int64) string {
	if value <= 0 {
		return "—"
	}
	if value < 1000 {
		return strconv.FormatInt(value, 10) + " ms"
	}
	return strconv.FormatFloat(float64(value)/1000, 'f', 1, 64) + " s"
}

func formatRelative(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	if value < time.Minute {
		return "just now"
	}
	if value < time.Hour {
		return strconv.Itoa(int(value/time.Minute)) + " min ago"
	}
	if value < 24*time.Hour {
		return strconv.Itoa(int(value/time.Hour)) + " hr ago"
	}
	return strconv.Itoa(int(value/(24*time.Hour))) + " days ago"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var dashboardTemplate = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"ms":             formatMilliseconds,
	"timestamp":      func(value time.Time) string { return value.UTC().Format("Jan 2, 2006 · 15:04 UTC") },
	"shortTimestamp": func(value time.Time) string { return value.UTC().Format("Jan 2 · 15:04") },
	"lower":          strings.ToLower,
}).Parse(dashboardTemplateSource))

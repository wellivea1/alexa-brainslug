// Package web serves the local development dashboard.
//
// This is a debugging surface, not a product surface: voice is the only
// intended interface. It has no authentication and it renders every prompt
// and answer the gateway has handled, so it must never be routed publicly.
// See docs/security.md.
package web

import (
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/store"
)

// defaultLimit is how many interactions are shown when none is requested.
const defaultLimit = 25

// Store is the slice of the history store the dashboard needs.
type Store interface {
	ListRecent(ctx context.Context, limit int) ([]store.Interaction, error)
}

// Dashboard renders recent interactions as HTML and JSON.
type Dashboard struct {
	store Store
}

// NewDashboard builds a dashboard over the given history store.
func NewDashboard(history Store) *Dashboard {
	return &Dashboard{store: history}
}

// HandleIndex renders the HTML dashboard.
//
// Stored text reaches the page through html/template, which escapes it. That
// matters more than it looks: prompts and answers are attacker-influenced
// text, since anything the user says to Alexa ends up here.
func (d *Dashboard) HandleIndex(w http.ResponseWriter, r *http.Request) {
	interactions, err := d.store.ListRecent(r.Context(), defaultLimit)
	if err != nil {
		slog.ErrorContext(r.Context(), "load dashboard history", "error", err)
		http.Error(w, "could not load history", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dashboardTemplate.Execute(w, map[string]any{
		"Interactions": interactions,
		"GeneratedAt":  time.Now().UTC(),
	}); err != nil {
		// Output has already started, so this cannot become an error page.
		slog.ErrorContext(r.Context(), "render dashboard", "error", err)
	}
}

// HandleHistory serves recent interactions as JSON.
//
// An unparseable limit falls back to the default rather than erroring: this
// is a debugging endpoint, and the store clamps the value regardless.
func (d *Dashboard) HandleHistory(w http.ResponseWriter, r *http.Request) {
	limit := defaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}

	interactions, err := d.store.ListRecent(r.Context(), limit)
	if err != nil {
		slog.ErrorContext(r.Context(), "load history", "error", err)
		http.Error(w, "could not load history", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"interactions": interactions}); err != nil {
		slog.ErrorContext(r.Context(), "encode history", "error", err)
	}
}

var dashboardTemplate = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"ms": func(value int64) string {
		if value <= 0 {
			return "-"
		}
		return strconv.FormatInt(value, 10) + " ms"
	},
}).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Alexa Brainslug</title>
  <style>
    :root { color-scheme: light dark; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    body { margin: 0; background: Canvas; color: CanvasText; }
    header { border-bottom: 1px solid color-mix(in srgb, CanvasText 16%, transparent); padding: 20px 24px 16px; }
    main { max-width: 1160px; margin: 0 auto; padding: 20px 24px 48px; }
    h1 { font-size: 24px; line-height: 1.2; margin: 0 0 6px; letter-spacing: 0; }
    p { margin: 0; color: color-mix(in srgb, CanvasText 68%, transparent); }
    table { border-collapse: collapse; width: 100%; margin-top: 18px; table-layout: fixed; }
    th, td { border-bottom: 1px solid color-mix(in srgb, CanvasText 14%, transparent); padding: 10px 8px; text-align: left; vertical-align: top; font-size: 14px; overflow-wrap: anywhere; }
    th { font-size: 12px; text-transform: uppercase; color: color-mix(in srgb, CanvasText 58%, transparent); }
    .time { width: 170px; }
    .meta { width: 170px; }
    .latency { width: 88px; }
    .status { width: 82px; }
    .answer { min-width: 240px; }
    details { margin-top: 8px; }
    summary { cursor: pointer; color: color-mix(in srgb, CanvasText 70%, transparent); }
    .empty { padding: 28px 0; color: color-mix(in srgb, CanvasText 62%, transparent); }
    .ok { color: #1f7a4d; }
    .error { color: #b42318; }
    @media (max-width: 820px) {
      main, header { padding-left: 14px; padding-right: 14px; }
      table, thead, tbody, th, td, tr { display: block; }
      thead { display: none; }
      tr { border-bottom: 1px solid color-mix(in srgb, CanvasText 18%, transparent); padding: 10px 0; }
      td { border: 0; padding: 5px 0; }
      td::before { content: attr(data-label); display: block; font-size: 12px; text-transform: uppercase; color: color-mix(in srgb, CanvasText 58%, transparent); margin-bottom: 2px; }
      .time, .meta, .latency, .status { width: auto; }
    }
  </style>
</head>
<body>
  <header>
    <h1>Alexa Brainslug</h1>
    <p>Recent local skill interactions. Do not expose this dashboard publicly without authentication.</p>
  </header>
  <main>
    {{if .Interactions}}
    <table aria-label="Recent interactions">
      <thead>
        <tr>
          <th class="time">Timestamp</th>
          <th>Prompt</th>
          <th class="answer">Answer</th>
          <th class="meta">Provider / model</th>
          <th class="latency">Latency</th>
          <th class="status">Status</th>
        </tr>
      </thead>
      <tbody>
        {{range .Interactions}}
        <tr>
          <td class="time" data-label="Timestamp">{{.CreatedAt.Format "2006-01-02 15:04:05 MST"}}</td>
          <td data-label="Prompt">{{.Prompt}}</td>
          <td class="answer" data-label="Answer">
            <div>{{.ShortAnswer}}</div>
            {{if .FullAnswer}}<details><summary>Full answer</summary><div>{{.FullAnswer}}</div></details>{{end}}
            {{if .ErrorMessage}}<details><summary>Error</summary><div>{{.ErrorMessage}}</div></details>{{end}}
          </td>
          <td class="meta" data-label="Provider / model">{{.Provider}}<br>{{.Model}}</td>
          <td class="latency" data-label="Latency">{{ms .LatencyMS}}</td>
          <td class="status {{.Status}}" data-label="Status">{{.Status}}</td>
        </tr>
        {{end}}
      </tbody>
    </table>
    {{else}}
    <div class="empty">No interactions have been stored yet.</div>
    {{end}}
  </main>
</body>
</html>`))

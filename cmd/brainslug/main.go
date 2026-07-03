package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"alexa-brainslug/internal/alexa"
	"alexa-brainslug/internal/config"
	"alexa-brainslug/internal/providers"
	"alexa-brainslug/internal/security"
	"alexa-brainslug/internal/store"
	"alexa-brainslug/internal/web"
)

const maxRequestBodyBytes int64 = 1 << 20

func main() {
	if err := config.LoadDotEnv(".env"); err != nil {
		slog.Warn("could not load .env", "error", err)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	ctx := context.Background()
	db, err := store.Open(ctx, cfg.SQLitePath)
	if err != nil {
		slog.Error("open sqlite store", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	provider, err := providers.NewConfigured(cfg, nil)
	if err != nil {
		slog.Error("configure provider", "error", err)
		os.Exit(1)
	}
	alexaHandler := alexa.NewHandler(cfg, provider, db)
	dashboard := web.NewDashboard(db)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler(cfg, provider.Name()))
	mux.Handle("/alexa", alexaHandler)
	mux.HandleFunc("/history", dashboard.HandleHistory)
	mux.HandleFunc("/", dashboard.HandleIndex)

	server := &http.Server{
		Addr:              cfg.ServerAddr,
		Handler:           requestLogMiddleware(security.Headers(cfg.PublicBaseURL, security.LimitRequestBody(maxRequestBodyBytes, mux))),
		ReadHeaderTimeout: 5 * time.Second,
	}

	slog.Info("starting Alexa Brainslug", "addr", cfg.ServerAddr, "provider", cfg.Provider, "model", cfg.SelectedModel(), "verify_alexa", cfg.AlexaVerifyRequests)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func healthHandler(cfg config.Config, providerName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status":                     "ok",
			"service":                    "alexa-brainslug",
			"provider":                   providerName,
			"model":                      cfg.SelectedModel(),
			"alexa_request_verification": cfg.AlexaVerifyRequests,
			"history_enabled":            cfg.SaveHistory,
			"public_base_url_configured": strings.TrimSpace(cfg.PublicBaseURL) != "",
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func requestLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		slog.Debug("request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr, "duration", time.Since(started))
	})
}

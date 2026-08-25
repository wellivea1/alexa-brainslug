// Command brainslug runs the Alexa Brainslug gateway: an HTTP service that
// answers Alexa custom skill requests by calling a configured AI provider.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/alexa"
	"github.com/wellivea1/alexa-brainslug/internal/app"
	"github.com/wellivea1/alexa-brainslug/internal/config"
	"github.com/wellivea1/alexa-brainslug/internal/providers"
	"github.com/wellivea1/alexa-brainslug/internal/security"
	"github.com/wellivea1/alexa-brainslug/internal/store"
	"github.com/wellivea1/alexa-brainslug/internal/web"
)

// Server timeouts.
//
// ReadHeaderTimeout bounds how long a client may dawdle over headers, which
// is the slowloris defence. WriteTimeout has to exceed the provider timeout
// or the gateway would cut off its own in-flight answers, so it is derived
// from the configured provider timeout rather than fixed.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	idleTimeout       = 60 * time.Second
	writeTimeoutSlack = 10 * time.Second

	// shutdownTimeout bounds how long in-flight requests get to finish after
	// a signal. Alexa gives up at 8 seconds, so anything longer than that is
	// only for the SQLite write that follows.
	shutdownTimeout = 15 * time.Second

	// maxRequestBodyBytes caps request bodies at the server boundary.
	maxRequestBodyBytes int64 = 1 << 20
)

func main() {
	if err := run(); err != nil {
		slog.Error("brainslug exited", "error", err)
		os.Exit(1)
	}
}

// run owns the process lifecycle and returns an error instead of exiting, so
// that every deferred cleanup, including the database handle, actually runs.
// os.Exit in the middle of startup skips defers; that was a real leak here.
func run() error {
	if err := config.LoadDotEnv(".env"); err != nil {
		// Not fatal: the environment may already carry everything needed.
		slog.Warn("could not load .env", "error", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	})))

	// Signals cancel this context, which unblocks the wait below.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.SQLitePath)
	if err != nil {
		return fmt.Errorf("open sqlite store: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("close sqlite store", "error", err)
		}
	}()

	provider, err := providers.NewConfigured(cfg, nil)
	if err != nil {
		return fmt.Errorf("configure provider: %w", err)
	}

	server := &http.Server{
		Addr:              cfg.ServerAddr,
		Handler:           buildHandler(cfg, provider, db),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      cfg.SelectedTimeout() + writeTimeoutSlack,
		IdleTimeout:       idleTimeout,
	}

	errs := make(chan error, 1)
	go func() {
		slog.Info("starting Alexa Brainslug",
			"addr", cfg.ServerAddr,
			"provider", cfg.Provider,
			"model", cfg.SelectedModel(),
			"verify_alexa", cfg.AlexaVerifyRequests,
			"history", cfg.SaveHistory,
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return fmt.Errorf("server failed: %w", err)
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining")
	}

	// A fresh context: the signal already cancelled the one above, and
	// Shutdown needs a live deadline to drain against.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	slog.Info("stopped cleanly")
	return nil
}

// buildHandler assembles the route table and the middleware chain.
//
// Middleware order matters: the body limit runs before anything reads a body,
// and request logging wraps everything so that rejected requests are logged
// too.
func buildHandler(cfg config.Config, provider providers.Provider, db *store.Store) http.Handler {
	application := app.NewService(app.Options{
		Model:           cfg.SelectedModel(),
		MaxOutputTokens: cfg.SelectedMaxOutputTokens(),
		SaveHistory:     cfg.SaveHistory,
		Secrets:         cfg.SecretValues(),
	}, providers.AsAppProvider(provider), store.AsAppRepository(db))

	alexaHandler := alexa.NewHandler(alexa.Options{
		Verification: alexa.VerificationConfig{
			VerifyRequests: cfg.AlexaVerifyRequests,
			AllowedSkillID: cfg.AlexaAllowedSkillID,
		},
	}, application)

	dashboard := web.NewDashboard(db)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler(&cfg, provider.Name()))
	mux.Handle("POST /alexa", alexaHandler)
	mux.HandleFunc("GET /history", dashboard.HandleHistory)
	mux.HandleFunc("GET /{$}", dashboard.HandleIndex)

	return security.RequestLogger(
		security.Headers(cfg.PublicBaseURL,
			security.LimitRequestBody(maxRequestBodyBytes, mux)))
}

// healthHandler reports liveness and the effective configuration.
//
// It names the provider and model deliberately: an operator has to be able to
// confirm which model is answering without reading the process environment.
// It never reports a credential.
func healthHandler(cfg *config.Config, providerName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(r.Context(), w, http.StatusOK, map[string]any{
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

func writeJSON(ctx context.Context, w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.ErrorContext(ctx, "encode JSON response", "error", err)
	}
}

// parseLogLevel maps LOG_LEVEL to a slog level, defaulting to info.
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

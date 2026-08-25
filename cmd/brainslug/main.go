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
	"github.com/wellivea1/alexa-brainslug/internal/inference"
	"github.com/wellivea1/alexa-brainslug/internal/providers"
	"github.com/wellivea1/alexa-brainslug/internal/security"
	"github.com/wellivea1/alexa-brainslug/internal/store"
	"github.com/wellivea1/alexa-brainslug/internal/web"
)

// Server timeouts.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	idleTimeout       = 60 * time.Second
	writeTimeoutSlack = 10 * time.Second
	adminWriteTimeout = 15 * time.Second

	// shutdownTimeout bounds the combined drain of both listeners.
	shutdownTimeout = 15 * time.Second

	// maxRequestBodyBytes caps request bodies at the server boundary.
	maxRequestBodyBytes int64 = 1 << 20
)

type namedServer struct {
	name   string
	server *http.Server
}

func main() {
	if err := run(); err != nil {
		slog.Error("brainslug exited", "error", err)
		os.Exit(1)
	}
}

// run owns the process lifecycle and returns an error instead of exiting, so
// that every deferred cleanup, including the database handle, actually runs.
func run() error {
	startedAt := time.Now().UTC()
	if err := config.LoadDotEnv(".env"); err != nil {
		// Not fatal: the environment may already carry everything needed.
		slog.Warn("could not load .env", "error", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	adminAddr, err := config.LoadAdminServerAddr(cfg.ServerAddr)
	if err != nil {
		return fmt.Errorf("invalid listener configuration: %w", err)
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

	provider, err := providers.NewConfigured(&cfg, nil)
	if err != nil {
		return fmt.Errorf("configure provider: %w", err)
	}

	application := app.NewService(app.Options{
		Model:           cfg.SelectedModel(),
		MaxOutputTokens: cfg.SelectedMaxOutputTokens(),
		SaveHistory:     cfg.SaveHistory,
		Secrets:         cfg.SecretValues(),
	}, providers.AsAppProvider(provider), store.AsAppRepository(db))
	providerManager, err := inference.NewManager(&cfg, application)
	if err != nil {
		return fmt.Errorf("configure inference manager: %w", err)
	}

	alexaHandler := alexa.NewHandler(alexa.Options{
		Verification: alexa.VerificationConfig{
			VerifyRequests: cfg.AlexaVerifyRequests,
			AllowedSkillID: cfg.AlexaAllowedSkillID,
		},
	}, application)

	servers := []namedServer{
		{
			name: "ask",
			server: &http.Server{
				Addr:              cfg.ServerAddr,
				Handler:           buildPublicHandler(cfg.PublicBaseURL, alexaHandler),
				ReadHeaderTimeout: readHeaderTimeout,
				ReadTimeout:       readTimeout,
				WriteTimeout:      cfg.SelectedTimeout() + writeTimeoutSlack,
				IdleTimeout:       idleTimeout,
			},
		},
		{
			name: "admin",
			server: &http.Server{
				Addr:              adminAddr,
				Handler:           buildAdminHandler(&cfg, providerManager, db, startedAt),
				ReadHeaderTimeout: readHeaderTimeout,
				ReadTimeout:       readTimeout,
				WriteTimeout:      adminWriteTimeout,
				IdleTimeout:       idleTimeout,
			},
		},
	}

	errs := make(chan error, len(servers))
	for _, server := range servers {
		go serve(server, errs)
	}

	var serveErr error
	select {
	case serveErr = <-errs:
		slog.Error("server failed, shutting down", "error", serveErr)
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining")
	}

	// A fresh context: the signal may already have canceled the process
	// context, and Shutdown needs a live deadline to drain against.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var shutdownErrs []error
	for _, server := range servers {
		if err := server.server.Shutdown(shutdownCtx); err != nil {
			shutdownErrs = append(shutdownErrs, fmt.Errorf("shutdown %s server: %w", server.name, err))
		}
	}
	if err := errors.Join(shutdownErrs...); err != nil {
		return err
	}
	if serveErr != nil {
		return serveErr
	}

	slog.Info("stopped cleanly")
	return nil
}

func serve(server namedServer, errs chan<- error) {
	slog.Info("starting Alexa Brainslug listener", "listener", server.name, "addr", server.server.Addr)
	if err := server.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errs <- fmt.Errorf("%s server: %w", server.name, err)
	}
}

// buildPublicHandler registers only the authenticated ASK ingress surface.
func buildPublicHandler(publicBaseURL string, alexaHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /alexa", alexaHandler)

	return security.RequestLogger(
		security.Headers(publicBaseURL,
			security.LimitRequestBody(maxRequestBodyBytes, mux)))
}

// buildAdminHandler registers local administrative routes on the separate
// listener. The caller controls binding; the default is host loopback.
func buildAdminHandler(cfg *config.Config, controller web.InferenceController, db *store.Store, startedAt time.Time) http.Handler {
	dashboard := web.NewDashboard(db, web.Options{
		Inference:               controller,
		StartedAt:               startedAt,
		AlexaVerifyRequests:     cfg.AlexaVerifyRequests,
		HistoryEnabled:          cfg.SaveHistory,
		PublicBaseURLConfigured: strings.TrimSpace(cfg.PublicBaseURL) != "",
		SkillIDConfigured:       strings.TrimSpace(cfg.AlexaAllowedSkillID) != "",
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler(cfg, controller))
	mux.HandleFunc("GET /history", dashboard.HandleHistory)
	mux.HandleFunc("GET /assets/dashboard.css", dashboard.HandleStyles)
	mux.HandleFunc("POST /settings/provider", dashboard.HandleProviderUpdate)
	mux.HandleFunc("GET /{$}", dashboard.HandleIndex)

	return security.RequestLogger(
		security.Headers("",
			security.LimitRequestBody(maxRequestBodyBytes, mux)))
}

// healthHandler reports liveness and the effective configuration.
//
// It names the provider and model deliberately: an operator has to be able to
// confirm which model is answering without reading the process environment.
// It never reports a credential.
func healthHandler(cfg *config.Config, controller interface{ Snapshot() inference.Snapshot }) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot := controller.Snapshot()
		active := snapshot.OpenAI
		if snapshot.ActiveProvider == config.ProviderAnthropic {
			active = snapshot.Anthropic
		}
		writeJSON(r.Context(), w, http.StatusOK, map[string]any{
			"status":                     "ok",
			"service":                    "alexa-brainslug",
			"provider":                   snapshot.ActiveProvider,
			"model":                      active.Model,
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

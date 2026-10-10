// Command brainslug-codex runs the independent LAN task controller or local
// Codex worker. It never mounts remote repositories or reuses gateway credentials.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/codex"
	"github.com/wellivea1/alexa-brainslug/internal/config"
	"github.com/wellivea1/alexa-brainslug/internal/control"
	"github.com/wellivea1/alexa-brainslug/internal/controlapi"
	"github.com/wellivea1/alexa-brainslug/internal/controlstore"
	"github.com/wellivea1/alexa-brainslug/internal/providers"
	"github.com/wellivea1/alexa-brainslug/internal/routing"
	"github.com/wellivea1/alexa-brainslug/internal/securefile"
)

// Version can be set independently from the gateway with -ldflags.
var Version = "dev"

type controllerConfig struct {
	ListenAddr string              `json:"listen_addr"`
	Database   string              `json:"database"`
	TLS        controlapi.TLSFiles `json:"tls"`
	Registry   controlapi.Registry `json:"registry"`
	Router     *routing.Config     `json:"router,omitempty"`
	Answer     *answerConfig       `json:"answer,omitempty"`
}
type answerConfig struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	BaseURL         string `json:"base_url"`
	APIKeyEnv       string `json:"api_key_env"`
	APIKeyFile      string `json:"api_key_file"`
	Format          string `json:"api_format"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
}
type workerConfig struct {
	Database string               `json:"database"`
	TLS      controlapi.TLSFiles  `json:"tls"`
	Worker   codex.WorkerOptions  `json:"worker"`
	Process  codex.ProcessOptions `json:"process"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout)
	stop()
	if err != nil {
		slog.Error("brainslug-codex stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: brainslug-codex controller|worker|probe|init-certs|evaluate-router|version --config path")
	}
	if args[0] == "version" {
		_, err := fmt.Fprintf(out, "brainslug-codex %s protocol %d\n", Version, control.ProtocolVersion)
		return err
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(out)
	path := flags.String("config", "", "explicit controller or worker JSON config")
	directory := flags.String("directory", "", "new private certificate directory")
	serverName := flags.String("server-name", "", "controller TLS DNS name or IP")
	workers := flags.String("workers", "", "comma-separated enrolled host IDs")
	live := flags.Bool("live", false, "explicitly permit local Codex probing or paid router evaluation")
	cases := flags.String("cases", "", "labeled router examples JSON")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return control.ErrInvalid
	}
	switch args[0] {
	case "init-certs":
		identities, err := controlapi.CreatePairing(*directory, *serverName, strings.Split(*workers, ","))
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(identities)
	case "controller":
		return serveController(ctx, *path)
	case "worker":
		return serveWorker(ctx, *path, false, out)
	case "probe":
		if !*live {
			return errors.New("probe requires --live and an explicitly configured disposable worker")
		}
		return serveWorker(ctx, *path, true, out)
	case "evaluate-router":
		if !*live {
			return errors.New("router evaluation requires --live; it can consume the independently configured API budget")
		}
		var cfg controllerConfig
		if err := readConfig(*path, &cfg); err != nil {
			return err
		}
		router, _, err := configureRouter(cfg.Router)
		if err != nil || router == nil {
			return errors.New("dedicated routing profile is required")
		}
		var examples []routing.Example
		if err := readConfig(*cases, &examples); err != nil {
			return err
		}
		if len(examples) == 0 || len(examples) > 100 {
			return control.ErrInvalid
		}
		return json.NewEncoder(out).Encode(routing.Evaluate(ctx, router, examples))
	default:
		return control.ErrInvalid
	}
}

func readConfig(path string, out any) error {
	if path == "" {
		return errors.New("an explicit config path is required")
	}
	// #nosec G304 G703 -- path is an operator CLI flag, never task or HTTP input.
	file, err := os.Open(path)
	if err != nil {
		return errors.New("could not open the explicit configuration file")
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 256*1024+1))
	if err != nil || len(data) > 256*1024 {
		return control.ErrInvalid
	}
	if err := securefile.DecodeJSON(data, out); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	return nil
}

func credential(envName, filePath string) (string, error) {
	if (envName == "") == (filePath == "") {
		return "", errors.New("choose exactly one dedicated API key environment name or file")
	}
	if envName != "" {
		key := strings.TrimSpace(os.Getenv(envName))
		if key == "" {
			return "", errors.New("dedicated API key environment variable is empty")
		}
		return key, nil
	}
	// #nosec G304 -- a credential file is explicit operator configuration.
	file, err := os.Open(filePath)
	if err != nil {
		return "", errors.New("dedicated API key file is unavailable")
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil || len(data) > 16384 || strings.TrimSpace(string(data)) == "" {
		return "", errors.New("dedicated API key file is invalid")
	}
	return strings.TrimSpace(string(data)), nil
}

func configureRouter(cfg *routing.Config) (*routing.Router, string, error) {
	if cfg == nil {
		return nil, "", nil
	}
	key, err := credential(cfg.APIKeyEnv, cfg.APIKeyFile)
	if err != nil {
		return nil, "", err
	}
	router, err := routing.New(*cfg, key, nil)
	return router, key, err
}

type independentAnswerer struct{ provider providers.Provider }

func (a *independentAnswerer) Answer(ctx context.Context, prompt string) (string, error) {
	response, err := a.provider.Ask(ctx, providers.Request{Prompt: prompt})
	return response.FullAnswer, err
}

func configureAnswer(cfg *answerConfig) (control.Answerer, string, error) {
	if cfg == nil {
		return nil, "", nil
	}
	if cfg.Model == "" || cfg.MaxOutputTokens < 16 || cfg.MaxOutputTokens > 128000 || cfg.TimeoutSeconds < 1 || cfg.TimeoutSeconds > 30 {
		return nil, "", control.ErrInvalid
	}
	key, err := credential(cfg.APIKeyEnv, cfg.APIKeyFile)
	if err != nil {
		return nil, "", err
	}
	settings := config.ProviderConfig{APIKey: key, Model: cfg.Model, BaseURL: cfg.BaseURL, MaxOutputTokens: cfg.MaxOutputTokens, Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second, AuthMode: config.OpenAIAuthModeAPIKey, APIFormat: cfg.Format}
	providerCfg := config.Config{Provider: cfg.Provider}
	definition, ok := config.LookupProvider(cfg.Provider)
	if !ok {
		return nil, "", control.ErrInvalid
	}
	*definition.Settings(&providerCfg) = settings
	provider, err := providers.NewConfigured(&providerCfg, nil)
	if err != nil {
		return nil, "", err
	}
	return &independentAnswerer{provider: provider}, key, nil
}

func prepareDatabase(path string) error {
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") {
		return errors.New("a dedicated host-local database path is required")
	}
	return securefile.PrepareDirectory(filepath.Dir(path))
}

func serveController(ctx context.Context, path string) error {
	var cfg controllerConfig
	if err := readConfig(path, &cfg); err != nil {
		return err
	}
	if err := cfg.Registry.Validate(); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil {
		return control.ErrInvalid
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return errors.New("controller must bind a specific loopback or private LAN IP")
	}
	tlsConfig, err := controlapi.LoadTLS(cfg.TLS, true)
	if err != nil {
		return err
	}
	router, routerKey, err := configureRouter(cfg.Router)
	if err != nil {
		return err
	}
	answerer, answerKey, err := configureAnswer(cfg.Answer)
	if err != nil {
		return err
	}
	if cfg.Registry.Policy.AutomaticEnabled && router == nil {
		return control.ErrInvalid
	}
	if err := prepareDatabase(cfg.Database); err != nil {
		return err
	}
	store, err := controlstore.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	policy := cfg.Registry.Policy
	policy.Secrets = []string{routerKey, answerKey}
	hub := controlapi.NewHub(policy.Hosts)
	var routerPort control.Router
	if router != nil {
		routerPort = router
	}
	engine, err := control.NewEngine(ctx, store, policy, hub, routerPort, answerer)
	if err != nil {
		return err
	}
	hub.AttachEngine(engine)
	api := &controlapi.API{Engine: engine, Hub: hub, Identities: cfg.Registry.Identities, RegistryRevision: cfg.Registry.Revision}
	api.SaveRegistry = func(registry controlapi.Registry) error {
		candidate := cfg
		candidate.Registry = registry
		data, err := json.MarshalIndent(candidate, "", "  ")
		if err != nil {
			return err
		}
		if err := securefile.Write(path, append(data, '\n')); err != nil {
			return err
		}
		cfg = candidate
		return nil
	}
	server := &http.Server{Addr: cfg.ListenAddr, Handler: api.Handler(), TLSConfig: tlsConfig, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768}
	processCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errorsCh := make(chan error, 2)
	go func() { errorsCh <- engine.Run(processCtx) }()
	go func() { errorsCh <- server.ListenAndServeTLS("", "") }()
	slog.Info("Codex controller ready", "address", cfg.ListenAddr, "protocol", control.ProtocolVersion, "desktop_placement", "unverified")
	var result error
	select {
	case <-ctx.Done():
	case result = <-errorsCh:
	}
	cancel()
	hub.Close()
	drain, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	_ = server.Shutdown(drain)
	if errors.Is(result, http.ErrServerClosed) || errors.Is(result, context.Canceled) {
		return nil
	}
	return result
}

func serveWorker(ctx context.Context, path string, probe bool, out io.Writer) error {
	var cfg workerConfig
	if err := readConfig(path, &cfg); err != nil {
		return err
	}
	if cfg.Process.ExpectedVersion != cfg.Worker.CodexVersion {
		return errors.New("worker and process must pin the same Codex version")
	}
	tlsConfig, err := controlapi.LoadTLS(cfg.TLS, false)
	if err != nil {
		return err
	}
	runtime, err := codex.NewSupervisor(ctx, cfg.Process)
	if err != nil {
		return err
	}
	defer func() { _ = runtime.Close() }()
	if probe {
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		models, err := runtime.Models(probeCtx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"codex_version": cfg.Worker.CodexVersion, "protocol_version": control.ProtocolVersion, "models": models, "desktop_placement": "unverified", "inference_entitlement": "not_proven_by_catalog"})
	}
	if err := prepareDatabase(cfg.Database); err != nil {
		return err
	}
	ledger, err := codex.OpenLedger(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer func() { _ = ledger.Close() }()
	discoveryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	worker, err := codex.NewWorker(discoveryCtx, ledger, cfg.Worker, runtime)
	cancel()
	if err != nil {
		return err
	}
	slog.Info("Codex worker ready", "host", cfg.Worker.HostID, "version", cfg.Worker.CodexVersion)
	return worker.Run(ctx, tlsConfig)
}

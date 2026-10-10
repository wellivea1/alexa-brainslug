package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/control"
	"github.com/wellivea1/alexa-brainslug/internal/controlapi"
)

func TestCLIReportsProtocolAndRequiresExplicitLiveOptIn(t *testing.T) {
	var output bytes.Buffer
	if err := run(t.Context(), []string{"version"}, &output); err != nil || !strings.Contains(output.String(), "protocol 1") {
		t.Fatal("standalone version command failed", err)
	}
	for _, mode := range []string{"probe", "evaluate-router"} {
		if err := run(t.Context(), []string{mode, "--config", "missing.json"}, &output); err == nil || !strings.Contains(err.Error(), "--live") {
			t.Fatal("live operation did not require explicit opt-in", mode, err)
		}
	}
}

func TestControllerModeStartsAndDrainsWithoutAnAgentAccount(t *testing.T) {
	directory := t.TempDir()
	pki := filepath.Join(directory, "pki")
	identities, err := controlapi.CreatePairing(pki, "127.0.0.1", []string{"worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	pair := control.ModelPair{Model: "fake-model", Effort: "medium"}
	cfg := controllerConfig{ListenAddr: "127.0.0.1:0", Database: filepath.Join(directory, "data", "controller.db"), TLS: controlapi.TLSFiles{Certificate: filepath.Join(pki, "controller.pem"), Key: filepath.Join(pki, "controller.key"), CA: filepath.Join(pki, "ca.pem")}, Registry: controlapi.Registry{Identities: identities, Policy: &control.Options{PolicyVersion: "cli-test", QueueLimit: 2, Concurrency: 1, RoutingSeconds: 1, RetentionDays: 1, Hosts: []control.Host{{ID: "worker-a", CodexVersion: "fake-version", Models: []control.ModelPair{pair}}}}}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "controller.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	if err := run(ctx, []string{"controller", "--config", path}, &bytes.Buffer{}); err != nil {
		t.Fatal("independent controller lifecycle failed", err)
	}
	if _, err := os.Stat(cfg.Database); err != nil {
		t.Fatal("controller did not create its independent database")
	}
	cfg.ListenAddr = "0.0.0.0:9091"
	data, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), []string{"controller", "--config", path}, &bytes.Buffer{}); err == nil {
		t.Fatal("wildcard controller listener was accepted")
	}
}

package config

import (
	"testing"
)

func TestCodexConnectionPrecedenceIsIndependentFromInference(t *testing.T) {
	base := CodexConfig{Mode: "manual", URL: "https://controller.lan:9091", Certificate: "original.pem", Key: "original.key", CA: "ca.pem"}
	cfg := Config{Codex: base, Provider: ProviderOpenAI}
	cfg.environment = &environmentBaseline{config: cfg}
	cfg.Codex.Enabled = true
	cfg.Codex.Mode = "shadow"
	settings := RuntimeSettingsFromConfig(&cfg)
	if settings.Codex == nil || settings.Codex.Mode == nil || settings.Codex.Enabled == nil || settings.Codex.URL != nil || settings.Provider != nil {
		t.Fatal("Codex settings overwrote independent environment values")
	}
	reloaded := Config{Codex: base, Provider: ProviderOpenAI}
	reloaded.Codex.URL = "https://new-controller.lan:9091"
	if err := reloaded.applyRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	if reloaded.Codex.Mode != "shadow" || !reloaded.Codex.Enabled || reloaded.Codex.URL != "https://new-controller.lan:9091" || reloaded.Provider != ProviderOpenAI {
		t.Fatal("controller reload changed inference or absent settings")
	}
	for _, url := range []string{"http://controller.lan", "https://user:password@controller.lan", "https://controller.lan/path"} {
		invalid := reloaded.Codex
		invalid.URL = url
		if err := invalid.Validate(); err == nil {
			t.Fatalf("unsafe controller origin accepted: %s", url)
		}
	}
}

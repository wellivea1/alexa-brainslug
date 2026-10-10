package config

import (
	"errors"
	"net/url"
	"strings"
)

// CodexConfig is the gateway's independent controller connection. It contains
// certificate paths, never worker accounts, repository roots, or private keys.
type CodexConfig struct {
	Enabled     bool   `json:"enabled"`
	Mode        string `json:"mode"`
	URL         string `json:"url"`
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
	CA          string `json:"ca"`
	ServerName  string `json:"server_name"`
}

// Validate checks explicit gateway routing and verified HTTPS connection settings.
func (c CodexConfig) Validate() error {
	if c.Mode != "" && c.Mode != "manual" && c.Mode != "shadow" && c.Mode != "automatic" {
		return errors.New("CODEX_ROUTING_MODE must be manual, shadow, or automatic")
	}
	if !c.Enabled {
		return nil
	}
	parsed, err := url.Parse(c.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return errors.New("CODEX_CONTROLLER_URL must be an HTTPS origin")
	}
	if c.Certificate == "" || c.Key == "" || c.CA == "" {
		return errors.New("codex control requires its separate enrolled certificate, key, and CA files")
	}
	return nil
}

// RuntimeCodexSettings preserves per-field environment precedence.
type RuntimeCodexSettings struct {
	Enabled     *bool   `json:"enabled,omitempty"`
	Mode        *string `json:"mode,omitempty"`
	URL         *string `json:"url,omitempty"`
	Certificate *string `json:"certificate,omitempty"`
	Key         *string `json:"key,omitempty"`
	CA          *string `json:"ca,omitempty"`
	ServerName  *string `json:"server_name,omitempty"`
}

func codexChanges(current, base CodexConfig) *RuntimeCodexSettings {
	saved := &RuntimeCodexSettings{Enabled: changed(current.Enabled, base.Enabled), Mode: changed(current.Mode, base.Mode), URL: changed(current.URL, base.URL), Certificate: changed(current.Certificate, base.Certificate), Key: changed(current.Key, base.Key), CA: changed(current.CA, base.CA), ServerName: changed(current.ServerName, base.ServerName)}
	if *saved == (RuntimeCodexSettings{}) {
		return nil
	}
	return saved
}

func (c *CodexConfig) apply(saved *RuntimeCodexSettings) {
	if saved == nil {
		return
	}
	if saved.Enabled != nil {
		c.Enabled = *saved.Enabled
	}
	pairs := []struct {
		target *string
		value  *string
	}{{&c.Mode, saved.Mode}, {&c.URL, saved.URL}, {&c.Certificate, saved.Certificate}, {&c.Key, saved.Key}, {&c.CA, saved.CA}, {&c.ServerName, saved.ServerName}}
	for _, pair := range pairs {
		if pair.value != nil {
			*pair.target = strings.TrimSpace(*pair.value)
		}
	}
}

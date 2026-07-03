package security

import (
	"strings"
	"testing"
)

func TestRedactStringRemovesSecretPatterns(t *testing.T) {
	input := "Authorization: Bearer abc123 api_key=secret-key-value token=secret-token-value password=hunter2 sk-proj-abcdefghijklmnopqrstuvwxyz"

	got := RedactString(input)

	for _, leaked := range []string{"abc123", "secret-key-value", "secret-token-value", "hunter2", "sk-proj-abcdefghijklmnopqrstuvwxyz"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted string leaked %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redacted marker in %q", got)
	}
}

func TestRedactStringRemovesConfiguredSecret(t *testing.T) {
	got := RedactString("provider echoed custom-secret-value", "custom-secret-value")
	if strings.Contains(got, "custom-secret-value") {
		t.Fatalf("configured secret leaked: %s", got)
	}
}

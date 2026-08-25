package providers

import (
	"strings"

	"github.com/wellivea1/alexa-brainslug/internal/security"
)

// redact removes this endpoint's credential from provider-controlled text
// before the text becomes a diagnostic or persisted error.
func (e endpoint) redact(message string) string {
	return security.RedactString(strings.TrimSpace(message), e.apiKey)
}

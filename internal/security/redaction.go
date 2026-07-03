package security

import (
	"regexp"
	"strings"
)

type redactionPattern struct {
	rx          *regexp.Regexp
	replacement string
}

var redactionPatterns = []redactionPattern{
	{regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)[^\s]+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)(api[_-]?key\s*[:=]\s*)[^\s"']+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)(token\s*[:=]\s*)[^\s"']+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)(password\s*[:=]\s*)[^\s"']+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)(cookie\s*[:=]\s*)[^\r\n\s"']+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)(session[_-]?id\s*[:=]\s*)[^\s"']+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`sk-[A-Za-z0-9_-]{12,}`), `[REDACTED]`},
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`), `[REDACTED]`},
}

func RedactString(input string, extraSecrets ...string) string {
	out := input
	for _, pattern := range redactionPatterns {
		out = pattern.rx.ReplaceAllString(out, pattern.replacement)
	}
	for _, secret := range extraSecrets {
		secret = strings.TrimSpace(secret)
		if len(secret) < 6 {
			continue
		}
		out = strings.ReplaceAll(out, secret, "[REDACTED]")
	}
	return out
}

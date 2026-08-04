package security

import (
	"regexp"
	"strings"
)

// redactedPlaceholder replaces anything that looks like a credential.
const redactedPlaceholder = "[REDACTED]"

// keptPrefix preserves the label in front of a redacted value, so a redacted
// message still says which kind of credential appeared.
const keptPrefix = "${1}" + redactedPlaceholder

// minSecretLength is the shortest value redacted by exact match. Shorter
// values are too likely to occur as ordinary substrings, and replacing them
// would corrupt the message without protecting anything.
const minSecretLength = 6

type redactionPattern struct {
	rx          *regexp.Regexp
	replacement string
}

// redactionPatterns catch credentials by shape, for the case where a value is
// not in the configured secret list: a provider echoing back a header, or an
// error quoting a request. This is the backstop, not the primary defence.
var redactionPatterns = []redactionPattern{
	{regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)\S+`), keptPrefix},
	{regexp.MustCompile(`(?i)(api[_-]?key\s*[:=]\s*)[^\s"']+`), keptPrefix},
	{regexp.MustCompile(`(?i)(token\s*[:=]\s*)[^\s"']+`), keptPrefix},
	{regexp.MustCompile(`(?i)(password\s*[:=]\s*)[^\s"']+`), keptPrefix},
	{regexp.MustCompile(`(?i)(cookie\s*[:=]\s*)[^\s"']+`), keptPrefix},
	{regexp.MustCompile(`(?i)(session[_-]?id\s*[:=]\s*)[^\s"']+`), keptPrefix},
	{regexp.MustCompile(`sk-[A-Za-z0-9_-]{12,}`), redactedPlaceholder},
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`), redactedPlaceholder},
}

// RedactString removes credentials from text before it is logged or stored.
//
// It applies the shape-based patterns first, then removes any exact secret in
// extraSecrets, which is the reliable path: the caller passes the credentials
// actually in use, so a key is removed whatever shape the provider wrapped it
// in. Blank and very short entries are skipped.
func RedactString(input string, extraSecrets ...string) string {
	out := input
	for _, pattern := range redactionPatterns {
		out = pattern.rx.ReplaceAllString(out, pattern.replacement)
	}
	for _, secret := range extraSecrets {
		secret = strings.TrimSpace(secret)
		if len(secret) < minSecretLength {
			continue
		}
		out = strings.ReplaceAll(out, secret, redactedPlaceholder)
	}
	return out
}

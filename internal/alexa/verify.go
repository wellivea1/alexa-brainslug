package alexa

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrVerificationNotImplemented is returned while signature verification is
// enabled but unbuilt. It exists so the gap is a loud, testable failure
// rather than a silently accepted request.
var ErrVerificationNotImplemented = errors.New("alexa request signature verification is enabled but not implemented yet")

// VerificationConfig controls how incoming requests are authenticated.
type VerificationConfig struct {
	// VerifyRequests enables signature verification. It defaults to true and
	// currently rejects every request, because the verification itself is not
	// implemented. That is deliberate: the failure mode of an unverified
	// public endpoint is an open proxy to a paid API key.
	VerifyRequests bool

	// AllowedSkillID, when set, restricts the endpoint to one skill. It is
	// checked regardless of VerifyRequests, so it offers a weak barrier
	// during local testing. It is not authentication: a skill ID is not a
	// secret and is trivially forged.
	AllowedSkillID string
}

// VerifyRequest authenticates an inbound Alexa request.
//
// A complete implementation must validate, in this order:
//
//   - the SignatureCertChainUrl header: https scheme, host s3.amazonaws.com,
//     path prefixed /echo.api/, port 443 if present;
//   - the downloaded certificate chain, up to a trusted root, including the
//     Not Before and Not After dates;
//   - the presence of echo-api.amazon.com in the certificate's SANs;
//   - the Signature-256 header against a SHA-256 hash of the raw body;
//   - the request timestamp, within 150 seconds of now.
//
// r and body are already threaded through for that work: the signature covers
// the exact bytes received, so it cannot be checked against a re-encoded
// envelope.
func VerifyRequest(r *http.Request, body []byte, envelope *RequestEnvelope, cfg VerificationConfig) error {
	_, _ = r, body // Used once signature verification lands.

	if allowed := strings.TrimSpace(cfg.AllowedSkillID); allowed != "" {
		if got := envelope.ApplicationID(); got != allowed {
			return fmt.Errorf("alexa skill ID %q is not allowed", got)
		}
	}

	if !cfg.VerifyRequests {
		return nil
	}
	return ErrVerificationNotImplemented
}

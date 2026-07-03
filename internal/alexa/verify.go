package alexa

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var ErrVerificationNotImplemented = errors.New("Alexa request signature verification is enabled but not implemented yet")

type VerificationConfig struct {
	VerifyRequests bool
	AllowedSkillID string
}

func VerifyRequest(r *http.Request, body []byte, envelope RequestEnvelope, cfg VerificationConfig) error {
	_ = r
	_ = body

	if strings.TrimSpace(cfg.AllowedSkillID) != "" && envelope.ApplicationID() != cfg.AllowedSkillID {
		return fmt.Errorf("Alexa skill ID %q is not allowed", envelope.ApplicationID())
	}
	if !cfg.VerifyRequests {
		return nil
	}

	// Full verification needs Amazon certificate chain validation, timestamp
	// checks, and request-body signature verification. Until that is complete,
	// production verification intentionally fails closed.
	return ErrVerificationNotImplemented
}

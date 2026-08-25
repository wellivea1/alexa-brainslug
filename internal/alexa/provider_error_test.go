package alexa

import (
	"testing"

	"github.com/wellivea1/alexa-brainslug/internal/app"
)

func TestSpeechForOutputLimitError(t *testing.T) {
	if got := speechForApplicationError(app.ErrProviderOutputLimit); got != speechProviderNoText {
		t.Errorf("speechForApplicationError() = %q, want %q", got, speechProviderNoText)
	}
}

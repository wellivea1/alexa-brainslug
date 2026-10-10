package alexa

import (
	"strings"
	"unicode"
)

// Alexa request types handled by this skill. Anything else falls through to a
// clarifying prompt rather than an error, because the user hears the result.
const (
	requestTypeLaunch       = "LaunchRequest"
	requestTypeIntent       = "IntentRequest"
	requestTypeSessionEnded = "SessionEndedRequest"
)

// dialogStateCompleted marks a dialog whose required slots are all filled.
const dialogStateCompleted = "COMPLETED"

// slotQuery is the free-form question slot on AskGPTIntent. It must match the
// slot name in skill/interaction-model.json.
const slotQuery = "Query"

// Custom intents. These must match the intent names in
// skill/interaction-model.json; a rename there is a breaking change here.
const (
	// IntentAskGPT carries the user's question in the Query slot.
	IntentAskGPT = "AskGPTIntent"

	// IntentLastResponse replays the most recent stored answer.
	IntentLastResponse = "LastResponseIntent"
)

// Built-in Amazon intents. Handling stop, cancel, help, and fallback is a
// certification requirement as well as good behaviour.
const (
	IntentHelp     = "AMAZON.HelpIntent"
	IntentStop     = "AMAZON.StopIntent"
	IntentCancel   = "AMAZON.CancelIntent"
	IntentFallback = "AMAZON.FallbackIntent"
	IntentRepeat   = "AMAZON.RepeatIntent"
)

// lastResponsePhrases are retrieval requests that Alexa can capture as a
// free-form Query instead of matching LastResponseIntent, most often when the
// user answers "What should I ask?" with "last response". Nobody sends these
// to a model as a question, so they are answered from history instead.
var lastResponsePhrases = map[string]bool{
	"last response":            true,
	"the last response":        true,
	"last answer":              true,
	"the last answer":          true,
	"what was the last answer": true,
	"read the last answer":     true,
	"repeat the last answer":   true,
	"previous answer":          true,
	"the previous answer":      true,
}

// isLastResponseRequest reports whether a captured Query is one of the
// retrieval phrases above, ignoring case and punctuation.
func isLastResponseRequest(query string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, query)
	return lastResponsePhrases[strings.Join(strings.Fields(normalized), " ")]
}

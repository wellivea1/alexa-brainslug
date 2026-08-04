package alexa

// Alexa request types handled by this skill. Anything else falls through to a
// clarifying prompt rather than an error, because the user hears the result.
const (
	requestTypeLaunch       = "LaunchRequest"
	requestTypeIntent       = "IntentRequest"
	requestTypeSessionEnded = "SessionEndedRequest"
)

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

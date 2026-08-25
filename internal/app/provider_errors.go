package app

import "errors"

// ErrProviderOutputLimit means the provider exhausted the configured output
// budget, which may include both reasoning and visible answer tokens.
var ErrProviderOutputLimit = errors.New("provider output token limit was exhausted")

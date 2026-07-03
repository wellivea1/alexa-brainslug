package providers

import (
	"context"
	"time"
)

type Provider interface {
	Name() string
	Ask(ctx context.Context, req Request) (Response, error)
}

type Request struct {
	UserID          string
	Prompt          string
	Model           string
	MaxOutputTokens int
	SystemPrompt    string
	PriorContext    []Message
}

type Message struct {
	Role    string
	Content string
}

type Response struct {
	ShortAnswer  string
	FullAnswer   string
	Model        string
	ProviderName string
	Latency      time.Duration
	RawError     string
}

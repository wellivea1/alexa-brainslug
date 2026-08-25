package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var testPrincipal = Principal{Namespace: "alexa", Subject: "user-1"}

type fakeProvider struct {
	name     string
	request  ProviderRequest
	response ProviderResponse
	err      error
}

func (p *fakeProvider) Name() string {
	if p.name == "" {
		return "fake"
	}
	return p.name
}

func (p *fakeProvider) Ask(_ context.Context, req ProviderRequest) (ProviderResponse, error) {
	p.request = req
	return p.response, p.err
}

type fakeRepository struct {
	saved   []Interaction
	latest  *Interaction
	saveErr error
	loadErr error
}

func (r *fakeRepository) SaveInteraction(_ context.Context, interaction Interaction) (int64, error) {
	if r.saveErr != nil {
		return 0, r.saveErr
	}
	r.saved = append(r.saved, interaction)
	return int64(len(r.saved)), nil
}

func (r *fakeRepository) GetLatestResponse(_ context.Context, _ Principal) (*Interaction, error) {
	if r.loadErr != nil {
		return nil, r.loadErr
	}
	if r.latest == nil {
		return nil, ErrRepositoryNotFound
	}
	return r.latest, nil
}

func TestServiceAsk(t *testing.T) {
	provider := &fakeProvider{response: ProviderResponse{
		ShortAnswer: "  short answer  ",
		FullAnswer:  "  full answer  ",
		Provider:    "resolved-provider",
		Model:       "resolved-model",
		Latency:     25 * time.Millisecond,
	}}
	repository := &fakeRepository{}
	service := NewService(Options{
		Model:           "requested-model",
		MaxOutputTokens: 512,
		SaveHistory:     true,
	}, provider, repository)

	answer, err := service.Ask(t.Context(), AskRequest{
		Principal:      testPrincipal,
		ConversationID: "session-1",
		Prompt:         "  what is Go  ",
	})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer.ShortAnswer != "short answer" || answer.FullAnswer != "full answer" {
		t.Errorf("answer = %+v", answer)
	}
	if answer.Provider != "resolved-provider" || answer.Model != "resolved-model" {
		t.Errorf("provider metadata = %+v", answer)
	}
	if provider.request.Prompt != "what is Go" {
		t.Errorf("provider prompt = %q", provider.request.Prompt)
	}
	if provider.request.Principal != testPrincipal {
		t.Errorf("provider principal = %+v", provider.request.Principal)
	}
	if provider.request.Model != "requested-model" || provider.request.MaxOutputTokens != 512 {
		t.Errorf("provider request = %+v", provider.request)
	}
	if strings.TrimSpace(provider.request.SystemPrompt) == "" {
		t.Error("provider system prompt is empty")
	}
	if len(repository.saved) != 1 {
		t.Fatalf("saved interactions = %d", len(repository.saved))
	}
	saved := repository.saved[0]
	if saved.Status != StatusOK || saved.Prompt != "what is Go" || saved.Latency != 25*time.Millisecond {
		t.Errorf("saved interaction = %+v", saved)
	}
	if saved.Principal != testPrincipal || saved.ConversationID != "session-1" {
		t.Errorf("saved scope = %+v", saved)
	}
}

func TestServiceReconfigureAppliesToSubsequentRequests(t *testing.T) {
	initial := &fakeProvider{name: "openai", response: ProviderResponse{ShortAnswer: "initial"}}
	next := &fakeProvider{name: "anthropic", response: ProviderResponse{ShortAnswer: "next"}}
	service := NewService(Options{Model: "gpt-old", MaxOutputTokens: 128}, initial, nil)

	if err := service.Reconfigure(Options{Model: "claude-new", MaxOutputTokens: 512}, next); err != nil {
		t.Fatalf("Reconfigure() error = %v", err)
	}
	answer, err := service.Ask(t.Context(), AskRequest{Principal: testPrincipal, Prompt: "question"})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer.Provider != "anthropic" || next.request.Model != "claude-new" || next.request.MaxOutputTokens != 512 {
		t.Fatalf("reconfigured request/answer = %#v / %#v", next.request, answer)
	}
	if initial.request.Prompt != "" {
		t.Fatal("the previous provider handled a request after reconfiguration")
	}
}

func TestServiceReconfigureRejectsMissingProvider(t *testing.T) {
	service := NewService(Options{}, &fakeProvider{}, nil)
	if err := service.Reconfigure(Options{}, nil); !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("Reconfigure() error = %v", err)
	}
}

func TestServiceAskRecordsRedactedProviderFailure(t *testing.T) {
	const secret = "secret-token"
	provider := &fakeProvider{err: errors.Join(ErrProviderUnavailable, errors.New(secret))}
	repository := &fakeRepository{}
	service := NewService(Options{
		Model:       "requested-model",
		SaveHistory: true,
		Secrets:     []string{secret},
	}, provider, repository)

	_, err := service.Ask(t.Context(), AskRequest{Principal: testPrincipal, Prompt: "question"})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("Ask() error = %v", err)
	}
	if len(repository.saved) != 1 {
		t.Fatalf("saved interactions = %d", len(repository.saved))
	}
	saved := repository.saved[0]
	if saved.Status != StatusError {
		t.Errorf("status = %q", saved.Status)
	}
	if strings.Contains(saved.ErrorMessage, secret) || !strings.Contains(saved.ErrorMessage, "[REDACTED]") {
		t.Errorf("redacted error = %q", saved.ErrorMessage)
	}
}

func TestServiceAskNormalizesUnknownProviderFailure(t *testing.T) {
	service := NewService(Options{}, &fakeProvider{err: errors.New("boom")}, nil)

	_, err := service.Ask(t.Context(), AskRequest{Principal: testPrincipal, Prompt: "question"})
	if !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("Ask() error = %v", err)
	}
}

func TestServiceAskValidatesInput(t *testing.T) {
	service := NewService(Options{}, &fakeProvider{}, nil)

	if _, err := service.Ask(t.Context(), AskRequest{Principal: testPrincipal}); !errors.Is(err, ErrEmptyPrompt) {
		t.Errorf("empty prompt error = %v", err)
	}
	if _, err := service.Ask(t.Context(), AskRequest{Prompt: "question"}); !errors.Is(err, ErrInvalidPrincipal) {
		t.Errorf("invalid principal error = %v", err)
	}
}

func TestServiceAskHonorsHistoryDisabled(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(Options{SaveHistory: false}, &fakeProvider{}, repository)

	if _, err := service.Ask(t.Context(), AskRequest{Principal: testPrincipal, Prompt: "question"}); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if len(repository.saved) != 0 {
		t.Errorf("saved interactions = %d", len(repository.saved))
	}
}

func TestServiceAskDoesNotFailWhenHistoryWriteFails(t *testing.T) {
	service := NewService(
		Options{SaveHistory: true},
		&fakeProvider{response: ProviderResponse{ShortAnswer: "answer"}},
		&fakeRepository{saveErr: errors.New("disk full")},
	)

	answer, err := service.Ask(t.Context(), AskRequest{Principal: testPrincipal, Prompt: "question"})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer.ShortAnswer != "answer" {
		t.Errorf("answer = %+v", answer)
	}
}

func TestServiceLatestAnswer(t *testing.T) {
	repository := &fakeRepository{latest: &Interaction{
		ShortAnswer: " previous answer ",
		FullAnswer:  " full previous answer ",
		Provider:    "fake",
		Model:       "model",
		Latency:     10 * time.Millisecond,
	}}
	service := NewService(Options{SaveHistory: true}, &fakeProvider{}, repository)

	answer, err := service.LatestAnswer(t.Context(), testPrincipal)
	if err != nil {
		t.Fatalf("LatestAnswer() error = %v", err)
	}
	if answer.ShortAnswer != "previous answer" || answer.FullAnswer != "full previous answer" {
		t.Errorf("answer = %+v", answer)
	}
}

func TestServiceLatestAnswerStates(t *testing.T) {
	tests := []struct {
		name       string
		opts       Options
		repository Repository
		principal  Principal
		want       error
	}{
		{
			name:       "history disabled",
			opts:       Options{SaveHistory: false},
			repository: &fakeRepository{},
			principal:  testPrincipal,
			want:       ErrNoHistory,
		},
		{
			name:       "not found",
			opts:       Options{SaveHistory: true},
			repository: &fakeRepository{},
			principal:  testPrincipal,
			want:       ErrNoHistory,
		},
		{
			name: "empty saved answer",
			opts: Options{SaveHistory: true},
			repository: &fakeRepository{latest: &Interaction{
				ShortAnswer: " ",
				FullAnswer:  " ",
			}},
			principal: testPrincipal,
			want:      ErrEmptySavedAnswer,
		},
		{
			name:       "invalid principal",
			opts:       Options{SaveHistory: true},
			repository: &fakeRepository{},
			principal:  Principal{},
			want:       ErrNoHistory,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(tt.opts, &fakeProvider{}, tt.repository)
			_, err := service.LatestAnswer(t.Context(), tt.principal)
			if !errors.Is(err, tt.want) {
				t.Errorf("LatestAnswer() error = %v, want %v", err, tt.want)
			}
		})
	}
}

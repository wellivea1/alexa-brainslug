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

	// blockUntilDone makes Ask wait for its context, like a slow model.
	blockUntilDone bool
	deadline       time.Time
	hasDeadline    bool
}

func (p *fakeProvider) Name() string {
	if p.name == "" {
		return "fake"
	}
	return p.name
}

func (p *fakeProvider) Ask(ctx context.Context, req ProviderRequest) (ProviderResponse, error) {
	p.request = req
	p.deadline, p.hasDeadline = ctx.Deadline()
	if p.blockUntilDone {
		<-ctx.Done()
		return ProviderResponse{}, ctx.Err()
	}
	return p.response, p.err
}

type fakeRepository struct {
	saved   []Interaction
	latest  *Interaction
	saveErr error
	loadErr error
}

// SaveInteraction fails on a finished context, as database/sql does.
func (r *fakeRepository) SaveInteraction(ctx context.Context, interaction Interaction) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
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
		FullAnswer: "  full answer  ",
		Provider:   "resolved-provider",
		Model:      "resolved-model",
		Latency:    25 * time.Millisecond,
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
		SystemPrompt:   "Write for a screen, preserving Markdown.",
	})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer.FullAnswer != "full answer" {
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
	if provider.request.SystemPrompt != "Write for a screen, preserving Markdown." {
		t.Errorf("adapter instructions were replaced: %q", provider.request.SystemPrompt)
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
	initial := &fakeProvider{name: "openai", response: ProviderResponse{FullAnswer: "initial"}}
	next := &fakeProvider{name: "anthropic", response: ProviderResponse{FullAnswer: "next"}}
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

// A caller that hangs up mid-request (Alexa giving up, a proxy dropping the
// connection) must still leave a history record, and the record must not
// blame the provider.
func TestServiceAskRecordsOutcomeAfterCallerCancels(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(Options{Model: "m", SaveHistory: true}, &fakeProvider{blockUntilDone: true}, repository)

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(20*time.Millisecond, cancel)
	_, err := service.Ask(ctx, AskRequest{Principal: testPrincipal, Prompt: "question"})

	if !errors.Is(err, ErrRequestCanceled) || errors.Is(err, ErrProviderFailed) {
		t.Fatalf("Ask() error = %v, want ErrRequestCanceled only", err)
	}
	if len(repository.saved) != 1 {
		t.Fatalf("saved interactions = %d, want the canceled request recorded", len(repository.saved))
	}
	if saved := repository.saved[0]; saved.Status != StatusError || !strings.Contains(saved.ErrorMessage, "canceled") {
		t.Errorf("saved interaction = %+v", saved)
	}
}

func TestServiceAskRecordsOutcomeAfterDeadline(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(Options{Model: "m", SaveHistory: true}, &fakeProvider{blockUntilDone: true}, repository)

	ctx, cancel := context.WithTimeout(t.Context(), persistTimeout+20*time.Millisecond)
	defer cancel()
	_, err := service.Ask(ctx, AskRequest{Principal: testPrincipal, Prompt: "question"})

	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("Ask() error = %v, want ErrProviderUnavailable", err)
	}
	if len(repository.saved) != 1 {
		t.Fatalf("saved interactions = %d, want the timed-out request recorded", len(repository.saved))
	}
}

// The provider stops early enough to leave persistTimeout for the save, but
// only when there is a save to make room for.
func TestServiceAskReservesPersistenceTime(t *testing.T) {
	tests := []struct {
		name        string
		saveHistory bool
		reserve     time.Duration
	}{
		{"history enabled", true, persistTimeout},
		{"history disabled", false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fakeProvider{response: ProviderResponse{FullAnswer: "answer"}}
			service := NewService(Options{SaveHistory: tt.saveHistory}, provider, &fakeRepository{})

			deadline := time.Now().Add(5 * time.Second)
			ctx, cancel := context.WithDeadline(t.Context(), deadline)
			defer cancel()
			if _, err := service.Ask(ctx, AskRequest{Principal: testPrincipal, Prompt: "question"}); err != nil {
				t.Fatalf("Ask() error = %v", err)
			}
			if !provider.hasDeadline {
				t.Fatal("provider context has no deadline")
			}
			if want := deadline.Add(-tt.reserve); !provider.deadline.Equal(want) {
				t.Errorf("provider deadline = %v, want %v", provider.deadline, want)
			}
		})
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
		&fakeProvider{response: ProviderResponse{FullAnswer: "answer"}},
		&fakeRepository{saveErr: errors.New("disk full")},
	)

	answer, err := service.Ask(t.Context(), AskRequest{Principal: testPrincipal, Prompt: "question"})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer.FullAnswer != "answer" {
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
	if answer.FullAnswer != "full previous answer" {
		t.Errorf("answer = %+v", answer)
	}
}

func TestServicePreservesAdapterPresentation(t *testing.T) {
	full := strings.TrimSpace("# Detailed answer\n\n" + strings.Repeat("**Keep this detail.** ", 150))
	for _, instructions := range []string{"", "Use Markdown for a web page."} {
		t.Run(instructions, func(t *testing.T) {
			provider := &fakeProvider{response: ProviderResponse{FullAnswer: full}}
			repository := &fakeRepository{}
			service := NewService(Options{SaveHistory: true}, provider, repository)
			answer, err := service.Ask(t.Context(), AskRequest{
				Principal: testPrincipal, Prompt: "question", SystemPrompt: instructions,
			})
			if err != nil {
				t.Fatal(err)
			}
			if provider.request.SystemPrompt != instructions || answer.FullAnswer != full {
				t.Fatal("the application changed the adapter's instructions or full answer")
			}
			if len(repository.saved) != 1 || repository.saved[0].FullAnswer != full || repository.saved[0].ShortAnswer != "" {
				t.Fatal("history must preserve the full answer without creating a voice summary")
			}
		})
	}
}

func TestServiceLatestAnswerReadsLegacySummary(t *testing.T) {
	repository := &fakeRepository{latest: &Interaction{ShortAnswer: " legacy answer "}}
	service := NewService(Options{SaveHistory: true}, &fakeProvider{}, repository)
	answer, err := service.LatestAnswer(t.Context(), testPrincipal)
	if err != nil || answer.FullAnswer != "legacy answer" {
		t.Fatalf("legacy answer = %+v, error = %v", answer, err)
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

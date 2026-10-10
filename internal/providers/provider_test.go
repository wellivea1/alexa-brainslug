package providers

import (
	"context"
	"testing"
)

type mockProvider struct{}

func (mockProvider) Name() string {
	return "mock"
}

func (mockProvider) Ask(_ context.Context, req Request) (Response, error) {
	return Response{
		FullAnswer:   "full",
		Model:        req.Model,
		ProviderName: "mock",
	}, nil
}

func TestProviderInterfaceWithMock(t *testing.T) {
	var provider Provider = mockProvider{}

	resp, err := provider.Ask(context.Background(), Request{
		UserID: "user-1",
		Prompt: "hello",
		Model:  "gpt-test",
	})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if provider.Name() != "mock" {
		t.Fatalf("Name() = %q", provider.Name())
	}
	if resp.FullAnswer != "full" || resp.Model != "gpt-test" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

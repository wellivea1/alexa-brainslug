package providers

import (
	"context"
	"errors"
)

var ErrOpenCodeNotImplemented = errors.New("opencode provider is not implemented yet")

type OpenCodeStub struct{}

func (OpenCodeStub) Name() string {
	return "opencode"
}

func (OpenCodeStub) Ask(ctx context.Context, req Request) (Response, error) {
	return Response{}, ErrOpenCodeNotImplemented
}

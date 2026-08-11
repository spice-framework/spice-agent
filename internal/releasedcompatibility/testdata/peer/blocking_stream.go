package main

import (
	"context"

	"github.com/spice-framework/spice-agent/model"
)

type BlockingStream struct{}

func (BlockingStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	<-ctx.Done()
	return model.StreamEvent{}, ctx.Err()
}

func (BlockingStream) Close() error { return nil }

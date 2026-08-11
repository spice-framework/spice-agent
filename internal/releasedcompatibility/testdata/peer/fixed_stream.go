package main

import (
	"context"
	"io"
	"slices"

	"github.com/spice-framework/spice-agent/model"
)

type FixedStream struct {
	events []model.StreamEvent
	next   int
}

func NewFixedStream(events []model.StreamEvent) *FixedStream {
	return &FixedStream{events: slices.Clone(events)}
}

func (stream *FixedStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return model.StreamEvent{}, err
	}
	if stream.next == len(stream.events) {
		return model.StreamEvent{}, io.EOF
	}
	value := stream.events[stream.next]
	stream.next++
	return value, nil
}

func (*FixedStream) Close() error { return nil }

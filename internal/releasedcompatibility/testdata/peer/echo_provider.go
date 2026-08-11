package main

import (
	"context"

	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
)

type EchoProvider struct{}

func (EchoProvider) Stream(ctx context.Context, request model.Request) (model.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input := ""
	messages := request.Messages()
	if len(messages) != 0 {
		for _, part := range messages[len(messages)-1].Parts() {
			if value, ok := part.TextValue(); ok && messages[len(messages)-1].Role() == message.RoleUser {
				input = value
				break
			}
		}
	}
	if input == "wait for cancellation" {
		return BlockingStream{}, nil
	}
	delta, err := model.TextDelta("released peer handled: " + input)
	if err != nil {
		return nil, err
	}
	completed, err := model.Completed(model.NewUsage(1, 1))
	if err != nil {
		return nil, err
	}
	return NewFixedStream([]model.StreamEvent{delta, completed}), nil
}

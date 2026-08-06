// Package model defines provider-neutral model streaming contracts.
package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/tool"
)

// Request is an immutable snapshot of one model operation.
type Request struct {
	model    string
	messages []message.Message
	tools    []tool.Definition
}

// NewRequest validates and copies one provider request.
func NewRequest(modelName string, messages []message.Message, tools []tool.Definition) (Request, error) {
	if modelName == "" || modelName != strings.TrimSpace(modelName) {
		return Request{}, errors.New("model name must be non-empty without surrounding whitespace")
	}
	if len(messages) == 0 {
		return Request{}, errors.New("model request requires at least one message")
	}
	result := Request{model: modelName, messages: append([]message.Message(nil), messages...), tools: make([]tool.Definition, len(tools))}
	seen := make(map[string]struct{}, len(tools))
	for index, definition := range tools {
		if err := definition.Validate(); err != nil {
			return Request{}, fmt.Errorf("model tool %d: %w", index, err)
		}
		if _, duplicate := seen[definition.Name()]; duplicate {
			return Request{}, fmt.Errorf("model tool %q is duplicated", definition.Name())
		}
		seen[definition.Name()] = struct{}{}
		result.tools[index] = definition.Clone()
	}
	return result, nil
}

// Model returns the selected provider model name.
func (request Request) Model() string { return request.model }

// Messages returns a defensive copy of request messages.
func (request Request) Messages() []message.Message {
	return append([]message.Message(nil), request.messages...)
}

// Tools returns a defensive copy of tool definitions.
func (request Request) Tools() []tool.Definition {
	result := make([]tool.Definition, len(request.tools))
	for index, definition := range request.tools {
		result[index] = definition.Clone()
	}
	return result
}

// EventKind identifies a model stream item.
type EventKind string

const (
	EventTextDelta EventKind = "text_delta"
	EventToolCall  EventKind = "tool_call"
	EventCompleted EventKind = "completed"
	EventFailed    EventKind = "failed"
)

// Usage contains provider-normalized token accounting. Zero means unknown.
type Usage struct {
	InputTokens  uint64
	OutputTokens uint64
	TotalTokens  uint64
}

// Problem is a provider-neutral typed terminal model failure.
type Problem struct {
	Code         string
	Message      string
	Retryable    bool
	BeforeStream bool
}

// StreamEvent is one ordered provider stream item.
type StreamEvent struct {
	Kind    EventKind
	Text    string
	Call    tool.Call
	Usage   Usage
	Problem Problem
}

// Validate rejects malformed and ambiguous stream values.
func (streamEvent StreamEvent) Validate() error {
	switch streamEvent.Kind {
	case EventTextDelta:
		if streamEvent.Text == "" {
			return errors.New("model text delta must not be empty")
		}
		return nil
	case EventToolCall:
		return streamEvent.Call.Validate()
	case EventCompleted:
		if streamEvent.Text != "" || streamEvent.Call.ID != "" || streamEvent.Problem.Code != "" {
			return errors.New("model completion event must not contain payload")
		}
		if streamEvent.Usage.TotalTokens != 0 && streamEvent.Usage.TotalTokens != streamEvent.Usage.InputTokens+streamEvent.Usage.OutputTokens {
			return errors.New("model usage total must equal input plus output tokens")
		}
		return nil
	case EventFailed:
		if streamEvent.Problem.Code == "" || streamEvent.Problem.Code != strings.TrimSpace(streamEvent.Problem.Code) {
			return errors.New("model failure requires a trimmed problem code")
		}
		if strings.TrimSpace(streamEvent.Problem.Message) == "" {
			return errors.New("model failure requires a message")
		}
		return nil
	default:
		return fmt.Errorf("model stream event kind %q is unsupported", streamEvent.Kind)
	}
}

// Stream supplies ordered model events. Recv returns io.EOF only after a valid
// completed event has already been returned.
type Stream interface {
	Recv(context.Context) (StreamEvent, error)
	Close() error
}

// Provider starts one model operation. Implementations must not retry after any
// stream item is observable.
type Provider interface {
	Stream(context.Context, Request) (Stream, error)
}

// RequireCompletion converts premature EOF into a contract error.
func RequireCompletion(streamEvent StreamEvent, err error, completed bool) error {
	if !errors.Is(err, io.EOF) || completed {
		return err
	}
	return errors.New("model stream ended before completion")
}

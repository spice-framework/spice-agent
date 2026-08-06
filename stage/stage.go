// Package stage defines replaceable, constructor-injected execution seams.
package stage

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/spice-framework/spice-agent/tool"
)

// ToolDispatcher is the sole executable route for tool calls.
type ToolDispatcher interface {
	Dispatch(context.Context, tool.Call, tool.Reporter) tool.Result
}

// ToolDispatchDecorator wraps the canonical dispatcher. Spice supplies these
// as an ordered typed collection.
type ToolDispatchDecorator interface {
	Wrap(ToolDispatcher) ToolDispatcher
}

// Dispatcher is an immutable named tool map constructed by Spice.
type Dispatcher struct {
	tools map[string]tool.Tool
}

// NewDispatcher validates canonical bean names and model-visible definitions.
func NewDispatcher(tools map[string]tool.Tool) (*Dispatcher, error) {
	result := make(map[string]tool.Tool, len(tools))
	for name, implementation := range tools {
		if implementation == nil {
			return nil, fmt.Errorf("tool bean %q is nil", name)
		}
		definition := implementation.Definition()
		if err := definition.Validate(); err != nil {
			return nil, fmt.Errorf("tool bean %q: %w", name, err)
		}
		if definition.Name() != name {
			return nil, fmt.Errorf("tool bean %q declares model name %q", name, definition.Name())
		}
		result[name] = implementation
	}
	return &Dispatcher{tools: result}, nil
}

// Definitions returns definitions ordered by canonical bean name.
func (dispatcher *Dispatcher) Definitions() []tool.Definition {
	names := slices.Sorted(maps.Keys(dispatcher.tools))
	result := make([]tool.Definition, 0, len(names))
	for _, name := range names {
		result = append(result, dispatcher.tools[name].Definition().Clone())
	}
	return result
}

// Dispatch validates and invokes one named tool without reflection.
func (dispatcher *Dispatcher) Dispatch(ctx context.Context, call tool.Call, reporter tool.Reporter) tool.Result {
	if err := call.Validate(); err != nil {
		return errorResult(call.ID, err)
	}
	implementation, found := dispatcher.tools[call.Name]
	if !found {
		return errorResult(call.ID, fmt.Errorf("tool %q is not available", call.Name))
	}
	return implementation.Execute(ctx, call, reporter)
}

func errorResult(callID tool.CallID, err error) tool.Result {
	content, _ := json.Marshal(map[string]string{"error": err.Error()})
	return tool.Result{CallID: callID, Content: content, Error: err.Error()}
}

package compositionfixture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spice-framework/spice-agent/tool"
	"github.com/spice-framework/spice/lifecycle"
)

// @import { Tool } from "github.com/spice-framework/spice-agent/annotation/agent"
// @import { Qualifier } from "github.com/spice-framework/spice/annotation/core"

type fixtureTool struct {
	definition tool.Definition
}

func (fixture *fixtureTool) Definition() tool.Definition {
	return fixture.definition.Clone()
}

func (fixture *fixtureTool) Execute(
	_ context.Context,
	call tool.Call,
	_ tool.Reporter,
) tool.Result {
	result, err := tool.NewResult(call.ID(), json.RawMessage(`{"ok":true}`))
	if err != nil {
		return tool.Result{}
	}
	return result
}

func newFixtureTool(name string) (*fixtureTool, error) {
	definition, err := tool.NewDefinition(
		name,
		"Composition proof tool.",
		json.RawMessage(`{"type":"object","additionalProperties":false}`),
	)
	if err != nil {
		return nil, fmt.Errorf("construct fixture tool %s: %w", name, err)
	}
	return &fixtureTool{definition: definition}, nil
}

// NewReadTool constructs the alias-qualified fixture tool.
//
// @Tool(name="read", aliases=["inspect"], qualifiers=["coding"], order=10)
func NewReadTool(log *CleanupLog) (ToolAlias, lifecycle.Cleanup, error) {
	implementation, err := newFixtureTool("read")
	if err != nil {
		return nil, nil, err
	}
	return implementation, func(context.Context) error {
		log.record("read")
		return nil
	}, nil
}

// NewWriteTool constructs a tool that depends on the read alias.
//
// @Tool(name="write", aliases=["save"], qualifiers=["coding"], order=20)
func NewWriteTool(
	log *CleanupLog,
	// @Qualifier("inspect")
	upstream ToolAlias,
) (ToolAlias, lifecycle.Cleanup, error) {
	if upstream == nil {
		return nil, nil, errors.New("qualified read dependency is nil")
	}
	implementation, err := newFixtureTool("write")
	if err != nil {
		return nil, nil, err
	}
	return implementation, func(context.Context) error {
		log.record("write")
		return nil
	}, nil
}

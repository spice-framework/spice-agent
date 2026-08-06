package compositionfixture

import (
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

// ProviderAlias proves that exact interface-return aliases retain Go identity.
type ProviderAlias = model.Provider

// ToolAlias proves that exact tool interface-return aliases retain Go identity.
type ToolAlias = tool.Tool

// TextStage proves exact instantiated generic interface alias resolution.
type TextStage = stage.Stage[string, string]

// FallbackStage is a narrow application-owned stage interface with no normal
// candidate, proving fallback selection.
type FallbackStage interface {
	Name() string
}

// ReplaceableStage is a narrow application-owned stage interface with both a
// fallback and a normal candidate, proving normal replacement.
type ReplaceableStage interface {
	Name() string
}

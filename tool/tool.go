// Package tool defines provider-neutral executable tool contracts.
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const maxPayloadBytes = 1 << 20

// CallID identifies one tool operation.
type CallID string

// Capability declares security-relevant effects without pretending to sandbox
// trusted tool code.
type Capability string

const (
	CapabilityFilesystemRead  Capability = "filesystem.read"
	CapabilityFilesystemWrite Capability = "filesystem.write"
	CapabilityProcessExecute  Capability = "process.execute"
	CapabilityNetworkAccess   Capability = "network.access"
	CapabilitySecretsRead     Capability = "secrets.read"
)

// Definition is the immutable model-visible description of a compiled or
// runtime tool. Capability order is declaration order and is significant.
type Definition struct {
	name         string
	description  string
	inputSchema  json.RawMessage
	capabilities []Capability
}

// NewDefinition validates and defensively copies a tool definition.
func NewDefinition(name, description string, inputSchema json.RawMessage, capabilities ...Capability) (Definition, error) {
	if err := validateName("tool name", name); err != nil {
		return Definition{}, err
	}
	if strings.TrimSpace(description) == "" {
		return Definition{}, errors.New("tool description must not be empty")
	}
	if len(inputSchema) == 0 || !json.Valid(inputSchema) {
		return Definition{}, errors.New("tool input schema must be valid JSON")
	}
	if len(inputSchema) > maxPayloadBytes {
		return Definition{}, fmt.Errorf("tool input schema exceeds %d bytes", maxPayloadBytes)
	}
	seen := make(map[Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if !validCapability(capability) {
			return Definition{}, fmt.Errorf("tool capability %q is unsupported", capability)
		}
		if _, duplicate := seen[capability]; duplicate {
			return Definition{}, fmt.Errorf("tool capability %q is duplicated", capability)
		}
		seen[capability] = struct{}{}
	}
	return Definition{name: name, description: description, inputSchema: append(json.RawMessage(nil), inputSchema...), capabilities: append([]Capability(nil), capabilities...)}, nil
}

// Validate rejects a zero or corrupted definition.
func (definition Definition) Validate() error {
	_, err := NewDefinition(definition.name, definition.description, definition.inputSchema, definition.capabilities...)
	return err
}

// Name returns the canonical Spice bean and model-visible name.
func (definition Definition) Name() string { return definition.name }

// Description returns human-facing model guidance.
func (definition Definition) Description() string { return definition.description }

// InputSchema returns a defensive copy.
func (definition Definition) InputSchema() json.RawMessage {
	return append(json.RawMessage(nil), definition.inputSchema...)
}

// Capabilities returns an ordered defensive copy.
func (definition Definition) Capabilities() []Capability {
	return append([]Capability(nil), definition.capabilities...)
}

func validCapability(capability Capability) bool {
	switch capability {
	case CapabilityFilesystemRead, CapabilityFilesystemWrite,
		CapabilityProcessExecute, CapabilityNetworkAccess, CapabilitySecretsRead:
		return true
	default:
		return false
	}
}

// Clone returns a defensive copy.
func (definition Definition) Clone() Definition {
	definition.inputSchema = append(json.RawMessage(nil), definition.inputSchema...)
	definition.capabilities = append([]Capability(nil), definition.capabilities...)
	return definition
}

// Call is one validated invocation requested by a model.
type Call struct {
	ID        CallID
	Name      string
	Arguments json.RawMessage
}

// Validate rejects malformed calls before dispatch.
func (call Call) Validate() error {
	if err := validateName("tool call ID", string(call.ID)); err != nil {
		return err
	}
	if err := validateName("tool name", call.Name); err != nil {
		return err
	}
	if len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
		return errors.New("tool arguments must be valid JSON")
	}
	if len(call.Arguments) > maxPayloadBytes {
		return fmt.Errorf("tool arguments exceed %d bytes", maxPayloadBytes)
	}
	return nil
}

// Result is one terminal tool outcome. An error result is data for the model,
// not a Go transport failure.
type Result struct {
	CallID  CallID
	Content json.RawMessage
	Error   string
}

// Validate enforces one bounded terminal result.
func (result Result) Validate() error {
	if err := validateName("tool result call ID", string(result.CallID)); err != nil {
		return err
	}
	if len(result.Content) == 0 || !json.Valid(result.Content) {
		return errors.New("tool result content must be valid JSON")
	}
	if len(result.Content) > maxPayloadBytes {
		return fmt.Errorf("tool result content exceeds %d bytes", maxPayloadBytes)
	}
	return nil
}

// Progress is bounded observable progress for a running call.
type Progress struct {
	CallID  CallID
	Message string
}

// Reporter receives progress synchronously. Implementations must honor context
// cancellation and must not retain mutable call buffers.
type Reporter interface {
	Report(context.Context, Progress) error
}

// Tool is one constructor-injected executable contribution.
type Tool interface {
	Definition() Definition
	Execute(context.Context, Call, Reporter) Result
}

func validateName(label, value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must be non-empty without surrounding whitespace", label)
	}
	if len(value) > 128 {
		return fmt.Errorf("%s exceeds 128 bytes", label)
	}
	return nil
}

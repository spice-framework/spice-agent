// Package message defines provider-neutral immutable conversation values.
package message

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	maxIDLength        = 128
	maxNamespaceLength = 256
	maxPartBytes       = 1 << 20
)

// ID identifies one message within a run.
type ID string

// NewID validates one externally supplied stable message identifier.
func NewID(value string) (ID, error) {
	if err := validateToken("message ID", value, maxIDLength); err != nil {
		return "", err
	}
	return ID(value), nil
}

// Role identifies a message author without provider-specific semantics.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// PartKind identifies the one payload carried by a Part.
type PartKind string

const (
	PartText       PartKind = "text"
	PartToolCall   PartKind = "tool_call"
	PartToolResult PartKind = "tool_result"
	PartExtension  PartKind = "extension"
)

// Part is an immutable bounded content part.
type Part struct {
	kind      PartKind
	text      string
	name      string
	callID    string
	namespace string
	data      []byte
}

// Text constructs a text part.
func Text(value string) (Part, error) {
	if value == "" {
		return Part{}, errors.New("message text must not be empty")
	}
	if len(value) > maxPartBytes {
		return Part{}, fmt.Errorf("message text exceeds %d bytes", maxPartBytes)
	}
	return Part{kind: PartText, text: value}, nil
}

// ToolCall constructs a model-requested tool call part.
func ToolCall(callID, name string, arguments json.RawMessage) (Part, error) {
	return structuredPart(PartToolCall, callID, name, "", arguments)
}

// ToolResult constructs a tool result part associated with a call name.
func ToolResult(callID, name string, result json.RawMessage) (Part, error) {
	return structuredPart(PartToolResult, callID, name, "", result)
}

// Extension constructs a namespaced JSON extension part.
func Extension(namespace string, value json.RawMessage) (Part, error) {
	if err := validateToken("extension namespace", namespace, maxNamespaceLength); err != nil {
		return Part{}, err
	}
	return structuredPart(PartExtension, "", "", namespace, value)
}

func structuredPart(kind PartKind, callID, name, namespace string, value json.RawMessage) (Part, error) {
	if kind != PartExtension {
		if err := validateToken("tool call ID", callID, maxIDLength); err != nil {
			return Part{}, err
		}
		if err := validateToken("tool name", name, maxIDLength); err != nil {
			return Part{}, err
		}
	}
	if len(value) == 0 || !json.Valid(value) {
		return Part{}, errors.New("message structured part requires one valid JSON value")
	}
	if len(value) > maxPartBytes {
		return Part{}, fmt.Errorf("message structured part exceeds %d bytes", maxPartBytes)
	}
	return Part{kind: kind, callID: callID, name: name, namespace: namespace, data: append([]byte(nil), value...)}, nil
}

// Kind returns the part discriminator.
func (part Part) Kind() PartKind { return part.kind }

// TextValue returns text content and whether this is a text part.
func (part Part) TextValue() (string, bool) { return part.text, part.kind == PartText }

// Name returns the tool name for tool-call and tool-result parts.
func (part Part) Name() string { return part.name }

// CallID returns the correlation identity for tool-call and tool-result parts.
func (part Part) CallID() string { return part.callID }

// Namespace returns the extension namespace.
func (part Part) Namespace() string { return part.namespace }

// Data returns a defensive copy of structured JSON content.
func (part Part) Data() json.RawMessage { return append(json.RawMessage(nil), part.data...) }

// Message is one validated immutable conversation entry.
type Message struct {
	id    ID
	role  Role
	parts []Part
}

// New constructs a message and defensively copies its parts.
func New(id ID, role Role, parts ...Part) (Message, error) {
	if _, err := NewID(string(id)); err != nil {
		return Message{}, err
	}
	if !validRole(role) {
		return Message{}, fmt.Errorf("message role %q is unsupported", role)
	}
	if len(parts) == 0 {
		return Message{}, errors.New("message requires at least one part")
	}
	result := Message{id: id, role: role, parts: make([]Part, len(parts))}
	for index, part := range parts {
		if !validPart(part) {
			return Message{}, fmt.Errorf("message part %d is invalid", index)
		}
		result.parts[index] = clonePart(part)
	}
	return result, nil
}

// ID returns the message identity.
func (value Message) ID() ID { return value.id }

// Role returns the author role.
func (value Message) Role() Role { return value.role }

// Parts returns a defensive copy.
func (value Message) Parts() []Part {
	result := make([]Part, len(value.parts))
	for index, part := range value.parts {
		result[index] = clonePart(part)
	}
	return result
}

func clonePart(part Part) Part {
	part.data = append([]byte(nil), part.data...)
	return part
}

func validRole(role Role) bool {
	return role == RoleSystem || role == RoleUser || role == RoleAssistant || role == RoleTool
}

func validPart(part Part) bool {
	switch part.kind {
	case PartText:
		return part.text != "" && len(part.text) <= maxPartBytes
	case PartToolCall, PartToolResult:
		return validateToken("tool call ID", part.callID, maxIDLength) == nil &&
			validateToken("tool name", part.name, maxIDLength) == nil && json.Valid(part.data)
	case PartExtension:
		return validateToken("extension namespace", part.namespace, maxNamespaceLength) == nil && json.Valid(part.data)
	default:
		return false
	}
}

func validateToken(name, value string, maximum int) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must be non-empty without surrounding whitespace", name)
	}
	if len(value) > maximum {
		return fmt.Errorf("%s exceeds %d bytes", name, maximum)
	}
	return nil
}

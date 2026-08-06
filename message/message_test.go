package message_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/message"
)

func TestMessageValuesAreValidatedAndDefensive(t *testing.T) {
	text, err := message.Text("hello")
	if err != nil {
		t.Fatal(err)
	}
	data := json.RawMessage(`{"value":1}`)
	extension, err := message.Extension("acme.view", data)
	if err != nil {
		t.Fatal(err)
	}
	data[2] = 'X'
	id, _ := message.NewID("message-1")
	value, err := message.New(id, message.RoleUser, text, extension)
	if err != nil {
		t.Fatal(err)
	}
	parts := value.Parts()
	copyData := parts[1].Data()
	copyData[2] = 'Y'
	if got := string(value.Parts()[1].Data()); got != `{"value":1}` {
		t.Fatalf("extension data = %s", got)
	}
	if got, ok := parts[0].TextValue(); !ok || got != "hello" {
		t.Fatalf("text = %q, %v", got, ok)
	}
	if value.ID() != id || value.Role() != message.RoleUser || parts[1].Namespace() != "acme.view" {
		t.Fatal("message accessors lost metadata")
	}
}

func TestMessageRejectsInvalidValues(t *testing.T) {
	invalid := []func() error{
		func() error { _, err := message.NewID(" bad"); return err },
		func() error { _, err := message.NewID(strings.Repeat("x", 129)); return err },
		func() error { _, err := message.Text(""); return err },
		func() error { _, err := message.Text(strings.Repeat("x", (1<<20)+1)); return err },
		func() error { _, err := message.ToolCall("", "tool", json.RawMessage(`{}`)); return err },
		func() error { _, err := message.ToolResult("call", "tool", json.RawMessage(`{`)); return err },
		func() error { _, err := message.Extension(" bad", json.RawMessage(`{}`)); return err },
		func() error {
			_, err := message.Extension("acme", json.RawMessage(strings.Repeat(" ", (1<<20)+1)))
			return err
		},
		func() error { id, _ := message.NewID("m"); _, err := message.New(id, "unknown"); return err },
		func() error { id, _ := message.NewID("m"); _, err := message.New(id, message.RoleUser); return err },
		func() error {
			id, _ := message.NewID("m")
			_, err := message.New(id, message.RoleUser, message.Part{})
			return err
		},
	}
	for index, check := range invalid {
		if err := check(); err == nil {
			t.Fatalf("invalid case %d succeeded", index)
		}
	}
}

func TestToolPartsExposeMetadata(t *testing.T) {
	call, _ := message.ToolCall("call-1", "read", json.RawMessage(`{"path":"x"}`))
	result, _ := message.ToolResult("call-1", "read", json.RawMessage(`"ok"`))
	if call.Kind() != message.PartToolCall || call.Name() != "read" || call.CallID() != result.CallID() || result.Kind() != message.PartToolResult {
		t.Fatal("tool part metadata mismatch")
	}
	if _, ok := call.TextValue(); ok {
		t.Fatal("tool call reported as text")
	}
}

func FuzzNewID(f *testing.F) {
	f.Add("message-1")
	f.Add(" bad ")
	f.Fuzz(func(t *testing.T, value string) {
		id, err := message.NewID(value)
		if err == nil && string(id) != value {
			t.Fatalf("ID = %q", id)
		}
	})
}

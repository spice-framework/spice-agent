package tool_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/tool"
)

func TestDefinitionCapabilitiesAreOrderedAndDefensive(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	definition, err := tool.NewDefinition("shell", "Run a command.", schema, tool.CapabilityProcessExecute, tool.CapabilityFilesystemRead)
	if err != nil {
		t.Fatal(err)
	}
	schema[2] = 'X'
	capabilities := definition.Capabilities()
	capabilities[0] = tool.CapabilitySecretsRead
	if got := definition.Capabilities(); got[0] != tool.CapabilityProcessExecute || got[1] != tool.CapabilityFilesystemRead {
		t.Fatalf("capabilities = %v", got)
	}
	copySchema := definition.InputSchema()
	copySchema[2] = 'Y'
	if !json.Valid(definition.InputSchema()) || definition.Name() != "shell" || definition.Description() == "" {
		t.Fatal("definition was not immutable")
	}
	if err := definition.Clone().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDefinitionRejectsInvalidCapabilities(t *testing.T) {
	for _, capabilities := range [][]tool.Capability{{"unknown"}, {tool.CapabilityNetworkAccess, tool.CapabilityNetworkAccess}} {
		if _, err := tool.NewDefinition("http", "HTTP", json.RawMessage(`{}`), capabilities...); err == nil {
			t.Fatalf("capabilities %v succeeded", capabilities)
		}
	}
	invalid := []struct{ name, description, schema string }{
		{"", "description", `{}`}, {" bad", "description", `{}`},
		{strings.Repeat("x", 129), "description", `{}`}, {"ok", "", `{}`}, {"ok", "description", `{`},
	}
	for _, test := range invalid {
		if _, err := tool.NewDefinition(test.name, test.description, json.RawMessage(test.schema)); err == nil {
			t.Fatalf("invalid definition %+v succeeded", test)
		}
	}
}

func TestCallAndResultValidation(t *testing.T) {
	valid := tool.Call{ID: "call-1", Name: "read", Arguments: json.RawMessage(`{}`)}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (tool.Result{CallID: valid.ID, Content: json.RawMessage(`"ok"`)}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, call := range []tool.Call{{}, {ID: "call", Name: "read", Arguments: json.RawMessage(`{`)}} {
		if err := call.Validate(); err == nil {
			t.Fatalf("invalid call %+v succeeded", call)
		}
	}
	if err := (tool.Result{CallID: "call", Content: json.RawMessage(`{`)}).Validate(); err == nil {
		t.Fatal("invalid result succeeded")
	}
}

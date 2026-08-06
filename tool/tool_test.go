package tool_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/tool"
)

func TestImmutableValuesAndCapabilities(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	definition, err := tool.NewDefinition("shell", "Run a command.", schema, tool.CapabilityProcessExecute, tool.CapabilityEnvironmentRead)
	if err != nil {
		t.Fatal(err)
	}
	schema[2] = 'X'
	capabilities := definition.Capabilities()
	capabilities[0] = tool.CapabilitySecretsRead
	if definition.Capabilities()[0] != tool.CapabilityProcessExecute || !json.Valid(definition.InputSchema()) || definition.SizeBytes() == 0 {
		t.Fatal("definition is mutable")
	}
	if definition.Name() != "shell" || definition.Description() != "Run a command." || definition.Validate() != nil || definition.Clone().Validate() != nil {
		t.Fatal("definition accessors or clone mismatch")
	}
	arguments := json.RawMessage(`{"path":"x"}`)
	call, err := tool.NewCall("call-1", "shell", arguments)
	if err != nil {
		t.Fatal(err)
	}
	arguments[2] = 'X'
	copyArguments := call.Arguments()
	copyArguments[2] = 'Y'
	if !json.Valid(call.Arguments()) || call.ID() != "call-1" || call.Name() != "shell" || call.Clone().Validate() != nil {
		t.Fatal("call is mutable")
	}
	content := json.RawMessage(`"ok"`)
	result, err := tool.NewResult(call.ID(), content)
	if err != nil {
		t.Fatal(err)
	}
	content[1] = 'X'
	resultContent := result.Content()
	resultContent[1] = 'Y'
	if !json.Valid(result.Content()) || result.CallID() != call.ID() || result.Clone().Validate() != nil {
		t.Fatal("result is mutable")
	}
	if _, failed := result.Problem(); failed {
		t.Fatal("success reported a problem")
	}
	failure, err := tool.NewErrorResult(call.ID(), json.RawMessage(`{"error":true}`), "denied")
	if err != nil {
		t.Fatal(err)
	}
	if problem, failed := failure.Problem(); !failed || problem != "denied" {
		t.Fatal("error result lost problem")
	}
	progress, err := tool.NewProgress(call.ID(), "working")
	if err != nil || progress.CallID() != call.ID() || progress.Message() != "working" || progress.Validate() != nil {
		t.Fatalf("progress: %v", err)
	}
}

func TestDefinitionFingerprintCoversContractAndNormalizesCapabilities(t *testing.T) {
	base, _ := tool.NewDefinition("shell", "Run a command.", json.RawMessage(`{"type":"object"}`), tool.CapabilityProcessExecute, tool.CapabilityEnvironmentRead)
	reordered, _ := tool.NewDefinition("shell", "Run a command.", json.RawMessage(`{"type":"object"}`), tool.CapabilityEnvironmentRead, tool.CapabilityProcessExecute)
	if base.Fingerprint() == "" || base.Fingerprint() != reordered.Fingerprint() {
		t.Fatal("fingerprint is empty or capability-order dependent")
	}
	for _, changed := range []tool.Definition{
		mustDefinition(t, "other", "Run a command.", `{"type":"object"}`, tool.CapabilityProcessExecute, tool.CapabilityEnvironmentRead),
		mustDefinition(t, "shell", "Run something else.", `{"type":"object"}`, tool.CapabilityProcessExecute, tool.CapabilityEnvironmentRead),
		mustDefinition(t, "shell", "Run a command.", `{"type":"string"}`, tool.CapabilityProcessExecute, tool.CapabilityEnvironmentRead),
		mustDefinition(t, "shell", "Run a command.", `{"type":"object"}`, tool.CapabilityProcessExecute),
	} {
		if changed.Fingerprint() == base.Fingerprint() {
			t.Fatal("changed contract retained its fingerprint")
		}
	}
}

func mustDefinition(t *testing.T, name, description, schema string, capabilities ...tool.Capability) tool.Definition {
	t.Helper()
	definition, err := tool.NewDefinition(name, description, json.RawMessage(schema), capabilities...)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestValuesRejectInvalidAndOversizedInput(t *testing.T) {
	for _, capabilities := range [][]tool.Capability{{"unknown"}, {tool.CapabilityNetworkAccess, tool.CapabilityNetworkAccess}} {
		if _, err := tool.NewDefinition("http", "HTTP", json.RawMessage(`{}`), capabilities...); err == nil {
			t.Fatalf("capabilities %v succeeded", capabilities)
		}
	}
	if _, err := tool.NewDefinition("", "description", json.RawMessage(`{}`)); err == nil {
		t.Fatal("empty name succeeded")
	}
	if _, err := tool.NewDefinition("ok", "", json.RawMessage(`{}`)); err == nil {
		t.Fatal("empty description succeeded")
	}
	if _, err := tool.NewDefinition("ok", "description", json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid schema succeeded")
	}
	if _, err := tool.NewCall("", "read", json.RawMessage(`{}`)); err == nil {
		t.Fatal("empty call ID succeeded")
	}
	if _, err := tool.NewCall("call", "read", json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid arguments succeeded")
	}
	if _, err := tool.NewResult("", json.RawMessage(`{}`)); err == nil {
		t.Fatal("empty result ID succeeded")
	}
	if _, err := tool.NewErrorResult("call", json.RawMessage(`{}`), " "); err == nil {
		t.Fatal("empty problem succeeded")
	}
	if _, err := tool.NewProgress("call", strings.Repeat("x", tool.MaximumProgressBytes+1)); err == nil {
		t.Fatal("oversized progress succeeded")
	}
	large := json.RawMessage(`"` + strings.Repeat("x", tool.MaximumPayloadBytes) + `"`)
	if _, err := tool.NewCall("call", "read", large); err == nil {
		t.Fatal("oversized call succeeded")
	}
	if (tool.Call{}).Validate() == nil || (tool.Result{}).Validate() == nil || (tool.Progress{}).Validate() == nil {
		t.Fatal("zero value succeeded")
	}
	if (tool.Definition{}).Validate() == nil {
		t.Fatal("zero definition succeeded")
	}
}

func FuzzToolCall(f *testing.F) {
	f.Add("call-1", "read", []byte(`{}`))
	f.Fuzz(func(t *testing.T, id, name string, arguments []byte) {
		call, err := tool.NewCall(tool.CallID(id), name, json.RawMessage(arguments))
		if err == nil && call.Validate() != nil {
			t.Fatal("constructed call did not validate")
		}
	})
}

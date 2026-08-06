package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/tool"
)

func TestImmutableValuesAndCapabilities(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	declaredCapabilities := []tool.Capability{tool.CapabilityProcessExecute, tool.CapabilityEnvironmentRead}
	definition, err := tool.NewDefinition("shell", "Run a command.", schema, tool.EffectMutating, tool.ReplayUnsafe, declaredCapabilities...)
	if err != nil {
		t.Fatal(err)
	}
	if declaredCapabilities[0] != tool.CapabilityProcessExecute || declaredCapabilities[1] != tool.CapabilityEnvironmentRead {
		t.Fatal("constructor mutated capability declaration input")
	}
	schema[2] = 'X'
	capabilities := definition.Capabilities()
	capabilities[0] = tool.CapabilitySecretsRead
	if definition.Capabilities()[0] != tool.CapabilityEnvironmentRead ||
		definition.Capabilities()[1] != tool.CapabilityProcessExecute ||
		!json.Valid(definition.InputSchema()) || definition.SizeBytes() == 0 {
		t.Fatal("definition is mutable")
	}
	if definition.Name() != "shell" || definition.Description() != "Run a command." ||
		definition.Effect() != tool.EffectMutating || definition.ReplaySafety() != tool.ReplayUnsafe ||
		definition.Validate() != nil || definition.Clone().Validate() != nil {
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
	if result.IsZero() || !(tool.Result{}).IsZero() {
		t.Fatal("result zero-state contract is wrong")
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
	base, _ := tool.NewDefinition("shell", "Run a command.", json.RawMessage(`{"type":"object"}`), tool.EffectMutating, tool.ReplayUnsafe, tool.CapabilityProcessExecute, tool.CapabilityEnvironmentRead)
	reordered, _ := tool.NewDefinition("shell", "Run a command.", json.RawMessage(`{"type":"object"}`), tool.EffectMutating, tool.ReplayUnsafe, tool.CapabilityEnvironmentRead, tool.CapabilityProcessExecute)
	if base.Fingerprint() == "" || base.Fingerprint() != reordered.Fingerprint() {
		t.Fatal("fingerprint is empty or capability-order dependent")
	}
	if !slices.Equal(base.Capabilities(), reordered.Capabilities()) ||
		!slices.IsSorted(base.Capabilities()) {
		t.Fatal("capability set is not canonically ordered")
	}
	for _, changed := range []tool.Definition{
		mustMutatingDefinition(t, "other", "Run a command.", `{"type":"object"}`, tool.ReplayUnsafe, tool.CapabilityProcessExecute, tool.CapabilityEnvironmentRead),
		mustMutatingDefinition(t, "shell", "Run something else.", `{"type":"object"}`, tool.ReplayUnsafe, tool.CapabilityProcessExecute, tool.CapabilityEnvironmentRead),
		mustMutatingDefinition(t, "shell", "Run a command.", `{"type":"string"}`, tool.ReplayUnsafe, tool.CapabilityProcessExecute, tool.CapabilityEnvironmentRead),
		mustMutatingDefinition(t, "shell", "Run a command.", `{"type":"object"}`, tool.ReplayUnsafe, tool.CapabilityProcessExecute),
		mustMutatingDefinition(t, "shell", "Run a command.", `{"type":"object"}`, tool.ReplayIdempotent, tool.CapabilityProcessExecute, tool.CapabilityEnvironmentRead),
	} {
		if changed.Fingerprint() == base.Fingerprint() {
			t.Fatal("changed contract retained its fingerprint")
		}
	}
	mutating, _ := tool.NewDefinition("read", "Read.", json.RawMessage(`{}`), tool.EffectMutating, tool.ReplayIdempotent, tool.CapabilityEnvironmentRead)
	readOnly, _ := tool.NewDefinition("read", "Read.", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe, tool.CapabilityEnvironmentRead)
	if mutating.Fingerprint() == readOnly.Fingerprint() {
		t.Fatal("effect change retained its fingerprint")
	}
}

func mustMutatingDefinition(t *testing.T, name, description, schema string, replaySafety tool.ReplaySafety, capabilities ...tool.Capability) tool.Definition {
	t.Helper()
	definition, err := tool.NewDefinition(name, description, json.RawMessage(schema), tool.EffectMutating, replaySafety, capabilities...)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestValuesRejectInvalidAndOversizedInput(t *testing.T) {
	for _, capabilities := range [][]tool.Capability{{"unknown"}, {tool.CapabilityNetworkAccess, tool.CapabilityNetworkAccess}} {
		if _, err := tool.NewDefinition("http", "HTTP", json.RawMessage(`{}`), tool.EffectMutating, tool.ReplayUnsafe, capabilities...); err == nil {
			t.Fatalf("capabilities %v succeeded", capabilities)
		}
	}
	if _, err := tool.NewDefinition("", "description", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe); err == nil {
		t.Fatal("empty name succeeded")
	}
	if _, err := tool.NewDefinition("ok", "", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe); err == nil {
		t.Fatal("empty description succeeded")
	}
	if _, err := tool.NewDefinition("ok", "description", json.RawMessage(`{`), tool.EffectReadOnly, tool.ReplaySafe); err == nil {
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

func TestDefinitionRejectsMissingAndInvalidExecutionMetadata(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		effect tool.Effect
		replay tool.ReplaySafety
	}{
		{"", tool.ReplaySafe},
		{tool.Effect("unknown"), tool.ReplaySafe},
		{tool.EffectReadOnly, ""},
		{tool.EffectMutating, tool.ReplaySafety("unknown")},
		{tool.EffectReadOnly, tool.ReplayIdempotent},
		{tool.EffectReadOnly, tool.ReplayUnsafe},
		{tool.EffectMutating, tool.ReplaySafe},
	} {
		if _, err := tool.NewDefinition("read", "Read", json.RawMessage(`{}`), test.effect, test.replay); err == nil {
			t.Errorf("effect=%q replay=%q succeeded", test.effect, test.replay)
		}
	}
	for _, capability := range []tool.Capability{
		tool.CapabilityFilesystemWrite,
		tool.CapabilityProcessExecute,
		tool.CapabilityNetworkAccess,
		tool.CapabilityEnvironmentWrite,
	} {
		if _, err := tool.NewDefinition(
			"read",
			"Read",
			json.RawMessage(`{}`),
			tool.EffectReadOnly,
			tool.ReplaySafe,
			capability,
		); err == nil {
			t.Errorf("read-only mutation capability %q succeeded", capability)
		}
	}
	if _, err := tool.NewDefinition(
		"read",
		"Read",
		json.RawMessage(`{}`),
		tool.EffectReadOnly,
		tool.ReplaySafe,
		tool.CapabilityFilesystemRead,
		tool.CapabilityEnvironmentRead,
		tool.CapabilitySecretsRead,
	); err != nil {
		t.Fatalf("read-only capabilities were rejected: %v", err)
	}
}

func TestExecutionErrorIsBoundedCorrelatedAndCancellationCompatible(t *testing.T) {
	t.Parallel()
	failure, err := tool.NewExecutionError("call-1", tool.ExecutionDefinitive, tool.RetryAllowed, context.Canceled)
	if err != nil {
		t.Fatal(err)
	}
	if failure.CallID() != "call-1" || failure.State() != tool.ExecutionDefinitive ||
		failure.RetryDisposition() != tool.RetryAllowed || !errors.Is(failure, context.Canceled) ||
		failure.Validate() != nil {
		t.Fatalf("execution failure = %#v, %v", failure, err)
	}
	uncertain, err := tool.NewExecutionError("call-2", tool.ExecutionUncertain, tool.RetryNever, errors.New("commit acknowledgement lost"))
	if err != nil || uncertain.State() != tool.ExecutionUncertain {
		t.Fatalf("uncertain failure = %#v, %v", uncertain, err)
	}
	for _, test := range []struct {
		callID tool.CallID
		state  tool.ExecutionState
		retry  tool.RetryDisposition
		cause  error
	}{
		{"", tool.ExecutionDefinitive, tool.RetryNever, errors.New("failure")},
		{"call", "", tool.RetryNever, errors.New("failure")},
		{"call", tool.ExecutionDefinitive, "", errors.New("failure")},
		{"call", tool.ExecutionUncertain, tool.RetryAllowed, errors.New("failure")},
		{"call", tool.ExecutionDefinitive, tool.RetryNever, nil},
		{"call", tool.ExecutionDefinitive, tool.RetryNever, errors.New(strings.Repeat("x", tool.MaximumExecutionErrorBytes+1))},
		{"call", tool.ExecutionDefinitive, tool.RetryNever, failure},
	} {
		if _, constructErr := tool.NewExecutionError(test.callID, test.state, test.retry, test.cause); constructErr == nil {
			t.Errorf("invalid execution error %#v succeeded", test)
		}
	}
	var nilFailure *tool.ExecutionError
	if nilFailure.Validate() == nil || nilFailure.Error() == "" || nilFailure.Unwrap() != nil ||
		nilFailure.CallID() != "" || nilFailure.State() != "" || nilFailure.RetryDisposition() != "" {
		t.Fatal("nil execution error is not defensive")
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

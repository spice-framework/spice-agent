package agent_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

func TestToolStartedOccurrenceRoundTripsOnlyBoundedNonSecretFacts(t *testing.T) {
	definition, err := tool.NewDefinition(
		"read", "SECRET description and C:/private/path", json.RawMessage(`{"secret":"value"}`),
		tool.EffectReadOnly, tool.ReplaySafe,
		tool.CapabilityEnvironmentRead, tool.CapabilityFilesystemRead,
	)
	if err != nil {
		t.Fatal(err)
	}
	planID, _ := stage.NewPlanID("generation:occurrence")
	plan, err := agent.NewPlanIdentity(
		[]string{"provider:test"}, "occurrence:v1", testWorkspaceFingerprint,
		planID, []tool.Definition{definition},
	)
	if err != nil {
		t.Fatal(err)
	}
	occurrence, err := agent.NewToolStartedOccurrence("call-1", "read", true, true, &definition, plan, 3)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := occurrence.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > agent.MaximumToolStartedOccurrenceBytes || bytes.Contains(encoded, []byte("SECRET")) ||
		bytes.Contains(encoded, []byte("private")) || bytes.Contains(encoded, []byte("secret")) {
		t.Fatalf("unsafe occurrence payload = %s", encoded)
	}
	decoded, err := agent.DecodeToolStartedOccurrence(encoded)
	if err != nil {
		t.Fatal(err)
	}
	wantCapabilities := []tool.Capability{tool.CapabilityEnvironmentRead, tool.CapabilityFilesystemRead}
	if decoded.CallID() != "call-1" || decoded.Name() != "read" || !decoded.Declared() || !decoded.Executable() ||
		decoded.DefinitionFingerprint() != definition.Fingerprint() || decoded.Effect() != tool.EffectReadOnly ||
		decoded.ReplaySafety() != tool.ReplaySafe || !slices.Equal(decoded.Capabilities(), wantCapabilities) ||
		decoded.ToolPlanID() != planID || decoded.PlanFingerprint() != plan.Fingerprint() ||
		decoded.WorkspaceFingerprint() != testWorkspaceFingerprint || decoded.Turn() != 3 {
		t.Fatalf("decoded occurrence = %#v", decoded)
	}
	capabilities := decoded.Capabilities()
	capabilities[0] = tool.CapabilityNetworkAccess
	if slices.Equal(decoded.Capabilities(), capabilities) {
		t.Fatal("occurrence capabilities were mutable")
	}
	reencoded, err := decoded.Encode()
	if err != nil || !bytes.Equal(encoded, reencoded) {
		t.Fatalf("canonical re-encode = %s, %v", reencoded, err)
	}
}

func TestToolStartedOccurrenceRejectsCorruptionMissingUnknownAndOversize(t *testing.T) {
	valid := validToolStartedOccurrenceBytes(t)
	withoutCapabilities := bytes.Replace(valid, []byte(`"capabilities":[],`), nil, 1)
	unknown := bytes.Replace(valid, []byte(`}`), []byte(`,"SECRET_unknown":true}`), 1)
	duplicate := bytes.Replace(
		valid, []byte(`{"version":`),
		[]byte(`{"version":"spice.agent.tool-started/v1alpha1","version":`), 1,
	)
	wrongVersion := bytes.Replace(valid, []byte(agent.ToolStartedOccurrenceVersion), []byte("unknown-version"), 1)
	for name, payload := range map[string][]byte{
		"empty":             nil,
		"corrupt":           []byte(`{`),
		"not object":        []byte(`[]`),
		"missing":           withoutCapabilities,
		"unknown":           unknown,
		"duplicate":         duplicate,
		"version":           wrongVersion,
		"null capabilities": bytes.Replace(valid, []byte(`"capabilities":[]`), []byte(`"capabilities":null`), 1),
		"trailing":          append(append([]byte(nil), valid...), []byte(` {}`)...),
		"oversize":          bytes.Repeat([]byte{'x'}, agent.MaximumToolStartedOccurrenceBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := agent.DecodeToolStartedOccurrence(payload); err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("decode = %v", err)
			}
		})
	}
}

func TestToolStartedOccurrenceRejectsContradictoryDeclarationFacts(t *testing.T) {
	planID, _ := stage.NewPlanID("generation:occurrence")
	plan, _ := agent.NewPlanIdentity([]string{"provider:test"}, "occurrence:v1", testWorkspaceFingerprint, planID, nil)
	definition, _ := tool.NewDefinition(
		"read", "Read.", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe,
	)
	other, _ := tool.NewDefinition(
		"other", "Other.", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe,
	)
	for name, test := range map[string]struct {
		declared   bool
		executable bool
		definition *tool.Definition
	}{
		"executable undeclared": {declared: false, executable: true},
		"missing definition":    {declared: true, executable: true},
		"unknown with metadata": {definition: &definition},
		"wrong definition":      {declared: true, executable: true, definition: &other},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := agent.NewToolStartedOccurrence(
				"call", "read", test.declared, test.executable, test.definition, plan, 1,
			); err == nil {
				t.Fatal("contradictory occurrence succeeded")
			}
		})
	}
	unknown, err := agent.NewToolStartedOccurrence("call", "missing", false, false, nil, plan, 1)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := unknown.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := agent.DecodeToolStartedOccurrence(encoded)
	if err != nil || decoded.Declared() || decoded.Executable() || decoded.DefinitionFingerprint() != "" || len(decoded.Capabilities()) != 0 {
		t.Fatalf("unknown occurrence = %#v, %v", decoded, err)
	}
}

func FuzzDecodeToolStartedOccurrence(f *testing.F) {
	f.Add(validToolStartedOccurrenceBytes(f))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, payload []byte) {
		occurrence, err := agent.DecodeToolStartedOccurrence(payload)
		if err != nil {
			return
		}
		encoded, err := occurrence.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = agent.DecodeToolStartedOccurrence(encoded); err != nil {
			t.Fatal(err)
		}
	})
}

type testOrFuzz interface {
	Helper()
	Fatal(...any)
}

func validToolStartedOccurrenceBytes(t testOrFuzz) []byte {
	t.Helper()
	definition, err := tool.NewDefinition(
		"read", "Read.", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe,
	)
	if err != nil {
		t.Fatal(err)
	}
	planID, _ := stage.NewPlanID("generation:occurrence")
	plan, err := agent.NewPlanIdentity([]string{"provider:test"}, "occurrence:v1", testWorkspaceFingerprint, planID, []tool.Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	occurrence, err := agent.NewToolStartedOccurrence("call", "read", true, true, &definition, plan, 1)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := occurrence.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

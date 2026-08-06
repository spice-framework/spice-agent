package annotationtool

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/annotation/agenttool"
	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/spice/annotation/sdk/protocol"
)

func TestToolIdentityDescriptionAndTypedDispatch(t *testing.T) {
	t.Parallel()
	implementation := New()
	identity, err := implementation.Initialize(t.Context(), protocol.InitializeParams{
		Protocol: sdk.ProtocolV1Alpha2,
		ToolPath: agenttool.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.Protocol != sdk.ProtocolV1Alpha2 || identity.ToolPath != agenttool.Path || identity.ModulePath != modulePath {
		t.Fatalf("identity = %#v", identity)
	}
	description, err := implementation.Describe(t.Context(), protocol.DescribeParams{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(description.DescriptorPackages, []string{descriptorPackage}) || len(description.Handlers) != 3 {
		t.Fatalf("description = %#v", description)
	}
	names := make([]string, len(description.Handlers))
	for index, handler := range description.Handlers {
		names[index] = handler.Descriptor.Name
		if handler.Descriptor.Package != descriptorPackage ||
			!slices.Equal(handler.Capabilities, []string{metadataCapability, providerCapability}) {
			t.Fatalf("handler = %#v", handler)
		}
	}
	if !slices.Equal(names, []string{"ModelProvider", "Stage", "Tool"}) {
		t.Fatalf("handler order = %v", names)
	}
	description.Handlers[0].Capabilities[0] = "mutated"
	again, _ := implementation.Describe(t.Context(), protocol.DescribeParams{})
	if again.Handlers[0].Capabilities[0] == "mutated" {
		t.Fatal("Describe exposed mutable tool state")
	}

	result, err := implementation.Analyze(t.Context(), protocol.AnalyzeParams{
		Descriptor: sdk.Symbol{Package: descriptorPackage, Name: "Tool"},
		Invocation: toolInvocation(),
	})
	if err != nil || len(result.Contributions) != 2 {
		t.Fatalf("Analyze = %#v, %v", result, err)
	}
	for index, wire := range result.Contributions {
		decoded, decodeErr := protocol.DecodeContribution(wire)
		if decodeErr != nil {
			t.Fatalf("contribution %d: %v", index, decodeErr)
		}
		if decoded.Kind != []sdk.ContributionKind{sdk.ContributionProvider, sdk.ContributionBeanMetadata}[index] {
			t.Fatalf("contribution %d kind = %s", index, decoded.Kind)
		}
	}
	if err = implementation.Shutdown(t.Context(), protocol.ShutdownParams{}); err != nil {
		t.Fatal(err)
	}
}

func TestToolFailsClosedOnIdentityDispatchAndCancellation(t *testing.T) {
	t.Parallel()
	var nilTool *Tool
	var nilContext context.Context
	if _, err := nilTool.Initialize(t.Context(), protocol.InitializeParams{}); err == nil ||
		func() bool {
			_, innerErr := New().Describe(nilContext, protocol.DescribeParams{})
			return innerErr == nil
		}() ||
		func() bool {
			_, innerErr := nilTool.Analyze(t.Context(), protocol.AnalyzeParams{})
			return innerErr == nil
		}() ||
		nilTool.Shutdown(t.Context(), protocol.ShutdownParams{}) == nil {
		t.Fatal("nil tool or context operation succeeded")
	}
	implementation := New()
	for _, params := range []protocol.InitializeParams{
		{Protocol: sdk.ProtocolVersion("spice.annotation/v1alpha1"), ToolPath: agenttool.Path},
		{Protocol: sdk.ProtocolV1Alpha2, ToolPath: "example.com/wrong"},
	} {
		if _, err := implementation.Initialize(t.Context(), params); err == nil {
			t.Fatalf("Initialize(%#v) succeeded", params)
		}
	}
	invocation := toolInvocation()
	if _, err := implementation.Analyze(t.Context(), protocol.AnalyzeParams{
		Descriptor: sdk.Symbol{Package: descriptorPackage, Name: "Stage"},
		Invocation: invocation,
	}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched dispatch = %v", err)
	}
	missing := invocation
	missing.DescriptorPackage = "example.com/missing"
	missing.DescriptorSymbol = "Missing"
	if _, err := implementation.Analyze(t.Context(), protocol.AnalyzeParams{
		Descriptor: sdk.Symbol{Package: "example.com/missing", Name: "Missing"},
		Invocation: missing,
	}); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("missing dispatch = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := implementation.Analyze(ctx, protocol.AnalyzeParams{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Analyze cancellation = %v", err)
	}
	if err := implementation.Shutdown(ctx, protocol.ShutdownParams{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown cancellation = %v", err)
	}
}

func TestHandlerRegistrationsAreDeterministicAndValid(t *testing.T) {
	t.Parallel()
	first := handlerRegistrations()
	second := handlerRegistrations()
	if len(first) != len(second) {
		t.Fatal("handler registration count changed between calls")
	}
	seen := make(map[string]struct{}, len(first))
	for index, registration := range first {
		if registration.description.Descriptor != second[index].description.Descriptor ||
			!slices.Equal(registration.description.Capabilities, second[index].description.Capabilities) {
			t.Fatalf("handler registration %d changed between calls", index)
		}
		key := symbolKey(registration.description.Descriptor)
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("duplicate registration %q", key)
		}
		seen[key] = struct{}{}
		if registration.handle == nil {
			t.Fatalf("registration %q has nil handler", key)
		}
	}
}

func toolInvocation() sdk.Invocation {
	name, _ := json.Marshal("read")
	return sdk.Invocation{
		DescriptorPackage: descriptorPackage,
		DescriptorSymbol:  "Tool",
		CanonicalName:     "agent.Tool",
		Arguments: []sdk.InvocationArgument{{
			Name: "name", Kind: sdk.KindString, Value: name,
		}},
		Declaration: sdk.Declaration{
			Target:      sdk.TargetFunction,
			SymbolID:    "example.com/app.NewRead",
			Name:        "NewRead",
			PackagePath: "example.com/app",
			TypeID:      "func() (github.com/spice-framework/spice-agent/tool.Tool, error)",
		},
		Facts: map[string]string{"symbol_kind": "function"},
	}
}

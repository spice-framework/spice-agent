package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	agentannotation "github.com/spice-framework/spice-agent/annotation/agent"
	"github.com/spice-framework/spice-agent/annotation/agenttool"
	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/spice/annotation/sdk/sdktest"
)

const descriptorPackage = "github.com/spice-framework/spice-agent/annotation/agent"

func TestDescriptorsExposeCanonicalTypedHandlers(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		symbol     string
		definition sdk.Definition
	}{
		{"ModelProvider", agentannotation.ModelProvider()},
		{"Stage", agentannotation.Stage()},
		{"Tool", agentannotation.Tool()},
	} {
		t.Run(test.symbol, func(t *testing.T) {
			t.Parallel()
			if err := test.definition.Validate(); err != nil {
				t.Fatal(err)
			}
			if test.definition.Name != "agent."+test.symbol ||
				test.definition.Implementation.Tool != agenttool.Path ||
				test.definition.Implementation.Protocol != sdk.ProtocolV1Alpha2 ||
				test.definition.Implementation.Handler == nil ||
				!reflect.DeepEqual(test.definition.Targets, []sdk.Target{sdk.TargetFunction}) {
				t.Fatalf("descriptor = %#v", test.definition)
			}
			nameFound := false
			for _, argument := range test.definition.Arguments {
				if argument.Name == "name" && argument.Required {
					nameFound = true
				}
			}
			if !nameFound || len(test.definition.Examples) == 0 || !strings.Contains(test.definition.Examples[0].Code, "func New") {
				t.Fatal("descriptor omitted required name or factory example")
			}
		})
	}
}

func TestHandlersContributeOnlyGenericProviderMetadataDeterministically(t *testing.T) {
	for _, test := range []struct {
		symbol     string
		typeID     string
		definition sdk.Definition
	}{
		{"ModelProvider", "func(config example.com/agent.Config) github.com/spice-framework/spice-agent/model.Provider", agentannotation.ModelProvider()},
		{"Stage", "func(config example.com/agent.Config) github.com/spice-framework/spice-agent/stage.Stage[example.com/agent.Input, example.com/agent.Output]", agentannotation.Stage()},
		{"Tool", "func(config example.com/agent.Config) github.com/spice-framework/spice-agent/tool.Tool", agentannotation.Tool()},
	} {
		t.Run(test.symbol, func(t *testing.T) {
			invocation := validInvocation(
				test.symbol, test.typeID,
				argument("name", sdk.KindString, "official.default"),
				argument("aliases", sdk.KindList, []string{"default-alias"}),
				argument("qualifiers", sdk.KindList, []string{"official", "default"}),
				argument("fallback", sdk.KindBoolean, true),
				argument("order", sdk.KindInteger, int64(-100)),
			)
			sdktest.RunHandlerCases(
				t, test.definition,
				sdktest.HandlerCase{
					Name:       "generic contributions",
					Invocation: invocation,
					WantKinds: []sdk.ContributionKind{
						sdk.ContributionProvider,
						sdk.ContributionBeanMetadata,
					},
					Check: func(t *testing.T, result sdk.Result) {
						t.Helper()
						provider := result.Contributions[0].Provider
						metadata := result.Contributions[1].BeanMetadata
						if provider.Name != "official.default" || !slices.Equal(provider.Aliases, []string{"default-alias"}) {
							t.Fatalf("provider = %#v", provider)
						}
						if !metadata.Fallback || metadata.Primary || metadata.Order == nil || *metadata.Order != -100 ||
							!slices.Equal(metadata.Qualifiers, []string{"official", "default"}) {
							t.Fatalf("metadata = %#v", metadata)
						}
					},
				},
				sdktest.HandlerCase{
					Name:              "cancellation",
					Invocation:        invocation,
					Canceled:          true,
					WantErrorContains: context.Canceled.Error(),
				},
			)
		})
	}
}

func TestModelProviderSelectionIsNeverImplicit(t *testing.T) {
	invocation := validInvocation(
		"ModelProvider",
		"func() github.com/spice-framework/spice-agent/model.Provider",
		argument("name", sdk.KindString, "openai-responses"),
	)
	result, err := agentannotation.ModelProviderHandler(t.Context(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	metadata := result.Contributions[1].BeanMetadata
	if metadata.Fallback || metadata.Primary || metadata.Order != nil || len(metadata.Qualifiers) != 0 {
		t.Fatalf("implicit selection metadata = %#v", metadata)
	}
}

func TestHandlersPreserveStandardProviderCleanupAndErrorForms(t *testing.T) {
	for _, typeID := range []string{
		"func() (github.com/spice-framework/spice-agent/tool.Tool, error)",
		"func() (github.com/spice-framework/spice-agent/tool.Tool, github.com/spice-framework/spice/lifecycle.Cleanup)",
		"func() (github.com/spice-framework/spice-agent/tool.Tool, github.com/spice-framework/spice/lifecycle.Cleanup, error)",
		"func() (result github.com/spice-framework/spice-agent/tool.Tool, cleanup github.com/spice-framework/spice/lifecycle.Cleanup, err error)",
		"func() example.com/app.ToolAlias",
	} {
		invocation := validInvocation("Tool", typeID, argument("name", sdk.KindString, "read"))
		if _, err := agentannotation.ToolHandler(t.Context(), invocation); err != nil {
			t.Fatalf("ToolHandler(%q) = %v", typeID, err)
		}
	}
	stageInvocation := validInvocation(
		"Stage",
		"func() (github.com/spice-framework/spice-agent/stage.Stage[example.com/Input, example.com/Output], error)",
		argument("name", sdk.KindString, "transform"),
	)
	if _, err := agentannotation.StageHandler(t.Context(), stageInvocation); err != nil {
		t.Fatalf("StageHandler(cleanup/error form) = %v", err)
	}
}

func TestHandlersRejectInvalidTargetsArgumentsAndSelection(t *testing.T) {
	valid := validInvocation(
		"Tool",
		"func() github.com/spice-framework/spice-agent/tool.Tool",
		argument("name", sdk.KindString, "read"),
	)
	for _, test := range []struct {
		name       string
		invocation sdk.Invocation
		contains   string
	}{
		{"wrong descriptor", withDescriptor(valid, "Stage"), "received descriptor"},
		{"unexported", withDeclarationName(valid, "newRead"), "must be exported"},
		{"method", withFact(valid, "receiver", "*Reader"), "must not be a method"},
		{"no result", withType(valid, "func()"), "must return a provider value"},
		{"missing name", validInvocation("Tool", "func() github.com/spice-framework/spice-agent/tool.Tool"), "name\" is required"},
		{"noncanonical name", withArguments(valid, argument("name", sdk.KindString, "Read Tool")), "not canonical"},
		{"duplicate alias", withArguments(valid, argument("name", sdk.KindString, "read"), argument("aliases", sdk.KindList, []string{"reader", "reader"})), "duplicated"},
		{"alias equals name", withArguments(valid, argument("name", sdk.KindString, "read"), argument("aliases", sdk.KindList, []string{"read"})), "duplicates its name"},
		{"duplicate qualifier", withArguments(valid, argument("name", sdk.KindString, "read"), argument("qualifiers", sdk.KindList, []string{"coding", "coding"})), "duplicated"},
		{"primary fallback", withArguments(valid, argument("name", sdk.KindString, "read"), argument("primary", sdk.KindBoolean, true), argument("fallback", sdk.KindBoolean, true)), "both primary and fallback"},
		{"order high", withArguments(valid, argument("name", sdk.KindString, "read"), argument("order", sdk.KindInteger, int64(1_000_001))), "between -1000000 and 1000000"},
		{"unsupported", withArguments(valid, argument("name", sdk.KindString, "read"), argument("mystery", sdk.KindString, "x")), "unsupported argument"},
		{"duplicate argument", withArguments(valid, argument("name", sdk.KindString, "read"), argument("name", sdk.KindString, "write")), "repeats argument"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := agentannotation.ToolHandler(t.Context(), test.invocation)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want %q", err, test.contains)
			}
		})
	}
	var nilContext context.Context
	if _, err := agentannotation.ToolHandler(nilContext, valid); err == nil {
		t.Fatal("nil handler context succeeded")
	}
}

func TestStageAcceptsNarrowExactApplicationInterface(t *testing.T) {
	invocation := validInvocation(
		"Stage",
		"func(dependency func(context.Context) error) example.com/app.PromptStage",
		argument("name", sdk.KindString, "prompt"),
	)
	if _, err := agentannotation.StageHandler(t.Context(), invocation); err != nil {
		t.Fatal(err)
	}
}

func TestOrderAcceptsPublishedInclusiveBounds(t *testing.T) {
	for _, order := range []int64{-1_000_000, 1_000_000} {
		invocation := validInvocation(
			"Stage",
			"func() example.com/app.StageAlias",
			argument("name", sdk.KindString, "stage"),
			argument("order", sdk.KindInteger, order),
		)
		result, err := agentannotation.StageHandler(t.Context(), invocation)
		if err != nil {
			t.Fatalf("order %d: %v", order, err)
		}
		if got := result.Contributions[1].BeanMetadata.Order; got == nil || *got != order {
			t.Fatalf("order %d contribution = %v", order, got)
		}
	}
}

func validInvocation(symbol, typeID string, arguments ...sdk.InvocationArgument) sdk.Invocation {
	return sdk.Invocation{
		DescriptorPackage: descriptorPackage,
		DescriptorSymbol:  symbol,
		CanonicalName:     "agent." + symbol,
		Arguments:         arguments,
		Declaration: sdk.Declaration{
			Target:      sdk.TargetFunction,
			SymbolID:    "example.com/app.New" + symbol,
			Name:        "New" + symbol,
			PackagePath: "example.com/app",
			TypeID:      typeID,
		},
		Facts: map[string]string{"symbol_kind": "function"},
	}
}

func argument(name string, kind sdk.Kind, value any) sdk.InvocationArgument {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return sdk.InvocationArgument{Name: name, Kind: kind, Value: encoded}
}

func withDescriptor(invocation sdk.Invocation, symbol string) sdk.Invocation {
	invocation.DescriptorSymbol = symbol
	return invocation
}

func withDeclarationName(invocation sdk.Invocation, name string) sdk.Invocation {
	invocation.Declaration.Name = name
	return invocation
}

func withType(invocation sdk.Invocation, typeID string) sdk.Invocation {
	invocation.Declaration.TypeID = typeID
	return invocation
}

func withFact(invocation sdk.Invocation, name, value string) sdk.Invocation {
	invocation.Facts = map[string]string{name: value}
	return invocation
}

func withArguments(invocation sdk.Invocation, arguments ...sdk.InvocationArgument) sdk.Invocation {
	invocation.Arguments = arguments
	return invocation
}

func TestHandlersPreserveContextCancellationIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := agentannotation.ModelProviderHandler(ctx, validInvocation(
		"ModelProvider",
		"func() github.com/spice-framework/spice-agent/model.Provider",
		argument("name", sdk.KindString, "provider"),
	))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func FuzzToolHandler(f *testing.F) {
	f.Add("read", int64(0))
	f.Add("official.read", int64(-1_000_000))
	f.Fuzz(func(t *testing.T, name string, order int64) {
		invocation := validInvocation(
			"Tool",
			"func() github.com/spice-framework/spice-agent/tool.Tool",
			argument("name", sdk.KindString, name),
			argument("order", sdk.KindInteger, order),
		)
		result, err := agentannotation.ToolHandler(t.Context(), invocation)
		if err != nil {
			return
		}
		for index, contribution := range result.Contributions {
			if validationErr := contribution.Validate(); validationErr != nil {
				t.Fatalf("contribution %d: %v", index, validationErr)
			}
		}
	})
}

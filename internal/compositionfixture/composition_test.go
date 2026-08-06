package compositionfixture_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/spice-framework/spice-agent/internal/compositionfixture"
	spicegen "github.com/spice-framework/spice-agent/internal/spicegen/compositionproof"
	"github.com/spice-framework/spice/bean"
	"github.com/spice-framework/spice/lifecycle"
)

func TestGeneratedCompositionSelectsAndOrdersExactInterfaceBeans(t *testing.T) {
	t.Parallel()
	application, err := spicegen.NewApplication(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	proof := application.Components().Proof
	if proof.ProviderName() != "beta" {
		t.Fatalf("selected provider = %q, want beta", proof.ProviderName())
	}
	processed, err := proof.Process(context.Background(), "  hello  ")
	if err != nil {
		t.Fatal(err)
	}
	if processed != "hello!" {
		t.Fatalf("ordered stage output = %q, want hello!", processed)
	}
	fallback, replaced := proof.StageSelections()
	if fallback != "fallback-only" || replaced != "replaceable-normal" {
		t.Fatalf("stage selections = %q, %q", fallback, replaced)
	}
	if names := proof.ToolNames(); !slices.Equal(names, []string{"read", "write"}) {
		t.Fatalf("canonical tool map names = %v", names)
	}
	if selected := proof.AliasSelectedName(); selected != "read" {
		t.Fatalf("alias-selected tool = %q, want read", selected)
	}
	if events := proof.CleanupEvents(); len(events) != 0 {
		t.Fatalf("cleanup before stop = %v", events)
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if events := proof.CleanupEvents(); !slices.Equal(events, []string{"write", "read"}) {
		t.Fatalf("reverse cleanup = %v", events)
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if events := proof.CleanupEvents(); !slices.Equal(events, []string{"write", "read"}) {
		t.Fatalf("idempotent cleanup = %v", events)
	}
}

func TestGeneratedCompositionSupportsTypedApplicationOverride(t *testing.T) {
	t.Parallel()
	application, err := spicegen.NewApplicationWithOptions(
		context.Background(),
		spicegen.ApplicationOptions{
			Overrides: spicegen.BeanOverrides{
				Beta: bean.Replace(compositionfixture.NewProviderStub("test")),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if stopErr := application.Stop(context.Background()); stopErr != nil {
			t.Error(stopErr)
		}
	})
	if selected := application.Components().Proof.ProviderName(); selected != "test" {
		t.Fatalf("test override selected provider = %q", selected)
	}
}

func TestGeneratedCompositionRollsBackConstructedCleanupOnFactoryFailure(t *testing.T) {
	t.Parallel()
	cleanup := compositionfixture.NewCleanupLog()
	failure := errors.New("injected write failure")
	application, err := spicegen.NewApplicationWithOptions(
		context.Background(),
		spicegen.ApplicationOptions{
			Overrides: spicegen.BeanOverrides{
				Cleanup: bean.Replace(cleanup),
				Write: bean.ReplaceFactory(func(context.Context) (
					compositionfixture.ToolAlias,
					lifecycle.Cleanup,
					error,
				) {
					return nil, nil, failure
				}),
			},
		},
	)
	if application != nil || !errors.Is(err, failure) {
		t.Fatalf("application, error = %#v, %v", application, err)
	}
	if events := cleanup.Snapshot(); !slices.Equal(events, []string{"read"}) {
		t.Fatalf("rollback cleanup = %v, want [read]", events)
	}
}

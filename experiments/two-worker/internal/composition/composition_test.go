package composition_test

import (
	"context"
	"testing"

	twoworker "github.com/spice-framework/spice-agent/experiments/two-worker"
	spicegen "github.com/spice-framework/spice-agent/experiments/two-worker/internal/spicegen/twoworkerproof"
)

func TestGeneratedApplicationInjectsDelegateTool(t *testing.T) {
	t.Parallel()
	application, err := spicegen.NewApplication(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if name := application.Components().Proof.DelegateName(); name != twoworker.ToolName {
		t.Fatalf("generated delegate name = %q", name)
	}
	if err = application.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

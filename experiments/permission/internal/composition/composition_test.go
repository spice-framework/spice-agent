package composition_test

import (
	"context"
	"testing"

	spicegen "github.com/spice-framework/spice-agent/experiments/permission/internal/spicegen/permissionproof"
)

func TestGeneratedApplicationInjectsPolicyGuardCollection(t *testing.T) {
	t.Parallel()
	application, err := spicegen.NewApplication(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if count := application.Components().Proof.GuardCount(); count != 1 {
		t.Fatalf("generated guard count = %d, want 1", count)
	}
	if err = application.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

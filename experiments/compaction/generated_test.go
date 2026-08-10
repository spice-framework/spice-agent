package compaction_test

import (
	"testing"

	spicegen "github.com/spice-framework/spice-agent/experiments/compaction/internal/spicegen/compactionproof"
)

func TestGeneratedApplicationConstructsExplicitProviderWrapper(t *testing.T) {
	application, err := spicegen.NewApplication(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !application.Components().Proof.ProviderPresent() || application.Components().CompactedModelProvider == nil {
		t.Fatal("generated application did not inject the explicit compaction provider")
	}
	if err = application.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

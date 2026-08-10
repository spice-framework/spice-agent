package gitworkflow_test

import (
	"slices"
	"testing"

	gitworkflow "github.com/spice-framework/spice-agent/experiments/git-workflow"
	spicegen "github.com/spice-framework/spice-agent/experiments/git-workflow/internal/spicegen/gitworkflowproof"
)

func TestGeneratedApplicationInjectsToolsAndCommitGuard(t *testing.T) {
	t.Parallel()
	application, err := spicegen.NewApplication(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	proof := application.Components().Proof
	names := proof.ToolNames()
	slices.Sort(names)
	want := []string{gitworkflow.CommitStagedToolName, gitworkflow.InspectToolName}
	if !slices.Equal(names, want) || proof.GuardCount() != 1 {
		t.Fatalf("generated tools/guards = %q/%d, want %q/1", names, proof.GuardCount(), want)
	}
	if err = application.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

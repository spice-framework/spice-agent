package stage_test

import (
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/stage"
)

func testToolDispatchScope(t *testing.T) stage.ToolDispatchScope {
	t.Helper()
	return testToolDispatchScopeForPlan(t, stage.PlanID("generation:test"))
}

func TestToolDispatchScopeValidatesEveryAuthorityFact(t *testing.T) {
	authority, _ := interaction.NewScope("run-test")
	valid := func(runID string, turn uint32, planID stage.PlanID, plan, workspace string, scope interaction.Scope) error {
		_, err := stage.NewToolDispatchScope(runID, turn, planID, plan, workspace, scope)
		return err
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if err := valid("run-test", 2, "generation:test", digest, digest, authority); err != nil {
		t.Fatal(err)
	}
	otherAuthority, _ := interaction.NewScope("other-run")
	for name, err := range map[string]error{
		"run":         valid(" bad ", 2, "generation:test", digest, digest, authority),
		"turn":        valid("run-test", 0, "generation:test", digest, digest, authority),
		"plan id":     valid("run-test", 2, "", digest, digest, authority),
		"plan digest": valid("run-test", 2, "generation:test", "sha256:bad", digest, authority),
		"workspace":   valid("run-test", 2, "generation:test", digest, "SHA256:"+strings.Repeat("a", 64), authority),
		"authority":   valid("run-test", 2, "generation:test", digest, digest, otherAuthority),
	} {
		if err == nil {
			t.Errorf("%s authority succeeded", name)
		}
	}
	scope := testToolDispatchScope(t)
	if scope.RunID() != "run-test" || scope.Turn() != 1 || scope.ToolPlanID() != "generation:test" ||
		scope.InteractionAuthority().RunID() != "run-test" {
		t.Fatalf("scope accessors = %#v", scope)
	}
}

func testToolDispatchScopeForPlan(t *testing.T, planID stage.PlanID) stage.ToolDispatchScope {
	t.Helper()
	authority, err := interaction.NewScope("run-test")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := stage.NewToolDispatchScope(
		"run-test", 1, planID, "sha256:"+strings.Repeat("0", 64), "", authority,
	)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

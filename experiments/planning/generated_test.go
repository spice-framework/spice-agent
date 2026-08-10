package planning_test

import (
	"bytes"
	"testing"

	"github.com/spice-framework/spice-agent/agent"
	planning "github.com/spice-framework/spice-agent/experiments/planning"
	spicegen "github.com/spice-framework/spice-agent/experiments/planning/internal/spicegen/planningproof"
	"github.com/spice-framework/spice-agent/message"
)

func TestGeneratedApplicationInjectsTypedPlannerAndExplicitService(t *testing.T) {
	application, err := spicegen.NewApplication(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	components := application.Components()
	if components.Proof == nil || components.Proof.Planner == nil || components.Proof.Service == nil ||
		components.Proof.Service != components.PlanningService || components.Proof.Engine != components.PlanningProofEngine {
		t.Fatal("generated graph did not inject exact planner/service/engine beans")
	}
	part, err := message.Text("Plan this worker request.")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := message.New("input", message.RoleUser, part)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := agent.NewDefinition("worker", "scripted", 1)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := components.Proof.Service.Prepare(t.Context(), definition, initial)
	if err != nil {
		t.Fatal(err)
	}
	if components.Proof.Provider.Calls() != 0 || prepared.Plan().Producer() != components.Proof.Planner.Identity() {
		t.Fatal("generated prepare crossed the explicit worker boundary")
	}
	run, err := components.Proof.Service.StartPrepared(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if err = run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := run.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	extracted, found, err := planning.Extract(snapshot)
	if err != nil || !found || !bytes.Equal(extracted.CanonicalJSON(), prepared.Plan().CanonicalJSON()) {
		t.Fatalf("generated snapshot plan found=%t err=%v", found, err)
	}
	if components.Proof.Provider.Calls() != 1 {
		t.Fatalf("provider calls=%d", components.Proof.Provider.Calls())
	}
	if err = application.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

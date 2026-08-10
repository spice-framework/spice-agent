package telemetry_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	spicegen "github.com/spice-framework/spice-agent/experiments/telemetry/internal/spicegen/telemetryproof"
	"github.com/spice-framework/spice-agent/message"
)

func TestGeneratedApplicationDrainsEngineBeforeTelemetry(t *testing.T) {
	application, err := spicegen.NewApplication(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	components := application.Components()
	if components.Proof == nil || components.Proof.Engine != components.TelemetryProofEngine ||
		components.Proof.Processor != components.TelemetryProcessor ||
		components.Proof.Exporter == nil || components.Proof.Health == nil {
		t.Fatal("generated application did not construct exact telemetry dependencies")
	}
	part, err := message.Text("secret prompt must never enter telemetry")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := message.New("input", message.RoleUser, part)
	if err != nil {
		t.Fatal(err)
	}
	input, err := agent.NewInput(initial)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := agent.NewDefinition("proof", "proof-model", 1)
	if err != nil {
		t.Fatal(err)
	}
	run, err := components.Proof.Engine.Start(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-components.Proof.Provider.Started():
	case <-time.After(2 * time.Second):
		t.Fatal("generated engine did not reach the cooperative provider")
	}
	stopContext, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err = application.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	if waitErr := run.Wait(t.Context()); waitErr == nil {
		t.Fatal("cancelled proof run unexpectedly succeeded")
	}
	if order := components.Proof.Lifecycle.Order(); !slices.Equal(order, []string{"engine", "telemetry"}) {
		t.Fatalf("cleanup order=%v", order)
	}
	records, shutdowns := components.Proof.Exporter.Evidence()
	if records == 0 || shutdowns != 1 {
		t.Fatalf("exported records=%d shutdowns=%d", records, shutdowns)
	}
	snapshot := components.Proof.Processor.Snapshot()
	if !snapshot.Closed() || snapshot.Dropped() != 0 || snapshot.OpenCorrelations() != 0 ||
		snapshot.IncompleteSpans() != 0 || snapshot.ExportFailures() != 0 {
		t.Fatalf("final snapshot=%+v", snapshot)
	}
	if reasons := components.Proof.Health.HealthContribution().Reasons(); len(reasons) != 0 {
		t.Fatalf("default telemetry changed readiness: %v", reasons)
	}
}

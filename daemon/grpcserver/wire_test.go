package grpcserver

import (
	"slices"
	"testing"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
)

func TestWireConversionsAreValidatedCanonicalAndIndependent(t *testing.T) {
	t.Parallel()
	build, limits, health, definitions := wireFixtureValues(t)

	wireBuild, err := buildToWire(build)
	if err != nil || wireBuild.GetComponent() != build.Component() || wireBuild.GetCommit() != build.Commit() {
		t.Fatalf("build conversion = %#v, %v", wireBuild, err)
	}
	wireCapabilities, err := capabilitiesToWire([]string{"tools", "events"})
	if err != nil || wireCapabilities == nil ||
		!slices.Equal(wireCapabilities.GetNames(), []string{"events", "tools"}) {
		t.Fatalf("capability conversion = %#v, %v", wireCapabilities, err)
	}
	wireLimits, err := limitsToWire(limits)
	if err != nil || wireLimits.GetMaxMessageBytes() != limits.MessageBytes() ||
		wireLimits.GetMaxActiveRuns() != limits.ActiveRuns() {
		t.Fatalf("limit conversion = %#v, %v", wireLimits, err)
	}
	wireHealth, err := healthToWire(health)
	if err != nil || wireHealth == nil || wireHealth.GetState() != commonv1.HealthState_HEALTH_STATE_DEGRADED ||
		!slices.Equal(wireHealth.GetDegradedReasons(), []string{"authority unavailable"}) {
		t.Fatalf("health conversion = %#v, %v", wireHealth, err)
	}
	wireDefinitions, err := definitionsToWire(definitions, wireLimits)
	if err != nil || wireDefinitions == nil || len(wireDefinitions.GetDefinitions()) != 1 ||
		wireDefinitions.GetRevision() != definitions.Revision() ||
		wireDefinitions.GetDefinitions()[0].GetModel() != "scripted" {
		t.Fatalf("definition conversion = %#v, %v", wireDefinitions, err)
	}

	wireCapabilities.Names[0] = "changed"
	wireHealth.DegradedReasons[0] = "changed"
	wireDefinitions.Definitions[0].Id = "changed"
	secondCapabilities, capabilitiesErr := capabilitiesToWire([]string{"tools", "events"})
	secondHealth, healthErr := healthToWire(health)
	secondDefinitions, definitionsErr := definitionsToWire(definitions, wireLimits)
	if capabilitiesErr != nil || healthErr != nil || definitionsErr != nil || secondCapabilities == nil ||
		secondHealth == nil || secondDefinitions == nil || len(secondCapabilities.GetNames()) != 2 ||
		len(secondHealth.GetDegradedReasons()) != 1 || len(secondDefinitions.GetDefinitions()) != 1 {
		t.Fatal("repeat wire conversions failed")
	}
	if secondCapabilities.GetNames()[0] != "events" || secondHealth.GetDegradedReasons()[0] != "authority unavailable" ||
		secondDefinitions.GetDefinitions()[0].GetId() != "assistant" {
		t.Fatal("wire conversions retained caller mutation")
	}
}

func TestWireConversionsRejectInvalidInputs(t *testing.T) {
	t.Parallel()
	_, limits, _, definitions := wireFixtureValues(t)
	wireLimits, err := limitsToWire(limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = buildToWire(client.Build{}); err == nil {
		t.Fatal("zero build converted")
	}
	if _, err = capabilitiesToWire([]string{"events", "events"}); err == nil {
		t.Fatal("duplicate capabilities converted")
	}
	if _, err = limitsToWire(client.Limits{}); err == nil {
		t.Fatal("zero limits converted")
	}
	if _, err = healthToWire(client.Health{}); err == nil {
		t.Fatal("zero health converted")
	}
	if _, err = definitionsToWire(daemon.DefinitionSet{}, wireLimits); err == nil {
		t.Fatal("zero definitions converted")
	}
	if _, err = definitionsToWire(definitions, &commonv1.Limits{}); err == nil {
		t.Fatal("definitions converted with invalid limits")
	}
	if _, err = healthStateToWire(client.HealthState("unknown")); err == nil {
		t.Fatal("unknown health state converted")
	}
	if err = enginev1.ValidateDefinitionSet(mustWireDefinitions(t, definitions, wireLimits), wireLimits); err != nil {
		t.Fatal(err)
	}
}

func wireFixtureValues(t *testing.T) (client.Build, client.Limits, client.Health, daemon.DefinitionSet) {
	t.Helper()
	build, err := client.NewBuild("spice-agentd", "0.1.0-preview.1", "01234567", "go1.26.5")
	if err != nil {
		t.Fatal(err)
	}
	limits, err := client.NewLimits(1<<20, 64, 128, 1<<20, 8, 16)
	if err != nil {
		t.Fatal(err)
	}
	health, err := client.NewHealth(client.HealthDegraded, []string{"authority unavailable"}, 3, limits)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := agent.NewDefinition("assistant", "scripted", 8)
	if err != nil {
		t.Fatal(err)
	}
	hostDefinition, err := daemon.NewDefinition("assistant", "v1", definition)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := daemon.NewDefinitionSet([]daemon.Definition{hostDefinition})
	if err != nil {
		t.Fatal(err)
	}
	return build, limits, health, definitions
}

func mustWireDefinitions(
	t *testing.T,
	definitions daemon.DefinitionSet,
	limits *commonv1.Limits,
) *enginev1.DefinitionSet {
	t.Helper()
	value, err := definitionsToWire(definitions, limits)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

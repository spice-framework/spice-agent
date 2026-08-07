//go:build 386

package enginev1_test

import (
	"math"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
)

func TestMaximumUint32LimitsDoNotOverflowCountValidation(t *testing.T) {
	t.Parallel()
	maximum := limits(
		4<<20,
		math.MaxUint32,
		math.MaxUint32,
		math.MaxUint32,
		math.MaxUint32,
		math.MaxUint32,
	)
	request := validInitializeRequest()
	request.RequestedLimits = maximum
	serverHealth := &commonv1.Health{
		State:  commonv1.HealthState_HEALTH_STATE_READY,
		Limits: maximum,
	}
	response := enginev1.NegotiateInitialize(
		request,
		commonv1.SupportedProtocolRange(),
		build("spice-agentd"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots", "tools"),
		maximum,
		serverHealth,
		definitionSet(),
		"client-1",
		1,
	)
	if err := enginev1.ValidateInitializeResponse(response); err != nil {
		t.Fatalf("initialize response with maximum uint32 limits: %v", err)
	}
	if err := enginev1.ValidateDefinitionSet(definitionSet(), maximum); err != nil {
		t.Fatalf("definition set with maximum uint32 item limit: %v", err)
	}
	if err := enginev1.ValidateStartRunRequest(validStartRunRequest(), maximum); err != nil {
		t.Fatalf("start request with maximum uint32 item limit: %v", err)
	}

	pending := pendingInteraction("run-1", "interaction-1", "Continue?")
	snapshot := &enginev1.InteractionSnapshot{
		Revision: 1,
		Pending:  []*enginev1.PendingInteraction{pending},
	}
	if err := enginev1.ValidateInteractionSnapshot(snapshot, maximum); err != nil {
		t.Fatalf("interaction snapshot with maximum uint32 item limit: %v", err)
	}
	control := &enginev1.InteractionStreamControl{
		Status:           commonv1.OKStatus(),
		LatestRevision:   1,
		PageLastRevision: 1,
		Tailing:          true,
	}
	validator, err := enginev1.NewInteractionTailValidator(snapshot, control, maximum)
	if err != nil {
		t.Fatalf("interaction tail with maximum uint32 item limit: %v", err)
	}
	next := pendingInteraction("run-1", "interaction-2", "Again?")
	if err = validator.Accept(interactionDeltaFrame(interactionDelta(
		2,
		enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_OPENED,
		next,
	))); err != nil {
		t.Fatalf("interaction delta with maximum uint32 item limit: %v", err)
	}

	events := []*enginev1.RunEvent{
		runEvent(1, enginev1.EventKind_EVENT_KIND_RUN_STARTED, false),
	}
	if err = enginev1.ValidateEventBatch("run-1", 0, events, maximum); err != nil {
		t.Fatalf("event batch with maximum uint32 replay limit: %v", err)
	}
}

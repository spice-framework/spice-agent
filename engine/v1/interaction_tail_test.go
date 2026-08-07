package enginev1_test

import (
	"math"
	"strings"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestInteractionTailValidatorRequiresContiguousMatchingMembership(t *testing.T) {
	t.Parallel()
	first := pendingInteraction("run-1", "first", "First?")
	snapshot := &enginev1.InteractionSnapshot{Revision: 7, Pending: []*enginev1.PendingInteraction{first}}
	control := &enginev1.InteractionStreamControl{
		Status: commonv1.OKStatus(), LatestRevision: 7, PageLastRevision: 7, Tailing: true,
	}
	validator, err := enginev1.NewInteractionTailValidator(snapshot, control, protocolLimits())
	if err != nil {
		t.Fatal(err)
	}
	second := pendingInteraction("run-1", "second", "Second?")
	opened := interactionDelta(8, enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_OPENED, second)
	if err = validator.Accept(interactionDeltaFrame(opened)); err != nil || validator.Revision() != 8 {
		t.Fatalf("valid open = revision %d, err %v", validator.Revision(), err)
	}

	invalid := map[string]*enginev1.InteractionDelta{
		"gap": interactionDelta(
			10, enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_CLOSED, second,
		),
		"duplicate open": interactionDelta(
			9, enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_OPENED, second,
		),
		"missing close": interactionDelta(
			9, enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_CLOSED,
			pendingInteraction("run-9", "missing", "Missing?"),
		),
		"changed close": interactionDelta(
			9, enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_CLOSED,
			pendingInteraction("run-1", "second", "Changed?"),
		),
	}
	for name, delta := range invalid {
		if err = validator.Accept(interactionDeltaFrame(delta)); err == nil || validator.Revision() != 8 {
			t.Errorf("%s = revision %d, err %v", name, validator.Revision(), err)
		}
	}
	closed := interactionDelta(9, enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_CLOSED, second)
	if err = validator.Accept(interactionDeltaFrame(closed)); err != nil || validator.Revision() != 9 {
		t.Fatalf("valid close = revision %d, err %v", validator.Revision(), err)
	}
}

func TestInteractionTailValidatorRejectsInvalidInitialStateAndRevisionOverflow(t *testing.T) {
	t.Parallel()
	control := &enginev1.InteractionStreamControl{
		Status: commonv1.OKStatus(), LatestRevision: math.MaxUint64,
		PageLastRevision: math.MaxUint64, Tailing: true,
	}
	validator, err := enginev1.NewInteractionTailValidator(
		&enginev1.InteractionSnapshot{Revision: math.MaxUint64}, control, protocolLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = validator.Accept(interactionDeltaFrame(interactionDelta(
		1, enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_OPENED,
		pendingInteraction("run-1", "next", "Next?"),
	))); err == nil {
		t.Fatal("revision overflow succeeded")
	}
	if validator.Revision() != math.MaxUint64 {
		t.Fatalf("overflow changed revision to %d", validator.Revision())
	}
	if _, err = enginev1.NewInteractionTailValidator(
		&enginev1.InteractionSnapshot{}, &enginev1.InteractionStreamControl{
			Status: commonv1.OKStatus(), Tailing: false,
		}, protocolLimits(),
	); err == nil {
		t.Fatal("finite control constructed a live-tail validator")
	}
	var missing *enginev1.InteractionTailValidator
	if err = missing.Accept(interactionDeltaFrame(interactionDelta(
		1, enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_OPENED,
		pendingInteraction("run-1", "next", "Next?"),
	))); err == nil {
		t.Fatal("nil tail validator accepted a delta")
	}
}

func TestInteractionTailValidatorKeepsReconnectSnapshotWithinCompleteFrameBound(t *testing.T) {
	t.Parallel()
	first := pendingInteraction("run-1", "first", strings.Repeat("a", 96))
	second := pendingInteraction("run-1", "second", strings.Repeat("b", 96))
	firstDelta := interactionDelta(1, enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_OPENED, first)
	secondDelta := interactionDelta(2, enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_OPENED, second)
	emptySnapshot := &enginev1.InteractionSnapshot{}
	control := &enginev1.InteractionStreamControl{Status: commonv1.OKStatus(), Tailing: true}
	firstSnapshot := &enginev1.InteractionSnapshot{Revision: 1, Pending: []*enginev1.PendingInteraction{first}}
	secondSnapshot := &enginev1.InteractionSnapshot{Revision: 2, Pending: []*enginev1.PendingInteraction{first, second}}
	bound := maximumProtoSize(
		interactionSnapshotFrame(emptySnapshot), interactionControlFrame(control),
		interactionDeltaFrame(firstDelta), interactionDeltaFrame(secondDelta),
		interactionSnapshotFrame(firstSnapshot),
	)
	if secondSize := proto.Size(interactionSnapshotFrame(secondSnapshot)); secondSize <= bound {
		t.Fatalf("test setup second snapshot size %d <= bound %d", secondSize, bound)
	}
	streamLimits := limits(uint64(bound), 4, 4, uint64(bound), 1, 1)
	validator, err := enginev1.NewInteractionTailValidator(emptySnapshot, control, streamLimits)
	if err != nil {
		t.Fatal(err)
	}

	oversized := proto.CloneOf(interactionDeltaFrame(firstDelta))
	unknown := protowire.AppendTag(nil, 100, protowire.BytesType)
	unknown = protowire.AppendBytes(unknown, []byte(strings.Repeat("x", bound)))
	oversized.ProtoReflect().SetUnknown(unknown)
	if err = validator.Accept(oversized); err == nil || validator.Revision() != 0 {
		t.Fatalf("oversized wrapper = revision %d, err %v", validator.Revision(), err)
	}
	if err = validator.Accept(interactionDeltaFrame(firstDelta)); err != nil {
		t.Fatal(err)
	}
	if err = validator.Accept(interactionDeltaFrame(secondDelta)); err == nil || validator.Revision() != 1 {
		t.Fatalf("unrepresentable aggregate = revision %d, err %v", validator.Revision(), err)
	}
	closed := interactionDelta(2, enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_CLOSED, first)
	if err = validator.Accept(interactionDeltaFrame(closed)); err != nil || validator.Revision() != 2 {
		t.Fatalf("state after rejected aggregate = revision %d, err %v", validator.Revision(), err)
	}
}

func pendingInteraction(runID, interactionID, prompt string) *enginev1.PendingInteraction {
	return &enginev1.PendingInteraction{
		RunId: runID, InteractionId: interactionID, Kind: "text", Prompt: prompt, SchemaJson: []byte(`{}`),
	}
}

func interactionDelta(
	revision uint64,
	kind enginev1.InteractionDeltaKind,
	interaction *enginev1.PendingInteraction,
) *enginev1.InteractionDelta {
	return &enginev1.InteractionDelta{Revision: revision, Kind: kind, Interaction: interaction}
}

func interactionDeltaFrame(delta *enginev1.InteractionDelta) *enginev1.StreamInteractionsResponse {
	return &enginev1.StreamInteractionsResponse{
		Payload: &enginev1.StreamInteractionsResponse_Delta{Delta: delta},
	}
}

func interactionSnapshotFrame(snapshot *enginev1.InteractionSnapshot) *enginev1.StreamInteractionsResponse {
	return &enginev1.StreamInteractionsResponse{
		Payload: &enginev1.StreamInteractionsResponse_Snapshot{Snapshot: snapshot},
	}
}

func interactionControlFrame(control *enginev1.InteractionStreamControl) *enginev1.StreamInteractionsResponse {
	return &enginev1.StreamInteractionsResponse{
		Payload: &enginev1.StreamInteractionsResponse_Control{Control: control},
	}
}

func maximumProtoSize(values ...proto.Message) int {
	maximum := 0
	for _, value := range values {
		maximum = max(maximum, proto.Size(value))
	}
	return maximum
}

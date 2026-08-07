package enginev1_test

import (
	"context"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestDefinitionSetIsServerOwnedSortedAndBounded(t *testing.T) {
	t.Parallel()
	definitions := definitionSet()
	definitions.Definitions = append(definitions.Definitions, &enginev1.Definition{
		Id: "review", Revision: "v2", Model: "reasoning", MaxTurns: 4,
	})
	if err := enginev1.ValidateDefinitionSet(definitions, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	definitions.Definitions[0].MaxTurns = 1000
	if err := enginev1.ValidateDefinitionSet(definitions, protocolLimits()); err != nil {
		t.Fatalf("1000-turn definition: %v", err)
	}
	definitions.Definitions[0].MaxTurns = 1001
	if err := enginev1.ValidateDefinitionSet(definitions, protocolLimits()); err == nil {
		t.Fatal("1001-turn definition succeeded")
	}
	definitions.Definitions[0].MaxTurns = 8
	resolved, err := enginev1.ResolveDefinition(
		&enginev1.AgentDefinitionRef{Id: "review", Revision: "v2"}, definitions, protocolLimits(),
	)
	if err != nil || resolved.GetModel() != "reasoning" {
		t.Fatalf("resolved definition = %#v, %v", resolved, err)
	}
	resolved.Model = "mutated"
	if definitions.Definitions[1].GetModel() != "reasoning" {
		t.Fatal("resolved definition retained server-owned storage")
	}
	if _, err = enginev1.ResolveDefinition(
		&enginev1.AgentDefinitionRef{Id: "missing", Revision: "v1"}, definitions, protocolLimits(),
	); err == nil {
		t.Fatal("unknown definition reference succeeded")
	}
	definitions.Definitions[1].Id = "aaa"
	if err := enginev1.ValidateDefinitionSet(definitions, protocolLimits()); err == nil {
		t.Fatal("unsorted definition set succeeded")
	}
	definitions = definitionSet()
	definitions.Definitions[0].MaxTurns = 0
	if err := enginev1.ValidateDefinitionSet(definitions, protocolLimits()); err == nil {
		t.Fatal("definition without a turn bound succeeded")
	}
}

func TestReplayControlsSupportEmptyAndImportedTails(t *testing.T) {
	t.Parallel()
	for _, bounds := range [][2]uint64{{1, 0}, {8, 7}} {
		if status := enginev1.CheckReplayCursor(bounds[1], bounds[0], bounds[1]); status != nil {
			t.Fatalf("empty bounds %v rejected: %#v", bounds, status)
		}
		pageLast := bounds[1]
		control := &enginev1.StreamControl{
			Status: commonv1.OKStatus(), EarliestSequence: bounds[0], LatestSequence: bounds[1],
			LastDeliveredSequence: bounds[1], Tailing: true, PageLastSequence: &pageLast,
		}
		if err := enginev1.ValidateStreamControl(control); err != nil {
			t.Fatalf("empty control %v: %v", bounds, err)
		}
		future := enginev1.CheckReplayCursor(bounds[1]+1, bounds[0], bounds[1])
		if future.GetReplayBounds().GetRecoverySequence() != bounds[1] {
			t.Fatalf("empty future recovery %v = %#v", bounds, future)
		}
	}
	pageLast := uint64(12)
	more := &enginev1.StreamControl{
		Status: commonv1.OKStatus(), EarliestSequence: 10, LatestSequence: 20,
		LastDeliveredSequence: 12, PageLastSequence: &pageLast, HasMore: true,
	}
	if err := enginev1.ValidateStreamControl(more); err != nil {
		t.Fatal(err)
	}
	more.Tailing = true
	if err := enginev1.ValidateStreamControl(more); err == nil {
		t.Fatal("control both paged and tailed")
	}
	more.Tailing = false
	more.HasMore = false
	if err := enginev1.ValidateStreamControl(more); err == nil {
		t.Fatal("non-final control silently omitted captured events")
	}
	legacy := &enginev1.StreamControl{
		Status: commonv1.OKStatus(), EarliestSequence: 10, LatestSequence: 20,
		LastDeliveredSequence: 9,
	}
	if err := enginev1.ValidateStreamControl(legacy); err != nil {
		t.Fatalf("legacy retained cursor: %v", err)
	}
	legacy.LastDeliveredSequence = 0
	if err := enginev1.ValidateStreamControl(legacy); err == nil {
		t.Fatal("legacy delivery cursor before retained domain succeeded")
	}
	pageLast = 12
	more.PageLastSequence = &pageLast
	more.HasMore = true
	more.LastDeliveredSequence = 0
	if err := enginev1.ValidateStreamControl(more); err == nil {
		t.Fatal("page delivery cursor before retained domain succeeded")
	}
}

func TestInteractionStreamAlwaysStartsFromAtomicPendingSnapshot(t *testing.T) {
	t.Parallel()
	request := &enginev1.StreamInteractionsRequest{
		ClientId: "client", OwnershipEpoch: 1, ReplayLimit: 32, Tail: true,
	}
	if err := enginev1.ValidateStreamInteractionsRequest(request, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	pending := []*enginev1.PendingInteraction{
		{RunId: "run-1", InteractionId: "approval-1", Kind: "confirm", Prompt: "Continue?", SchemaJson: []byte(`{"type":"boolean"}`)},
		{RunId: "run-2", InteractionId: "input-1", Kind: "text", Prompt: "Value?", SchemaJson: []byte(`{"type":"string"}`)},
	}
	snapshot := &enginev1.InteractionSnapshot{Revision: 9, Pending: pending}
	if err := enginev1.ValidateInteractionSnapshot(snapshot, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	delta := &enginev1.InteractionDelta{
		Revision: 10, Kind: enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_CLOSED,
		Interaction: pending[0],
	}
	if err := enginev1.ValidateInteractionDelta(delta); err != nil {
		t.Fatal(err)
	}
	control := &enginev1.InteractionStreamControl{
		Status: commonv1.OKStatus(), LatestRevision: 10, PageLastRevision: 10, Tailing: true,
	}
	if err := enginev1.ValidateInteractionStreamControl(control); err != nil {
		t.Fatal(err)
	}
	omitted, valid := proto.Clone(control).(*enginev1.InteractionStreamControl)
	if !valid {
		t.Fatal("interaction control clone had unexpected type")
	}
	omitted.LatestRevision = 11
	if err := enginev1.ValidateInteractionStreamControl(omitted); err == nil {
		t.Fatal("interaction control silently omitted a captured delta")
	}
	page := []*enginev1.StreamInteractionsResponse{
		{Payload: &enginev1.StreamInteractionsResponse_Snapshot{Snapshot: snapshot}},
		{Payload: &enginev1.StreamInteractionsResponse_Delta{Delta: delta}},
		{Payload: &enginev1.StreamInteractionsResponse_Control{Control: control}},
	}
	if err := enginev1.ValidateInteractionStreamPage(page, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	duplicateOpen := &enginev1.InteractionDelta{
		Revision: 10, Kind: enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_OPENED,
		Interaction: pending[0],
	}
	page[1] = &enginev1.StreamInteractionsResponse{Payload: &enginev1.StreamInteractionsResponse_Delta{Delta: duplicateOpen}}
	if err := enginev1.ValidateInteractionStreamPage(page, protocolLimits()); err == nil {
		t.Fatal("duplicate interaction open succeeded")
	}
	missingClose := &enginev1.InteractionDelta{
		Revision: 10, Kind: enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_CLOSED,
		Interaction: &enginev1.PendingInteraction{
			RunId: "run-9", InteractionId: "missing", Kind: "confirm", Prompt: "Missing?", SchemaJson: []byte(`{}`),
		},
	}
	page[1] = &enginev1.StreamInteractionsResponse{Payload: &enginev1.StreamInteractionsResponse_Delta{Delta: missingClose}}
	if err := enginev1.ValidateInteractionStreamPage(page, protocolLimits()); err == nil {
		t.Fatal("close of absent interaction succeeded")
	}
	page[1] = &enginev1.StreamInteractionsResponse{Payload: &enginev1.StreamInteractionsResponse_Delta{Delta: delta}}
	page[0] = page[1]
	if err := enginev1.ValidateInteractionStreamPage(page, protocolLimits()); err == nil {
		t.Fatal("delta-only interaction reconnect page succeeded")
	}
	page[0] = &enginev1.StreamInteractionsResponse{Payload: &enginev1.StreamInteractionsResponse_Snapshot{Snapshot: snapshot}}
	snapshot.Pending[0], snapshot.Pending[1] = snapshot.Pending[1], snapshot.Pending[0]
	if err := enginev1.ValidateInteractionSnapshot(snapshot, protocolLimits()); err == nil {
		t.Fatal("unsorted pending snapshot succeeded")
	}
	delta.Revision = 0
	if err := enginev1.ValidateInteractionDelta(delta); err == nil {
		t.Fatal("unrevisioned interaction delta succeeded")
	}
}

func TestSuspendResumeAndSnapshotImportPreserveRunIdentity(t *testing.T) {
	t.Parallel()
	if err := enginev1.ValidateSuspendRunRequest(&enginev1.SuspendRunRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "suspend-1", RunId: "run-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := enginev1.ValidateResumeRunRequest(&enginev1.ResumeRunRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "resume-1", RunId: "run-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := enginev1.ValidateResumeRunRequest(&enginev1.ResumeRunRequest{}); err == nil {
		t.Fatal("unowned resume succeeded")
	}
	payload := []byte(`{"version":"spice.agent.snapshot/v1alpha2","run_id":"run-1"}`)
	snapshot, err := enginev1.NewSnapshotEnvelope(
		context.Background(), snapshotAuthority(t),
		"run-1", 7, enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED, payload,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = enginev1.ValidateImportSnapshotRequest(context.Background(), &enginev1.ImportSnapshotRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "import-1", Snapshot: snapshot,
	}, snapshotAuthority(t)); err != nil {
		t.Fatal(err)
	}
	if _, err = enginev1.NewSnapshotEnvelope(
		context.Background(), snapshotAuthority(t),
		"run-2", 7, enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED, payload,
	); err == nil {
		t.Fatal("snapshot envelope changed embedded run identity")
	}
}

func TestProvisionalSchemaReservationsPreventSemanticReuse(t *testing.T) {
	t.Parallel()
	assertReservations(t, (&enginev1.AgentDefinitionRef{}).ProtoReflect().Descriptor(), []protoreflect.FieldNumber{3, 4, 5, 6}, []protoreflect.Name{"model", "max_turns", "expected_static_plan_fingerprint"})
	assertReservations(t, (&enginev1.InitializeRequest{}).ProtoReflect().Descriptor(), []protoreflect.FieldNumber{6}, []protoreflect.Name{"authentication_token"})
	assertReservations(t, (&enginev1.RunEvent{}).ProtoReflect().Descriptor(), []protoreflect.FieldNumber{7}, []protoreflect.Name{"operation_id"})
	assertReservations(t, (&enginev1.RespondInteractionRequest{}).ProtoReflect().Descriptor(), []protoreflect.FieldNumber{6}, []protoreflect.Name{"response_id"})
	assertReservations(t, (&enginev1.RespondInteractionResponse{}).ProtoReflect().Descriptor(), []protoreflect.FieldNumber{3, 4}, []protoreflect.Name{"duplicate_response", "committed_sequence"})
	assertReservations(t, (&enginev1.ImportSnapshotRequest{}).ProtoReflect().Descriptor(), []protoreflect.FieldNumber{4, 5, 6}, []protoreflect.Name{"new_run_id", "expected_static_plan_fingerprint", "expected_plan_id"})
	if field := (&enginev1.Definition{}).ProtoReflect().Descriptor().Fields().ByName("static_plan_fingerprint"); field != nil {
		t.Fatal("server definition exposed a stale plan fingerprint")
	}
	if field := (&enginev1.RespondInteractionResponse{}).ProtoReflect().Descriptor().Fields().ByName("duplicate_operation"); field == nil || field.Number() != 5 {
		t.Fatal("interaction response duplicate operation field mismatch")
	}
	if !(&enginev1.StreamControl{}).ProtoReflect().Descriptor().Fields().ByName("page_last_sequence").HasOptionalKeyword() {
		t.Fatal("stream page cursor is not optional")
	}
}

func assertReservations(t *testing.T, descriptor protoreflect.MessageDescriptor, numbers []protoreflect.FieldNumber, names []protoreflect.Name) {
	t.Helper()
	for _, number := range numbers {
		if !descriptor.ReservedRanges().Has(number) {
			t.Errorf("%s field %d is not reserved", descriptor.FullName(), number)
		}
	}
	for _, name := range names {
		if !descriptor.ReservedNames().Has(name) {
			t.Errorf("%s name %q is not reserved", descriptor.FullName(), name)
		}
	}
}

func TestStreamInteractionUnknownFieldsRemainAdditive(t *testing.T) {
	t.Parallel()
	response := &enginev1.StreamInteractionsResponse{
		Payload: &enginev1.StreamInteractionsResponse_Snapshot{Snapshot: &enginev1.InteractionSnapshot{Revision: 1}},
	}
	encoded, err := proto.Marshal(response)
	if err != nil || len(encoded) == 0 {
		t.Fatalf("interaction stream response = %x, %v", encoded, err)
	}
}

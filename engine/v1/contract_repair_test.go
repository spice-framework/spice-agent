package enginev1_test

import (
	"context"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/encoding/protowire"
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
	if err := enginev1.ValidateInteractionStreamProtocol(&commonv1.ProtocolVersion{Major: 1, Minor: 2}); err != nil {
		t.Fatal(err)
	}
	if err := enginev1.ValidateInteractionStreamProtocol(&commonv1.ProtocolVersion{Major: 1, Minor: 1}); err == nil {
		t.Fatal("minor-one interaction stream succeeded")
	}
	request := &enginev1.StreamInteractionsRequest{
		ClientId: "client", OwnershipEpoch: 1, Tail: true,
	}
	minorTwo := &commonv1.ProtocolVersion{Major: 1, Minor: 2}
	if err := enginev1.ValidateStreamInteractionsRequest(request, minorTwo, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	if err := enginev1.ValidateStreamInteractionsRequest(
		request, &commonv1.ProtocolVersion{Major: 1, Minor: 1}, protocolLimits(),
	); err == nil {
		t.Fatal("minor-one interaction request succeeded")
	}
	legacy, err := proto.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	legacy = protowire.AppendTag(legacy, 3, protowire.VarintType)
	legacy = protowire.AppendVarint(legacy, 32)
	legacyRequest := new(enginev1.StreamInteractionsRequest)
	if err = proto.Unmarshal(legacy, legacyRequest); err != nil {
		t.Fatal(err)
	}
	if err = enginev1.ValidateStreamInteractionsRequest(legacyRequest, minorTwo, protocolLimits()); err == nil {
		t.Fatal("legacy interaction replay field succeeded under minor two")
	}
	pending := []*enginev1.PendingInteraction{
		{RunId: "run-1", InteractionId: "approval-1", Kind: "confirm", Prompt: "Continue?", SchemaJson: []byte(`{"type":"boolean"}`)},
		{RunId: "run-2", InteractionId: "input-1", Kind: "text", Prompt: "Value?", SchemaJson: []byte(`{"type":"string"}`)},
	}
	snapshot := &enginev1.InteractionSnapshot{Revision: 9, Pending: pending}
	if err := enginev1.ValidateInteractionSnapshot(snapshot, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	control := &enginev1.InteractionStreamControl{
		Status: commonv1.OKStatus(), LatestRevision: 9, PageLastRevision: 9, Tailing: true,
	}
	if err := enginev1.ValidateInteractionStreamControl(control); err != nil {
		t.Fatal(err)
	}
	omitted, valid := proto.Clone(control).(*enginev1.InteractionStreamControl)
	if !valid {
		t.Fatal("interaction control clone had unexpected type")
	}
	omitted.LatestRevision = 10
	if err := enginev1.ValidateInteractionStreamControl(omitted); err == nil {
		t.Fatal("interaction control silently omitted a captured delta")
	}
	page := []*enginev1.StreamInteractionsResponse{
		{Payload: &enginev1.StreamInteractionsResponse_Snapshot{Snapshot: snapshot}},
		{Payload: &enginev1.StreamInteractionsResponse_Control{Control: control}},
	}
	if err := enginev1.ValidateInteractionStreamPage(page, true, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	page[1] = &enginev1.StreamInteractionsResponse{Payload: &enginev1.StreamInteractionsResponse_Snapshot{Snapshot: snapshot}}
	if err := enginev1.ValidateInteractionStreamPage(page, true, protocolLimits()); err == nil {
		t.Fatal("duplicate interaction snapshot succeeded")
	}
	page[1] = &enginev1.StreamInteractionsResponse{Payload: &enginev1.StreamInteractionsResponse_Control{Control: control}}
	control.LatestRevision = 10
	if err := enginev1.ValidateInteractionStreamPage(page, true, protocolLimits()); err == nil {
		t.Fatal("control newer than complete snapshot succeeded")
	}
	control.LatestRevision = 9
	page[0] = page[1]
	if err := enginev1.ValidateInteractionStreamPage(page, true, protocolLimits()); err == nil {
		t.Fatal("delta-only interaction reconnect page succeeded")
	}
	page[0] = &enginev1.StreamInteractionsResponse{Payload: &enginev1.StreamInteractionsResponse_Snapshot{Snapshot: snapshot}}
	if err := enginev1.ValidateInteractionStreamPage(page, false, protocolLimits()); err == nil {
		t.Fatal("tailing control accepted for finite request")
	}
	snapshot.Pending[0], snapshot.Pending[1] = snapshot.Pending[1], snapshot.Pending[0]
	if err := enginev1.ValidateInteractionSnapshot(snapshot, protocolLimits()); err == nil {
		t.Fatal("unsorted pending snapshot succeeded")
	}
	delta := &enginev1.InteractionDelta{
		Kind:        enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_CLOSED,
		Interaction: pending[0],
	}
	if err := enginev1.ValidateInteractionDelta(delta); err == nil {
		t.Fatal("unrevisioned interaction delta succeeded")
	}
}

func TestMinorTwoNegotiationRequiresServerSizedSnapshotBounds(t *testing.T) {
	t.Parallel()
	for name, reduce := range map[string]func(*commonv1.Limits){
		"message bytes": func(value *commonv1.Limits) { value.MaxMessageBytes-- },
		"collection":    func(value *commonv1.Limits) { value.MaxCollectionItems-- },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			request := validInitializeRequest()
			reduce(request.RequestedLimits)
			negotiation, failure := enginev1.PreflightInitialize(
				request, commonv1.SupportedProtocolRange(), build("spice-agentd"),
				capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots", "tools"),
				protocolLimits(), health(), definitionSet(),
			)
			if negotiation != nil || failure.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
				t.Fatalf("minor-two reduced limits = negotiation %#v, failure %#v", negotiation, failure)
			}

			request.Protocol.Maximum.Minor = 1
			negotiation, failure = enginev1.PreflightInitialize(
				request, commonv1.SupportedProtocolRange(), build("spice-agentd"),
				capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots", "tools"),
				protocolLimits(), health(), definitionSet(),
			)
			if failure != nil || negotiation == nil {
				t.Fatalf("minor-one reduced limits = negotiation %#v, failure %#v", negotiation, failure)
			}
		})
	}

	request := validInitializeRequest()
	response := enginev1.NegotiateInitialize(
		request, commonv1.SupportedProtocolRange(), build("spice-agentd"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots", "tools"),
		protocolLimits(), health(), definitionSet(), "client", 1,
	)
	response.Limits.MaxMessageBytes--
	if err := enginev1.ValidateInitializeResponse(response); err == nil {
		t.Fatal("client accepted minor-two response with a reduced snapshot bound")
	}
}

func TestSuspendResumeAndSnapshotImportPreserveRunIdentity(t *testing.T) {
	t.Parallel()
	if err := enginev1.ValidateSuspendRunRequest(&enginev1.SuspendRunRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "suspend-1", RunId: "run-1",
	}, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	if err := enginev1.ValidateResumeRunRequest(&enginev1.ResumeRunRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "resume-1", RunId: "run-1",
	}, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	if err := enginev1.ValidateResumeRunRequest(&enginev1.ResumeRunRequest{}, protocolLimits()); err == nil {
		t.Fatal("unowned resume succeeded")
	}
	payload := []byte(`{"version":"spice.agent.snapshot/v1alpha3","run_id":"run-1"}`)
	snapshot, err := enginev1.NewSnapshotEnvelope(
		context.Background(), snapshotAuthority(t),
		"run-1", 7, enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED, payload,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = enginev1.ValidateImportSnapshotRequest(context.Background(), &enginev1.ImportSnapshotRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "import-1", Snapshot: snapshot,
	}, snapshotAuthority(t), protocolLimits()); err != nil {
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
	assertReservations(t, (&enginev1.StreamInteractionsRequest{}).ProtoReflect().Descriptor(), []protoreflect.FieldNumber{3}, []protoreflect.Name{"replay_limit"})
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

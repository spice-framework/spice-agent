package enginev1_test

import (
	"bytes"
	"context"
	"slices"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestInitializeNegotiatesVersionCapabilitiesLimitsAndHealth(t *testing.T) {
	t.Parallel()
	request := validInitializeRequest()
	response := enginev1.NegotiateInitialize(
		request,
		commonv1.SupportedProtocolRange(),
		build("spice-agentd"),
		capabilities("events", "plugins", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		limits(4<<20, 128, 256, 8<<20, 8, 32),
		health(),
		definitionSet(),
		"client-1",
		1,
	)
	if err := enginev1.ValidateInitializeResponse(response); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(response.GetCapabilities().GetNames(), []string{"events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"}) ||
		response.GetLimits().GetMaxReplayEvents() != 128 ||
		response.GetProtocol().GetMajor() != 1 || response.GetOwnershipEpoch() != 1 ||
		response.GetDefinitions().GetRevision() != "catalog-v1" {
		t.Fatalf("initialize response = %#v", response)
	}
	server := response.GetServer()
	if server == nil {
		t.Fatal("initialize response omitted server identity")
		return
	}
	server.Component = "mutated"
	if health().GetState() != commonv1.HealthState_HEALTH_STATE_READY {
		t.Fatal("test health fixture unexpectedly mutable")
	}
	definitions := response.GetDefinitions()
	if definitions == nil {
		t.Fatal("initialize response omitted definitions")
		return
	}
	definitions.Revision = "mutated"
	if definitionSet().GetRevision() != "catalog-v1" {
		t.Fatal("initialize response retained server-owned definitions")
	}
}

func TestInitializeFailsClosedForOldNewAndMissingCapability(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*enginev1.InitializeRequest)
		code   commonv1.ErrorCode
	}{
		{
			"old major",
			func(request *enginev1.InitializeRequest) {
				request.Protocol = versionRange(2)
			},
			commonv1.ErrorCode_ERROR_CODE_INCOMPATIBLE_VERSION,
		},
		{
			"new major",
			func(request *enginev1.InitializeRequest) {
				request.Protocol = versionRange(9)
			},
			commonv1.ErrorCode_ERROR_CODE_INCOMPATIBLE_VERSION,
		},
		{
			"missing capability",
			func(request *enginev1.InitializeRequest) {
				request.RequiredCapabilities = capabilities("remote-admin")
				request.SupportedCapabilities = capabilities("events", "remote-admin")
			},
			commonv1.ErrorCode_ERROR_CODE_MISSING_CAPABILITY,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := validInitializeRequest()
			test.mutate(request)
			response := enginev1.NegotiateInitialize(
				request,
				commonv1.SupportedProtocolRange(),
				build("spice-agentd"),
				capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
				limits(4<<20, 128, 256, 8<<20, 8, 32),
				health(),
				definitionSet(),
				"client-1",
				1,
			)
			if response.GetStatus().GetCode() != test.code {
				t.Fatalf("status = %#v", response.GetStatus())
			}
		})
	}
}

func TestSnapshotAuthorityRequiresProtocolMinorOneAndCapability(t *testing.T) {
	t.Parallel()
	request := validInitializeRequest()
	request.Protocol = &commonv1.ProtocolRange{
		Minimum: &commonv1.ProtocolVersion{Major: 1},
		Maximum: &commonv1.ProtocolVersion{Major: 1},
	}
	response := enginev1.NegotiateInitialize(
		request, commonv1.SupportedProtocolRange(), build("spice-agentd"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(), "client-1", 1,
	)
	if response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_MISSING_CAPABILITY {
		t.Fatalf("minor-zero authority status = %#v", response.GetStatus())
	}

	request.RequiredCapabilities = capabilities("events")
	response = enginev1.NegotiateInitialize(
		request, commonv1.SupportedProtocolRange(), build("spice-agentd"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(), "client-1", 1,
	)
	if response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_OK ||
		response.GetProtocol().GetMinor() != 0 ||
		!slices.Equal(response.GetCapabilities().GetNames(), []string{"events"}) {
		t.Fatalf("minor-zero negotiated authority = %#v", response)
	}

	response.Protocol = &commonv1.ProtocolVersion{Major: 1}
	response.Capabilities = capabilities("events", "snapshots")
	if err := enginev1.ValidateInitializeResponse(response); err == nil {
		t.Fatal("client accepted snapshot transfer on protocol minor zero")
	}
}

func TestInitializeReconnectClaimRequiresOwnershipEpochCompareAndSwap(t *testing.T) {
	t.Parallel()
	request := validInitializeRequest()
	request.ReconnectClaim = &enginev1.ReconnectClaim{ClientId: "client-1", ExpectedOwnershipEpoch: 7}
	response := enginev1.NegotiateInitialize(
		request, commonv1.SupportedProtocolRange(), build("spice-agentd"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"), protocolLimits(), health(), definitionSet(), "client-1", 8,
	)
	if err := enginev1.ValidateInitializeResponseForRequest(request, response); err != nil || response.GetOwnershipEpoch() != 8 {
		t.Fatalf("reconnect response = %#v, %v", response, err)
	}
	wrongEpoch, valid := proto.Clone(response).(*enginev1.InitializeResponse)
	if !valid {
		t.Fatal("initialize clone had unexpected type")
	}
	wrongEpoch.OwnershipEpoch = 9
	if err := enginev1.ValidateInitializeResponseForRequest(request, wrongEpoch); err == nil {
		t.Fatal("client accepted reconnect outside expected epoch CAS")
	}
	for _, test := range []struct {
		client string
		epoch  uint64
	}{
		{"other", 8}, {"client-1", 7}, {"client-1", 9},
	} {
		response = enginev1.NegotiateInitialize(
			request, commonv1.SupportedProtocolRange(), build("spice-agentd"),
			capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"), protocolLimits(), health(), definitionSet(), test.client, test.epoch,
		)
		if response.GetStatus().GetCode() == commonv1.ErrorCode_ERROR_CODE_OK {
			t.Fatalf("invalid reconnect %q/%d succeeded", test.client, test.epoch)
		}
	}
}

func TestRunCreationAndCancellationAreBoundedIdempotentContracts(t *testing.T) {
	t.Parallel()
	request := &enginev1.StartRunRequest{
		ClientId:          "client-1",
		OwnershipEpoch:    1,
		ClientOperationId: "start-1",
		Definition: &enginev1.AgentDefinitionRef{
			Id: "coding", Revision: "v1",
		},
		Input: &enginev1.Message{
			Id: "message-1", Role: enginev1.MessageRole_MESSAGE_ROLE_USER,
			Parts: []*enginev1.ContentPart{{Value: &enginev1.ContentPart_Text{Text: "inspect the repository"}}},
		},
	}
	if err := enginev1.ValidateStartRunRequest(request, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	request.Input.Role = enginev1.MessageRole_MESSAGE_ROLE_ASSISTANT
	if err := enginev1.ValidateStartRunRequest(request, protocolLimits()); err == nil {
		t.Fatal("assistant initial input succeeded")
	}
	cancel := &enginev1.CancelRunRequest{
		ClientId: "client-1", OwnershipEpoch: 1,
		ClientOperationId: "cancel-1", RunId: "run-1", Reason: "user requested",
	}
	if err := enginev1.ValidateCancelRunRequest(cancel, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	cancel.ClientOperationId = ""
	if err := enginev1.ValidateCancelRunRequest(cancel, protocolLimits()); err == nil {
		t.Fatal("cancellation without idempotency identity succeeded")
	}
}

func TestReplayBoundsOrderingOverloadAndUnknownLifecycle(t *testing.T) {
	t.Parallel()
	if status := enginev1.CheckReplayCursor(9, 10, 20); status != nil {
		t.Fatalf("valid replay cursor = %#v", status)
	}
	gap := enginev1.CheckReplayCursor(8, 10, 20)
	if gap.GetCode() != commonv1.ErrorCode_ERROR_CODE_OUT_OF_RANGE ||
		gap.GetReplayBounds().GetRecoverySequence() != 9 {
		t.Fatalf("replay gap = %#v", gap)
	}
	if status := enginev1.CheckReplayCursor(20, 10, 20); status != nil {
		t.Fatalf("tail cursor = %#v", status)
	}
	if status := enginev1.CheckReplayCursor(21, 10, 20); status.GetReplayBounds() == nil ||
		status.GetReplayBounds().GetRecoverySequence() != 20 {
		t.Fatalf("future cursor = %#v", status)
	}
	events := []*enginev1.RunEvent{
		runEvent(11, enginev1.EventKind_EVENT_KIND_MODEL_DELTA, false),
		runEvent(12, enginev1.EventKind_EVENT_KIND_MODEL_COMPLETED, true),
	}
	if err := enginev1.ValidateEventBatch("run-1", 10, events, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	events[1].Sequence = 13
	if err := enginev1.ValidateEventBatch("run-1", 10, events, protocolLimits()); err == nil {
		t.Fatal("event sequence gap succeeded")
	}
	unknown := runEvent(12, enginev1.EventKind(9000), false)
	if err := enginev1.ValidateRunEvent(unknown); err == nil {
		t.Fatal("unknown lifecycle event kind succeeded")
	}
	overload := enginev1.OverloadStatus("active-runs", 8, 9)
	if err := commonv1.ValidateStatus(overload); err != nil ||
		overload.GetOverload().GetObserved() != 9 {
		t.Fatalf("overload status = %#v, %v", overload, err)
	}
}

func TestStaleInteractionResponseFailsBeforePayloadAcceptance(t *testing.T) {
	t.Parallel()
	request := &enginev1.RespondInteractionRequest{
		ClientId: "client-1", OwnershipEpoch: 6,
		ClientOperationId: "respond-1", RunId: "run-1",
		InteractionId: "interaction-1",
		ValueJson:     []byte(`{"approved":true}`),
	}
	status := enginev1.ValidateInteractionResponse(
		request, "client-1", 7, "run-1", "interaction-1",
	)
	if status.GetCode() != commonv1.ErrorCode_ERROR_CODE_STALE_CLIENT ||
		status.GetStaleClient().GetExpectedEpoch() != 7 {
		t.Fatalf("stale response status = %#v", status)
	}
	request.OwnershipEpoch = 7
	status = enginev1.ValidateInteractionResponse(
		request, "client-1", 7, "run-1", "interaction-1",
	)
	if status.GetCode() != commonv1.ErrorCode_ERROR_CODE_OK {
		t.Fatalf("valid interaction status = %#v", status)
	}
	request.ValueJson = []byte("not-json")
	if status = enginev1.ValidateInteractionResponse(
		request, "client-1", 7, "run-1", "interaction-1",
	); status.GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("malformed interaction status = %#v", status)
	}
}

func TestSnapshotDigestLifecycleAndImportSafety(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"version":"spice.agent.snapshot/v1alpha2","run_id":"run-1"}`)
	snapshot, err := enginev1.NewSnapshotEnvelope(
		context.Background(),
		snapshotAuthority(t),
		"run-1",
		42,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED,
		payload,
	)
	if err != nil {
		t.Fatal(err)
	}
	payload[0] = 'x'
	returnedPayload := snapshot.GetPayload()
	if len(returnedPayload) == 0 || returnedPayload[0] != '{' {
		t.Fatal("snapshot retained caller-owned payload")
	}
	oldSnapshot, valid := proto.Clone(snapshot).(*enginev1.SnapshotEnvelope)
	if !valid {
		t.Fatal("snapshot clone had unexpected type")
	}
	oldSnapshot.Format = "spice.agent.snapshot/v1alpha1"
	if err = enginev1.ValidateSnapshotEnvelope(oldSnapshot); err == nil {
		t.Fatal("v1alpha1 snapshot format succeeded after hard cut")
	}
	request := &enginev1.ImportSnapshotRequest{
		ClientId: "client-1", OwnershipEpoch: 1,
		ClientOperationId: "import-1", Snapshot: snapshot,
	}
	if err = enginev1.ValidateImportSnapshotRequest(context.Background(), request, snapshotAuthority(t), protocolLimits()); err != nil {
		t.Fatal(err)
	}
	snapshot.Sha256[0] ^= 0xff
	if err = enginev1.ValidateSnapshotEnvelope(snapshot); err == nil {
		t.Fatal("snapshot with wrong digest succeeded")
	}
	snapshot, err = enginev1.NewSnapshotEnvelope(
		context.Background(),
		snapshotAuthority(t),
		"run-1",
		42,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_COMPLETED,
		[]byte(`{"complete":true,"run_id":"run-1"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Snapshot = snapshot
	if err = enginev1.ValidateImportSnapshotRequest(context.Background(), request, snapshotAuthority(t), protocolLimits()); err == nil {
		t.Fatal("terminal snapshot import succeeded")
	}
}

func TestUnknownFieldsSurviveEngineRoundTrip(t *testing.T) {
	t.Parallel()
	event := runEvent(1, enginev1.EventKind_EVENT_KIND_RUN_STARTED, false)
	encoded, err := proto.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	encoded = protowire.AppendTag(encoded, 100, protowire.BytesType)
	encoded = protowire.AppendString(encoded, "future-field")
	var decoded enginev1.RunEvent
	if err = proto.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("unknown engine field was discarded")
	}
	if err = enginev1.ValidateRunEvent(&decoded); err != nil {
		t.Fatalf("known event fields rejected: %v", err)
	}
	roundTrip, err := proto.Marshal(&decoded)
	if err != nil || !bytes.Contains(roundTrip, []byte("future-field")) {
		t.Fatalf("unknown engine field round trip = %x, %v", roundTrip, err)
	}
}

func TestGeneratedServiceDescriptorFreezesOnlyTheProcessBoundary(t *testing.T) {
	t.Parallel()
	descriptor := enginev1.EngineService_ServiceDesc
	if descriptor.ServiceName != "spice.agent.engine.v1.EngineService" ||
		len(descriptor.Methods) != 9 || len(descriptor.Streams) != 2 ||
		descriptor.Streams[0].StreamName != "StreamEvents" ||
		descriptor.Streams[1].StreamName != "StreamInteractions" ||
		!descriptor.Streams[0].ServerStreams || descriptor.Streams[0].ClientStreams ||
		!descriptor.Streams[1].ServerStreams || descriptor.Streams[1].ClientStreams {
		t.Fatalf("service descriptor = %#v", descriptor)
	}
}

func FuzzEngineEnvelope(f *testing.F) {
	codec, err := enginev1.NewHMACSnapshotAuthority(bytes.Repeat([]byte{0x11}, 32), 7, bytes.Repeat([]byte{0x22}, 32))
	if err != nil {
		f.Fatal(err)
	}
	seed, err := proto.Marshal(runEvent(1, enginev1.EventKind_EVENT_KIND_RUN_STARTED, false))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{0xff, 0x01})
	f.Fuzz(func(_ *testing.T, data []byte) {
		var event enginev1.RunEvent
		if proto.Unmarshal(data, &event) == nil {
			_ = enginev1.ValidateRunEvent(&event)
		}
		var snapshot enginev1.SnapshotEnvelope
		if proto.Unmarshal(data, &snapshot) == nil {
			_ = enginev1.ValidateSnapshotEnvelope(&snapshot)
			_ = enginev1.ValidateImportSnapshotRequest(context.Background(), &enginev1.ImportSnapshotRequest{
				ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "fuzz-import", Snapshot: &snapshot,
			}, codec, protocolLimits())
		}
		var response enginev1.RespondInteractionRequest
		if proto.Unmarshal(data, &response) == nil {
			_ = enginev1.ValidateInteractionResponse(&response, "client", 1, "run", "interaction")
		}
	})
}

func validInitializeRequest() *enginev1.InitializeRequest {
	return &enginev1.InitializeRequest{
		Protocol:              commonv1.SupportedProtocolRange(),
		Client:                build("spice-agent-tui"),
		SupportedCapabilities: capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots", "tools"),
		RequiredCapabilities:  capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		RequestedLimits:       limits(2<<20, 64, 128, 4<<20, 4, 8),
	}
}

func definitionSet() *enginev1.DefinitionSet {
	return &enginev1.DefinitionSet{
		Revision: "catalog-v1",
		Definitions: []*enginev1.Definition{{
			Id: "coding", Revision: "v1", Model: "reasoning", MaxTurns: 8,
		}},
	}
}

func versionRange(major uint32) *commonv1.ProtocolRange {
	return &commonv1.ProtocolRange{
		Minimum: &commonv1.ProtocolVersion{Major: major},
		Maximum: &commonv1.ProtocolVersion{Major: major},
	}
}

func build(component string) *commonv1.BuildIdentity {
	return &commonv1.BuildIdentity{
		Component: component, Version: "v0.1.0-preview.1", Commit: "0123456789ab", GoVersion: "go1.26.5",
	}
}

func capabilities(names ...string) *commonv1.CapabilitySet {
	return &commonv1.CapabilitySet{Names: slices.Clone(names)}
}

func limits(messageBytes uint64, items, replayEvents uint32, replayBytes uint64, streams, runs uint32) *commonv1.Limits {
	return &commonv1.Limits{
		MaxMessageBytes: messageBytes, MaxCollectionItems: items,
		MaxReplayEvents: replayEvents, MaxReplayBytes: replayBytes,
		MaxConcurrentStreams: streams, MaxActiveRuns: runs,
	}
}

func protocolLimits() *commonv1.Limits {
	return limits(4<<20, 128, 256, 8<<20, 8, 32)
}

func snapshotAuthority(t *testing.T) *enginev1.HMACSnapshotAuthority {
	t.Helper()
	codec, err := enginev1.NewHMACSnapshotAuthority(
		bytes.Repeat([]byte{0x11}, 32),
		7,
		bytes.Repeat([]byte{0x22}, 32),
	)
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

func health() *commonv1.Health {
	return &commonv1.Health{
		State:  commonv1.HealthState_HEALTH_STATE_READY,
		Limits: limits(4<<20, 128, 256, 8<<20, 8, 32),
	}
}

func runEvent(sequence uint64, kind enginev1.EventKind, terminal bool) *enginev1.RunEvent {
	return &enginev1.RunEvent{
		RunId: "run-1", Sequence: sequence, UnixNano: 1,
		Kind: kind, PayloadJson: []byte(`{"ok":true}`), Terminal: terminal,
	}
}

package enginev1_test

import (
	"strings"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/proto"
)

func TestInitializeResponseAndServerFailureBoundaries(t *testing.T) {
	t.Parallel()
	request := validInitializeRequest()
	for name, response := range map[string]*enginev1.InitializeResponse{
		"nil":            nil,
		"missing status": {},
		"error status": {
			Status: &commonv1.Status{Code: commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, Message: "bad"},
		},
		"missing protocol": {Status: commonv1.OKStatus()},
	} {
		if err := enginev1.ValidateInitializeResponse(response); err == nil {
			t.Errorf("%s response succeeded", name)
		}
	}

	validResponse := enginev1.NegotiateInitialize(
		request, commonv1.SupportedProtocolRange(), build("spice-agentd"),
		capabilities("events", "snapshots"), protocolLimits(), health(), "client-1", 1,
	)
	for name, mutate := range map[string]func(*enginev1.InitializeResponse){
		"build":        func(value *enginev1.InitializeResponse) { value.Server = nil },
		"capabilities": func(value *enginev1.InitializeResponse) { value.Capabilities = nil },
		"limits":       func(value *enginev1.InitializeResponse) { value.Limits = nil },
		"health":       func(value *enginev1.InitializeResponse) { value.Health = nil },
		"client":       func(value *enginev1.InitializeResponse) { value.ClientId = "" },
		"epoch":        func(value *enginev1.InitializeResponse) { value.OwnershipEpoch = 0 },
	} {
		candidate, ok := proto.Clone(validResponse).(*enginev1.InitializeResponse)
		if !ok {
			t.Fatal("Protobuf clone changed initialize response type")
			return
		}
		mutate(candidate)
		if err := enginev1.ValidateInitializeResponse(candidate); err == nil {
			t.Errorf("malformed %s response succeeded", name)
		}
	}

	for name, response := range map[string]*enginev1.InitializeResponse{
		"nil request":      enginev1.NegotiateInitialize(nil, commonv1.SupportedProtocolRange(), build("server"), capabilities("events"), protocolLimits(), health(), "client", 1),
		"bad server build": enginev1.NegotiateInitialize(request, commonv1.SupportedProtocolRange(), nil, capabilities("events"), protocolLimits(), health(), "client", 1),
		"bad health":       enginev1.NegotiateInitialize(request, commonv1.SupportedProtocolRange(), build("server"), capabilities("events"), protocolLimits(), nil, "client", 1),
		"bad ownership":    enginev1.NegotiateInitialize(request, commonv1.SupportedProtocolRange(), build("server"), capabilities("events"), protocolLimits(), health(), "", 0),
	} {
		if response.GetStatus().GetCode() == commonv1.ErrorCode_ERROR_CODE_OK {
			t.Errorf("%s negotiation succeeded", name)
		}
	}
}

func TestMessageUnionAndRunRequestBoundaries(t *testing.T) {
	t.Parallel()
	validParts := []*enginev1.ContentPart{
		{Value: &enginev1.ContentPart_Text{Text: "hello"}},
		{Value: &enginev1.ContentPart_ToolCall{ToolCall: &enginev1.ToolCallPart{CallId: "call-1", Name: "read", ArgumentsJson: []byte(`{"path":"README.md"}`)}}},
		{Value: &enginev1.ContentPart_ToolResult{ToolResult: &enginev1.ToolResultPart{CallId: "call-1", Name: "read", ResultJson: []byte(`{"text":"ok"}`)}}},
		{Value: &enginev1.ContentPart_Extension{Extension: &enginev1.ExtensionPart{Namespace: "example.view", ValueJson: []byte(`{"kind":"table"}`)}}},
	}
	message := &enginev1.Message{Id: "message-1", Role: enginev1.MessageRole_MESSAGE_ROLE_ASSISTANT, Parts: validParts}
	if err := enginev1.ValidateMessage(message); err != nil {
		t.Fatal(err)
	}
	invalidMessages := []*enginev1.Message{
		nil,
		{},
		{Id: "message", Role: enginev1.MessageRole(9000), Parts: validParts[:1]},
		{Id: "message", Role: enginev1.MessageRole_MESSAGE_ROLE_USER},
		{Id: "message", Role: enginev1.MessageRole_MESSAGE_ROLE_USER, Parts: []*enginev1.ContentPart{nil}},
		{Id: "message", Role: enginev1.MessageRole_MESSAGE_ROLE_USER, Parts: []*enginev1.ContentPart{{}}},
		{Id: "message", Role: enginev1.MessageRole_MESSAGE_ROLE_USER, Parts: []*enginev1.ContentPart{{Value: (*enginev1.ContentPart_Text)(nil)}}},
		{Id: "message", Role: enginev1.MessageRole_MESSAGE_ROLE_USER, Parts: []*enginev1.ContentPart{{Value: &enginev1.ContentPart_ToolCall{}}}},
		{Id: "message", Role: enginev1.MessageRole_MESSAGE_ROLE_USER, Parts: []*enginev1.ContentPart{{Value: &enginev1.ContentPart_ToolResult{}}}},
		{Id: "message", Role: enginev1.MessageRole_MESSAGE_ROLE_USER, Parts: []*enginev1.ContentPart{{Value: &enginev1.ContentPart_Extension{}}}},
	}
	for index, invalid := range invalidMessages {
		if err := enginev1.ValidateMessage(invalid); err == nil {
			t.Errorf("invalid message %d succeeded", index)
		}
	}

	valid := validStartRunRequest()
	if err := enginev1.ValidateStartRunRequest(valid, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*enginev1.StartRunRequest){
		"definition": func(value *enginev1.StartRunRequest) { value.Definition = nil },
		"turns":      func(value *enginev1.StartRunRequest) { value.Definition.MaxTurns = 0 },
		"message":    func(value *enginev1.StartRunRequest) { value.Input = nil },
		"role":       func(value *enginev1.StartRunRequest) { value.Input.Role = enginev1.MessageRole_MESSAGE_ROLE_SYSTEM },
	} {
		candidate := validStartRunRequest()
		mutate(candidate)
		if err := enginev1.ValidateStartRunRequest(candidate, protocolLimits()); err == nil {
			t.Errorf("invalid start %s succeeded", name)
		}
	}
	if err := enginev1.ValidateStartRunRequest(nil, protocolLimits()); err == nil {
		t.Fatal("nil start request succeeded")
	}
}

func TestStreamReplayEventAndCancellationBoundaries(t *testing.T) {
	t.Parallel()
	request := &enginev1.StreamEventsRequest{
		ClientId: "client", OwnershipEpoch: 1, RunId: "run", ReplayLimit: 10, Tail: true,
	}
	if err := enginev1.ValidateStreamEventsRequest(request, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]*enginev1.StreamEventsRequest{
		"nil":          nil,
		"client":       {RunId: "run", ReplayLimit: 1},
		"zero replay":  {ClientId: "client", OwnershipEpoch: 1, RunId: "run"},
		"large replay": {ClientId: "client", OwnershipEpoch: 1, RunId: "run", ReplayLimit: 999},
	} {
		if err := enginev1.ValidateStreamEventsRequest(value, protocolLimits()); err == nil {
			t.Errorf("invalid stream %s succeeded", name)
		}
	}

	for name, event := range map[string]*enginev1.RunEvent{
		"nil":       nil,
		"timestamp": {RunId: "run", Sequence: 1, Kind: enginev1.EventKind_EVENT_KIND_RUN_STARTED},
		"terminal":  runEvent(1, enginev1.EventKind_EVENT_KIND_RUN_COMPLETED, false),
		"json":      {RunId: "run", Sequence: 1, UnixNano: 1, Kind: enginev1.EventKind_EVENT_KIND_RUN_STARTED, PayloadJson: []byte("bad")},
	} {
		if err := enginev1.ValidateRunEvent(event); err == nil {
			t.Errorf("invalid event %s succeeded", name)
		}
	}
	events := make([]*enginev1.RunEvent, protocolLimits().GetMaxReplayEvents()+1)
	if err := enginev1.ValidateEventBatch("run", 0, events, protocolLimits()); err == nil {
		t.Fatal("oversized event collection succeeded")
	}
	tiny := limits(1024, 4, 2, 1, 1, 1)
	if err := enginev1.ValidateEventBatch("run-1", 0, []*enginev1.RunEvent{runEvent(1, enginev1.EventKind_EVENT_KIND_RUN_STARTED, false)}, tiny); err == nil {
		t.Fatal("oversized event bytes succeeded")
	}

	if err := enginev1.ValidateCancelRunRequest(nil); err == nil {
		t.Fatal("nil cancellation succeeded")
	}
	if err := enginev1.ValidateCancelRunRequest(&enginev1.CancelRunRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "cancel", RunId: "run",
		Reason: strings.Repeat("x", 1025),
	}); err == nil {
		t.Fatal("unbounded cancellation reason succeeded")
	}
}

func TestInteractionAndSnapshotFailureBoundaries(t *testing.T) {
	t.Parallel()
	if status := enginev1.ValidateInteractionResponse(nil, "client", 1, "run", "interaction"); status.GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("nil interaction = %#v", status)
	}
	request := &enginev1.RespondInteractionRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "respond",
		RunId: "wrong", InteractionId: "interaction", ResponseId: "response", ValueJson: []byte(`true`),
	}
	if status := enginev1.ValidateInteractionResponse(request, "client", 1, "run", "interaction"); status.GetCode() != commonv1.ErrorCode_ERROR_CODE_CONFLICT {
		t.Fatalf("wrong interaction correlation = %#v", status)
	}

	valid, err := enginev1.NewSnapshotEnvelope("run", 1, enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED, []byte(`{"safe":true}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]*enginev1.SnapshotEnvelope{
		"nil":       nil,
		"format":    {Format: "unknown", RunId: "run", LastSequence: 1},
		"sequence":  {Format: enginev1.SnapshotFormat, RunId: "run"},
		"lifecycle": {Format: enginev1.SnapshotFormat, RunId: "run", LastSequence: 1, Lifecycle: enginev1.SnapshotLifecycle(9000), Payload: []byte("x")},
		"empty":     {Format: enginev1.SnapshotFormat, RunId: "run", LastSequence: 1, Lifecycle: enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED},
	} {
		if err = enginev1.ValidateSnapshotEnvelope(value); err == nil {
			t.Errorf("invalid snapshot %s succeeded", name)
		}
	}
	if _, err = enginev1.NewSnapshotEnvelope("", 0, enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_UNSPECIFIED, nil); err == nil {
		t.Fatal("invalid snapshot construction succeeded")
	}
	if err = enginev1.ValidateImportSnapshotRequest(nil); err == nil {
		t.Fatal("nil snapshot import succeeded")
	}
	if err = enginev1.ValidateImportSnapshotRequest(&enginev1.ImportSnapshotRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "import",
		NewRunId: "run-2", ExpectedStaticPlanFingerprint: "static",
		ExpectedPlanId: "plan", Snapshot: valid,
	}); err != nil {
		t.Fatal(err)
	}
}

func validStartRunRequest() *enginev1.StartRunRequest {
	return &enginev1.StartRunRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "start",
		Definition: &enginev1.AgentDefinitionRef{
			Id: "agent", Revision: "v1", Model: "model", MaxTurns: 4,
			ExpectedStaticPlanFingerprint: "static",
		},
		Input: &enginev1.Message{
			Id: "message", Role: enginev1.MessageRole_MESSAGE_ROLE_USER,
			Parts: []*enginev1.ContentPart{{Value: &enginev1.ContentPart_Text{Text: "hello"}}},
		},
	}
}

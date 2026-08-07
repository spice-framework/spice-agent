package grpcclient

import (
	"math"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/proto"
)

func TestWireValueRoundTrips(t *testing.T) {
	t.Parallel()
	connection := unaryTestConnection(t)

	version, err := protocolVersionToWire(connection.Protocol())
	if err != nil {
		t.Fatal(err)
	}
	if decoded, decodeErr := protocolVersionFromWire(version); decodeErr != nil || decoded != connection.Protocol() {
		t.Fatalf("protocol version = %#v, %v", decoded, decodeErr)
	}
	rangeValue, err := client.NewProtocolRange(connection.Protocol(), connection.Protocol())
	if err != nil {
		t.Fatal(err)
	}
	wireRange, err := protocolRangeToWire(rangeValue)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, decodeErr := protocolRangeFromWire(wireRange); decodeErr != nil || decoded != rangeValue {
		t.Fatalf("protocol range = %#v, %v", decoded, decodeErr)
	}
	wireBuild, err := buildToWire(connection.Server())
	if err != nil {
		t.Fatal(err)
	}
	if decoded, decodeErr := buildFromWire(wireBuild); decodeErr != nil || decoded != connection.Server() {
		t.Fatalf("build = %#v, %v", decoded, decodeErr)
	}
	wireLimits, err := limitsToWire(connection.Limits())
	if err != nil {
		t.Fatal(err)
	}
	if decoded, decodeErr := limitsFromWire(wireLimits); decodeErr != nil || decoded != connection.Limits() {
		t.Fatalf("limits = %#v, %v", decoded, decodeErr)
	}

	for wireState, publicState := range map[commonv1.HealthState]client.HealthState{
		commonv1.HealthState_HEALTH_STATE_STARTING: client.HealthStarting,
		commonv1.HealthState_HEALTH_STATE_READY:    client.HealthReady,
		commonv1.HealthState_HEALTH_STATE_DEGRADED: client.HealthDegraded,
		commonv1.HealthState_HEALTH_STATE_STOPPING: client.HealthStopping,
	} {
		reasons := []string(nil)
		if wireState == commonv1.HealthState_HEALTH_STATE_DEGRADED {
			reasons = []string{"dependency unavailable"}
		}
		decoded, decodeErr := healthFromWire(&commonv1.Health{State: wireState, DegradedReasons: reasons, Limits: wireLimits})
		if decodeErr != nil || decoded.State() != publicState {
			t.Fatalf("health %s = %#v, %v", wireState, decoded, decodeErr)
		}
	}

	envelope := unaryTestSnapshotEnvelope(t, "run-snapshot-round-trip")
	snapshot, err := snapshotFromWire(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, decodeErr := snapshotToWire(snapshot); decodeErr != nil || !proto.Equal(decoded, envelope) {
		t.Fatalf("snapshot round trip = %#v, %v", decoded, decodeErr)
	}
}

func TestWireConvertersRejectInvalidValues(t *testing.T) {
	t.Parallel()
	if _, err := protocolVersionToWire(client.ProtocolVersion{}); err == nil {
		t.Fatal("invalid public protocol version was accepted")
	}
	if _, err := protocolRangeToWire(client.ProtocolRange{}); err == nil {
		t.Fatal("invalid public protocol range was accepted")
	}
	if _, err := buildToWire(client.Build{}); err == nil {
		t.Fatal("invalid public build was accepted")
	}
	if _, err := limitsToWire(client.Limits{}); err == nil {
		t.Fatal("invalid public limits were accepted")
	}
	if _, err := protocolVersionFromWire(nil); err == nil {
		t.Fatal("nil protocol version was accepted")
	}
	if _, err := protocolRangeFromWire(nil); err == nil {
		t.Fatal("nil protocol range was accepted")
	}
	if _, err := buildFromWire(nil); err == nil {
		t.Fatal("nil build was accepted")
	}
	if _, err := limitsFromWire(nil); err == nil {
		t.Fatal("nil limits were accepted")
	}
	if _, err := healthFromWire(&commonv1.Health{
		State:  commonv1.HealthState_HEALTH_STATE_UNSPECIFIED,
		Limits: &commonv1.Limits{MaxMessageBytes: 1, MaxCollectionItems: 1, MaxReplayEvents: 1, MaxReplayBytes: 1, MaxConcurrentStreams: 1, MaxActiveRuns: 1},
	}); err == nil {
		t.Fatal("unsupported health was accepted")
	}
	if limitsFitPlatform(nil) {
		t.Fatal("nil limits fit the platform")
	}
	if platformMessageMaximum(math.MaxUint64) != math.MaxInt {
		t.Fatal("message maximum was not clamped")
	}
	if _, err := snapshotToWire(client.Snapshot{}); err == nil {
		t.Fatal("invalid snapshot was accepted")
	}
	if _, err := snapshotFromWire(nil); err == nil {
		t.Fatal("nil wire snapshot was accepted")
	}
	if _, err := catalogFromWire(&enginev1.DefinitionSet{
		Revision: "catalog", Definitions: []*enginev1.Definition{{Id: "", Revision: "revision", Model: "model", MaxTurns: 1}},
	}, unaryTestConnection(t).Limits()); err == nil {
		t.Fatal("invalid definition catalog was accepted")
	}
}

func TestEventWireTranslationCoversEveryProtocolKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		wire    enginev1.EventKind
		public  client.EventKind
		payload string
	}{
		{enginev1.EventKind_EVENT_KIND_RUN_STARTED, client.EventRunStarted, `{"definition":"coding"}`},
		{enginev1.EventKind_EVENT_KIND_RUN_COMPLETED, client.EventRunCompleted, ``},
		{enginev1.EventKind_EVENT_KIND_RUN_FAILED, client.EventRunFailed, `{"error":"run failed"}`},
		{enginev1.EventKind_EVENT_KIND_RUN_CANCELLED, client.EventRunCancelled, `{"error":"run cancelled"}`},
		{enginev1.EventKind_EVENT_KIND_TURN_STARTED, client.EventTurnStarted, `{"turn":1}`},
		{enginev1.EventKind_EVENT_KIND_TURN_COMPLETED, client.EventTurnCompleted, `{"turn":1}`},
		{enginev1.EventKind_EVENT_KIND_TURN_FAILED, client.EventTurnFailed, `{"error":"turn failed"}`},
		{enginev1.EventKind_EVENT_KIND_MODEL_STARTED, client.EventModelStarted, `{"turn":1,"operation_id":"model-operation"}`},
		{enginev1.EventKind_EVENT_KIND_MODEL_DELTA, client.EventModelDelta, `{"text":"delta"}`},
		{enginev1.EventKind_EVENT_KIND_MODEL_COMPLETED, client.EventModelCompleted, `{"input_tokens":2,"output_tokens":3,"metadata":[]}`},
		{enginev1.EventKind_EVENT_KIND_MODEL_FAILED, client.EventModelFailed, `{"code":"provider","message":"failed","retryable":true,"before_stream":false,"metadata":[]}`},
		{enginev1.EventKind_EVENT_KIND_TOOL_STARTED, client.EventToolStarted, `{"call_id":"call","name":"read"}`},
		{enginev1.EventKind_EVENT_KIND_TOOL_PROGRESS, client.EventToolProgress, `{"call_id":"call","message":"working"}`},
		{enginev1.EventKind_EVENT_KIND_TOOL_COMPLETED, client.EventToolCompleted, `{"call_id":"call","name":"read","error":""}`},
		{enginev1.EventKind_EVENT_KIND_TOOL_FAILED, client.EventToolFailed, `{"call_id":"call","name":"read","error":"failed","outcome":"definitive","retry":"allowed"}`},
		{enginev1.EventKind_EVENT_KIND_INTERACTION_STARTED, client.EventInteractionStarted, `{"id":"prompt","kind":"confirmation"}`},
		{enginev1.EventKind_EVENT_KIND_INTERACTION_COMPLETED, client.EventInteractionCompleted, `{"id":"prompt"}`},
		{enginev1.EventKind_EVENT_KIND_INTERACTION_FAILED, client.EventInteractionFailed, `{"id":"prompt","error":"failed"}`},
		{enginev1.EventKind_EVENT_KIND_INTERACTION_CANCELLED, client.EventInteractionCancelled, `{"id":"prompt","error":"cancelled"}`},
	}
	for index, test := range tests {
		t.Run(string(test.public), func(t *testing.T) {
			t.Parallel()
			terminal := map[client.EventKind]bool{
				client.EventRunCompleted: true, client.EventRunFailed: true, client.EventRunCancelled: true,
				client.EventTurnCompleted: true, client.EventTurnFailed: true,
				client.EventModelCompleted: true, client.EventModelFailed: true,
				client.EventToolCompleted: true, client.EventToolFailed: true,
				client.EventInteractionCompleted: true, client.EventInteractionFailed: true,
				client.EventInteractionCancelled: true,
			}[test.public]
			value := &enginev1.RunEvent{
				RunId: "run", Sequence: uint64(index + 1), UnixNano: time.Now().UnixNano(),
				Kind: test.wire, PayloadJson: []byte(test.payload), Terminal: terminal,
			}
			converted, err := eventFromWire(value)
			if err != nil || converted.Kind() != test.public {
				t.Fatalf("event = %#v, %v", converted, err)
			}
		})
	}
	if _, err := eventKindFromWire(enginev1.EventKind_EVENT_KIND_UNSPECIFIED); err == nil {
		t.Fatal("unsupported event kind was accepted")
	}
}

func TestEventPayloadDecoderRejectsAmbiguousOrInvalidJSON(t *testing.T) {
	t.Parallel()
	for name, payload := range map[string][]byte{
		"missing":   nil,
		"malformed": []byte(`{"text":`),
		"unknown":   []byte(`{"text":"ok","secret":"leak"}`),
		"trailing":  []byte(`{"text":"ok"}{}`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := eventDetailFromWire(client.EventModelDelta, payload); err == nil {
				t.Fatal("invalid payload was accepted")
			}
		})
	}
	if _, err := eventDetailFromWire(client.EventKind("future"), []byte(`{}`)); err == nil {
		t.Fatal("unsupported public event kind was accepted")
	}
}

func TestPendingInteractionWireTranslation(t *testing.T) {
	t.Parallel()
	value := &enginev1.PendingInteraction{
		RunId: "run", InteractionId: "interaction", Kind: "confirmation",
		Prompt: "continue?", SchemaJson: []byte(`{"type":"boolean"}`),
	}
	pending, err := pendingFromWire(value)
	if err != nil || pending.ID() != "interaction" {
		t.Fatalf("pending = %#v, %v", pending, err)
	}
	value.SchemaJson = []byte(`invalid`)
	if _, err = pendingFromWire(value); err == nil {
		t.Fatal("invalid pending schema was accepted")
	}
}

func TestConnectionWireDecoderRejectsEachInvalidComponent(t *testing.T) {
	t.Parallel()
	connection := unaryTestConnection(t)
	protocol, err := protocolVersionToWire(connection.Protocol())
	if err != nil {
		t.Fatal(err)
	}
	build, err := buildToWire(connection.Server())
	if err != nil {
		t.Fatal(err)
	}
	limits, err := limitsToWire(connection.Limits())
	if err != nil {
		t.Fatal(err)
	}
	health := &commonv1.Health{State: commonv1.HealthState_HEALTH_STATE_READY, Limits: limits}
	definition := &enginev1.Definition{Id: "definition", Revision: "revision", Model: "model", MaxTurns: 1}
	base := &enginev1.InitializeResponse{
		Status: commonv1.OKStatus(), Protocol: protocol, Server: build,
		Capabilities: &commonv1.CapabilitySet{Names: []string{"events"}}, Limits: limits,
		Health: health, ClientId: "client", OwnershipEpoch: 1,
		Definitions: &enginev1.DefinitionSet{Revision: "catalog", Definitions: []*enginev1.Definition{definition}},
	}
	mutations := []func(*enginev1.InitializeResponse){
		func(value *enginev1.InitializeResponse) { value.Protocol = nil },
		func(value *enginev1.InitializeResponse) { value.Server = nil },
		func(value *enginev1.InitializeResponse) { value.Limits = nil },
		func(value *enginev1.InitializeResponse) { value.Health = nil },
		func(value *enginev1.InitializeResponse) { value.Definitions = nil },
	}
	for _, mutate := range mutations {
		value := proto.CloneOf(base)
		mutate(value)
		if _, decodeErr := connectionFromWire(value); decodeErr == nil {
			t.Fatalf("invalid connection component was accepted: %#v", value)
		}
	}
}

package grpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestPlatformIntRejectsOverflow(t *testing.T) {
	t.Parallel()
	if value, ok := platformInt(uint64(math.MaxInt)); !ok || value != math.MaxInt {
		t.Fatalf("maximum platform int = %d, %t", value, ok)
	}
	if value, ok := platformInt(uint64(math.MaxInt) + 1); ok || value != 0 {
		t.Fatalf("overflowing platform int = %d, %t", value, ok)
	}
}

func TestEventToWirePreservesEveryEnvelopeKind(t *testing.T) {
	t.Parallel()
	kinds := []event.Kind{
		event.RunStarted, event.RunCompleted, event.RunFailed, event.RunCancelled,
		event.TurnStarted, event.TurnCompleted, event.TurnFailed,
		event.ModelStarted, event.ModelDelta, event.ModelCompleted, event.ModelFailed,
		event.ToolStarted, event.ToolProgress, event.ToolCompleted, event.ToolFailed,
		event.InteractionStarted, event.InteractionCompleted, event.InteractionFailed, event.InteractionCancelled,
	}
	for index, kind := range kinds {
		payload := []byte(`{"index":1}`)
		expectedPayload := payload
		switch kind {
		case event.ToolStarted:
			payload = toolStartedEventPayload(t)
			expectedPayload = []byte(`{"call_id":"call","name":"read"}`)
		case event.ToolCompleted:
			payload = toolTerminalEventPayload(t, kind, "", "")
			expectedPayload = []byte(`{"call_id":"call","name":"read","error":""}`)
		case event.ToolFailed:
			payload = toolTerminalEventPayload(t, kind, tool.ExecutionDefinitive, tool.RetryAllowed)
			expectedPayload = []byte(`{"call_id":"call","name":"read","error":"tool execution failed","outcome":"definitive","retry":"allowed"}`)
		}
		envelope := eventEnvelope(t, uint64(index+1), kind, payload)
		wire, err := eventToWire(envelope)
		if err != nil {
			t.Fatalf("convert %s: %v", kind, err)
		}
		if wire.GetRunId() != envelope.RunID() || wire.GetSequence() != envelope.Sequence() ||
			wire.GetUnixNano() != envelope.At().UnixNano() || string(wire.GetPayloadJson()) != string(expectedPayload) ||
			wire.GetTerminal() != envelope.Terminal() || wire.GetKind() == enginev1.EventKind_EVENT_KIND_UNSPECIFIED {
			t.Fatalf("lossy %s conversion: %#v", kind, wire)
		}
	}
}

func TestEventToWireRejectsCorruptToolTerminalAndProjectsOnlyLegacySafeFields(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		kind     event.Kind
		outcome  tool.ExecutionState
		retry    tool.RetryDisposition
		expected string
	}{
		{name: "completed", kind: event.ToolCompleted, expected: `{"call_id":"call","name":"read","error":""}`},
		{
			name: "failed", kind: event.ToolFailed, outcome: tool.ExecutionUncertain, retry: tool.RetryNever,
			expected: `{"call_id":"call","name":"read","error":"tool execution failed","outcome":"uncertain","retry":"never"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload := toolTerminalEventPayload(t, test.kind, test.outcome, test.retry)
			wire, err := eventToWire(eventEnvelope(t, 1, test.kind, payload))
			if err != nil {
				t.Fatal(err)
			}
			if string(wire.GetPayloadJson()) != test.expected || strings.Contains(string(wire.GetPayloadJson()), "SECRET") {
				t.Fatalf("legacy tool-terminal projection = %s", wire.GetPayloadJson())
			}
			otherKind := event.ToolCompleted
			if test.kind == event.ToolCompleted {
				otherKind = event.ToolFailed
			}
			if _, err = eventToWire(eventEnvelope(t, 2, otherKind, payload)); err == nil {
				t.Fatal("mismatched terminal occurrence kind succeeded")
			}
		})
	}
	legacy := []byte(`{"call_id":"call","name":"read","error":"SECRET"}`)
	if _, err := eventToWire(eventEnvelope(t, 3, event.ToolFailed, legacy)); err == nil {
		t.Fatal("legacy-only durable tool-terminal payload succeeded")
	}
}

func TestEventToWireRejectsCorruptToolStartAndProjectsOnlyLegacyIdentity(t *testing.T) {
	t.Parallel()
	envelope := eventEnvelope(t, 1, event.ToolStarted, toolStartedEventPayload(t))
	wire, err := eventToWire(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err = json.Unmarshal(wire.GetPayloadJson(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2 || payload["call_id"] == nil || payload["name"] == nil || payload["tool_plan_id"] != nil ||
		payload["capabilities"] != nil || payload["workspace_fingerprint"] != nil {
		t.Fatalf("legacy tool-start projection = %s", wire.GetPayloadJson())
	}
	corrupt := eventEnvelope(t, 2, event.ToolStarted, []byte(`{"call_id":"call","name":"read"}`))
	if _, err = eventToWire(corrupt); err == nil {
		t.Fatal("legacy-only durable tool-start payload succeeded")
	}
}

func toolStartedEventPayload(t *testing.T) []byte {
	t.Helper()
	definition, err := tool.NewDefinition(
		"read", "Read.", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe,
		tool.CapabilityFilesystemRead,
	)
	if err != nil {
		t.Fatal(err)
	}
	planID, _ := stage.NewPlanID("generation:daemon-wire")
	plan, err := agent.NewPlanIdentity(
		[]string{"provider:test"}, "daemon-wire:v1", "sha256:"+strings.Repeat("a", 64), planID, []tool.Definition{definition},
	)
	if err != nil {
		t.Fatal(err)
	}
	occurrence, err := agent.NewToolStartedOccurrence("call", "read", true, true, &definition, plan, 1)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := occurrence.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func toolTerminalEventPayload(
	t *testing.T,
	kind event.Kind,
	outcome tool.ExecutionState,
	retry tool.RetryDisposition,
) []byte {
	t.Helper()
	occurrence, err := agent.NewToolTerminalOccurrence(kind, "call", "read", outcome, retry)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := occurrence.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestStreamEventObservationPrevalidatesPageBeforeDisclosure(t *testing.T) {
	t.Parallel()
	limits := streamEventLimits(t)
	page := event.ReplayPage{
		EarliestSequence: 1, LatestSequence: 1, PageLastSequence: 0,
		Events: []event.Envelope{eventEnvelope(t, 1, event.RunStarted, nil)},
	}
	stream := newRecordingEventStream(t.Context())
	err := streamEventObservation(streamEventRequest("run-events", false), &fixtureEventObservation{
		page: page, ctx: t.Context(),
	}, limits, stream)
	if err != nil {
		t.Fatal(err)
	}
	responses := stream.Responses()
	if len(responses) != 1 || responses[0].GetEvent() != nil ||
		responses[0].GetControl().GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INTERNAL {
		t.Fatalf("invalid page disclosed partial events: %#v", responses)
	}
}

func TestStreamEventObservationReplaysAndTailsContiguously(t *testing.T) {
	t.Parallel()
	log := eventLogFixture(t, 8)
	if err := log.Append(eventEnvelope(t, 1, event.RunStarted, []byte(`{"state":"start"}`))); err != nil {
		t.Fatal(err)
	}
	page, err := log.Replay(t.Context(), event.ReplayRequest{
		AfterSequence: 0, MaxEvents: 8, MaxBytes: 1 << 20, Tail: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	observation := &fixtureEventObservation{page: page, ctx: t.Context()}
	stream := newRecordingEventStream(t.Context())
	limits := streamEventLimits(t)
	done := make(chan error, 1)
	go func() {
		done <- serveOwnedEventObservation(
			streamEventRequest("run-events", true), observation, limits, stream,
		)
	}()
	waitForEventSends(t, stream.sent, 2)
	if err = log.Append(eventEnvelope(t, 2, event.ModelDelta, []byte(`{"text":"ok"}`))); err != nil {
		t.Fatal(err)
	}
	log.Close()
	waitForEventSends(t, stream.sent, 1)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	responses := stream.Responses()
	if len(responses) != 3 || responses[0].GetEvent().GetSequence() != 1 ||
		!responses[1].GetControl().GetTailing() || responses[2].GetEvent().GetSequence() != 2 {
		t.Fatalf("replay/tail responses = %#v", responses)
	}
	if !observation.closed.Load() {
		t.Fatal("owned observation was not closed after sender exit")
	}
}

func TestStreamEventObservationReconnectFenceStopsOldSenderBeforeClose(t *testing.T) {
	t.Parallel()
	log := eventLogFixture(t, 8)
	if err := log.Append(eventEnvelope(t, 1, event.RunStarted, nil)); err != nil {
		t.Fatal(err)
	}
	observationCtx, cancelObservation := context.WithCancel(t.Context())
	page, err := log.Replay(observationCtx, event.ReplayRequest{
		AfterSequence: 0, MaxEvents: 8, MaxBytes: 1 << 20, Tail: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	observation := &fixtureEventObservation{page: page, ctx: observationCtx}
	stream := newRecordingEventStream(t.Context())
	limits := streamEventLimits(t)
	done := make(chan error, 1)
	go func() {
		done <- serveOwnedEventObservation(
			streamEventRequest("run-events", true), observation, limits, stream,
		)
	}()
	waitForEventSends(t, stream.sent, 2)
	cancelObservation()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if !observation.closed.Load() || len(stream.Responses()) != 2 {
		t.Fatalf("reconnect fence leaked a frame or lease: closed=%v responses=%#v", observation.closed.Load(), stream.Responses())
	}
	log.Close()
}

func TestStreamEventsReconnectWaitsForFlowControlBlockedSend(t *testing.T) {
	t.Parallel()
	root, cancelRoot := context.WithCancel(context.Background())
	sessions, err := daemon.NewSessionStore(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	rpcContext, cancelRPC := context.WithCancel(t.Context())
	lease, err := sessions.AcquireStream(rpcContext, session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancelRPC()
		lease.Close()
		cancelRoot()
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
		defer cancelShutdown()
		if shutdownErr := sessions.Shutdown(shutdownContext); shutdownErr != nil {
			t.Errorf("shutdown session store: %v", shutdownErr)
		}
	})

	observation := &leasedEventObservation{
		page: event.ReplayPage{EarliestSequence: 1}, lease: lease,
	}
	stream := newBlockingEventStream(rpcContext)
	limits := streamEventLimits(t)
	done := make(chan error, 1)
	go func() {
		done <- serveOwnedEventObservation(
			streamEventRequest("run-events", false), observation, limits, stream,
		)
	}()
	waitForEventSends(t, stream.sendStarted, 1)

	reconnectContext, cancelReconnect := context.WithTimeout(t.Context(), 100*time.Millisecond)
	_, reconnectErr := sessions.ReconnectContext(reconnectContext, session.ClientID(), session.Epoch())
	cancelReconnect()
	if !errors.Is(reconnectErr, context.DeadlineExceeded) {
		t.Fatalf("flow-control-blocked reconnect = %v", reconnectErr)
	}
	if err = sessions.Check(session.ClientID(), session.Epoch()); err != nil {
		t.Fatalf("timed-out reconnect advanced old epoch: %v", err)
	}
	if err = sessions.Check(session.ClientID(), session.Epoch()+1); !errors.Is(err, daemon.ErrStaleSession) {
		t.Fatalf("timed-out reconnect exposed a new epoch: %v", err)
	}
	if !errors.Is(context.Cause(lease.Context()), daemon.ErrStaleSession) {
		t.Fatalf("reconnect did not cancel observation lease: %v", context.Cause(lease.Context()))
	}
	select {
	case sendErr := <-done:
		t.Fatalf("server Send was assumed interruptible and returned early: %v", sendErr)
	default:
	}
	if observation.closed.Load() {
		t.Fatal("stream lease closed while its sender was still blocked")
	}

	cancelRPC()
	select {
	case sendErr := <-done:
		if status.Code(sendErr) != codes.Canceled {
			t.Fatalf("canceled blocked send = %v", sendErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceling the old RPC did not unblock its sender")
	}
	if !observation.closed.Load() {
		t.Fatal("sender exit did not release the reconnect fence")
	}

	next, err := sessions.ReconnectContext(t.Context(), session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatalf("reconnect retry after old RPC join: %v", err)
	}
	if next.ClientID() != session.ClientID() || next.Epoch() != session.Epoch()+1 {
		t.Fatalf("reconnect retry ownership = %q/%d", next.ClientID(), next.Epoch())
	}
}

func TestFinishEventTailReturnsExactSubscriberCapacity(t *testing.T) {
	t.Parallel()
	log := eventLogFixture(t, 1)
	if err := log.Append(eventEnvelope(t, 1, event.RunStarted, nil)); err != nil {
		t.Fatal(err)
	}
	page, err := log.Replay(t.Context(), event.ReplayRequest{
		AfterSequence: 0, MaxEvents: 1, MaxBytes: 1 << 20, Tail: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = log.Append(eventEnvelope(t, 2, event.ModelDelta, []byte(`{"text":"queued"}`))); err != nil {
		t.Fatal(err)
	}
	if err = log.Append(eventEnvelope(t, 3, event.ModelDelta, []byte(`{"text":"refused"}`))); err != nil {
		t.Fatal(err)
	}
	exhaustion := page.Tail.Wait(t.Context())
	typed, ok := errors.AsType[*event.ResourceExhaustedError](exhaustion)
	if !ok {
		t.Fatalf("tail failure = %v", exhaustion)
	}
	stream := newRecordingEventStream(t.Context())
	if err = finishEventTail(stream, streamEventLimits(t), page, exhaustion); err != nil {
		t.Fatal(err)
	}
	responses := stream.Responses()
	if len(responses) != 1 || responses[0].GetControl().GetLastDeliveredSequence() != typed.LastDelivered ||
		responses[0].GetControl().GetStatus().GetOverload().GetResource() != typed.Resource() ||
		responses[0].GetControl().GetStatus().GetOverload().GetLimit() != typed.Limit() ||
		responses[0].GetControl().GetStatus().GetOverload().GetObserved() != typed.Observed() {
		t.Fatalf("tail capacity control = %#v for %v", responses, exhaustion)
	}
}

func TestFinishEventTailTreatsReconnectCancellationAsCleanFence(t *testing.T) {
	t.Parallel()
	stream := newRecordingEventStream(t.Context())
	if err := finishEventTail(stream, streamEventLimits(t), event.ReplayPage{}, context.Canceled); err != nil {
		t.Fatal(err)
	}
	if responses := stream.Responses(); len(responses) != 0 {
		t.Fatalf("reconnect cancellation emitted an application frame: %#v", responses)
	}
}

func TestSendReplayFailureTreatsReconnectCancellationAsCleanFence(t *testing.T) {
	t.Parallel()
	stream := newRecordingEventStream(t.Context())
	service := &engineService{}
	if err := service.sendReplayFailure(stream, streamEventLimits(t), 4, context.Canceled); err != nil {
		t.Fatal(err)
	}
	if responses := stream.Responses(); len(responses) != 0 {
		t.Fatalf("reconnect cancellation emitted an application frame: %#v", responses)
	}
}

func TestStreamEventsValidatesBeforeHostAndDoesNotDiscloseOwnership(t *testing.T) {
	t.Parallel()
	host := eventGRPCHostFixture(t)
	api, ctx, initialized := initializeLifecycleGRPC(t, host, []string{"events"})

	invalid := receiveOnlyEventControl(t, api, ctx, &enginev1.StreamEventsRequest{
		ClientId: initialized.GetClientId(), OwnershipEpoch: initialized.GetOwnershipEpoch(), ReplayLimit: 1,
	})
	if invalid.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT || host.calls.Load() != 0 {
		t.Fatalf("invalid request = %#v, host calls %d", invalid, host.calls.Load())
	}
	invalidHost := receiveOnlyEventControl(t, api, ctx, streamEventRequestForSession(initialized, "run-invalid-host"))
	if invalidHost.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INTERNAL {
		t.Fatalf("nil host observation = %#v", invalidHost)
	}

	host.replayErr = daemon.ErrHostedRunUnavailable
	first := receiveOnlyEventControl(t, api, ctx, streamEventRequestForSession(initialized, "run-private-a"))
	second := receiveOnlyEventControl(t, api, ctx, streamEventRequestForSession(initialized, "run-private-b"))
	if first.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_NOT_FOUND ||
		!proto.Equal(first.GetStatus(), second.GetStatus()) || first.GetStatus().GetDetail() != nil {
		t.Fatalf("run ownership was disclosed: first=%#v second=%#v", first, second)
	}
}

func TestStreamEventsReturnsExactGapAndCapacityRecovery(t *testing.T) {
	t.Parallel()
	host := eventGRPCHostFixture(t)
	api, ctx, initialized := initializeLifecycleGRPC(t, host, []string{"events"})
	host.replayErr = &event.OutOfRangeError{RequestedAfter: 2, Earliest: 5, Latest: 9, RecoveryAfter: 4}
	request := streamEventRequestForSession(initialized, "run-gap")
	request.AfterSequence = 2
	gap := receiveOnlyEventControl(t, api, ctx, request)
	if gap.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_OUT_OF_RANGE ||
		gap.GetEarliestSequence() != 5 || gap.GetLatestSequence() != 9 || gap.GetLastDeliveredSequence() != 4 ||
		gap.GetStatus().GetReplayBounds().GetRecoverySequence() != 4 {
		t.Fatalf("gap recovery = %#v", gap)
	}
	host.replayErr = &event.OutOfRangeError{RequestedAfter: 3, Earliest: 5, Latest: 9, RecoveryAfter: 4}
	invalidGap := receiveOnlyEventControl(t, api, ctx, request)
	if invalidGap.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INTERNAL ||
		invalidGap.GetEarliestSequence() != 0 || invalidGap.GetLatestSequence() != 0 {
		t.Fatalf("inconsistent host gap was trusted: %#v", invalidGap)
	}

	exhaustionLog := eventLogFixture(t, 2)
	if err := exhaustionLog.Append(eventEnvelope(t, 1, event.RunStarted, []byte(`{"large":"payload"}`))); err != nil {
		t.Fatal(err)
	}
	_, exhaustion := exhaustionLog.Replay(t.Context(), event.ReplayRequest{MaxEvents: 1, MaxBytes: 1})
	if exhaustion == nil {
		t.Fatal("fixture did not exhaust replay bytes")
	}
	host.replayErr = exhaustion
	capacity := receiveOnlyEventControl(t, api, ctx, streamEventRequestForSession(initialized, "run-capacity"))
	typed, ok := errors.AsType[*event.ResourceExhaustedError](exhaustion)
	if !ok || capacity.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED ||
		capacity.GetLastDeliveredSequence() != typed.LastDelivered ||
		capacity.GetStatus().GetOverload().GetResource() != typed.Resource() ||
		capacity.GetStatus().GetOverload().GetLimit() != typed.Limit() ||
		capacity.GetStatus().GetOverload().GetObserved() != typed.Observed() {
		t.Fatalf("capacity recovery = %#v for %v", capacity, exhaustion)
	}
}

func TestStreamEventsRejectsStaleEpochAndPropagatesContext(t *testing.T) {
	t.Parallel()
	host := eventGRPCHostFixture(t)
	api, ctx, initialized := initializeLifecycleGRPC(t, host, []string{"events"})
	reconnect := grpcInitializeRequest(host.health.Limits())
	reconnect.SupportedCapabilities = &commonv1.CapabilitySet{Names: []string{"events"}}
	reconnect.RequiredCapabilities = &commonv1.CapabilitySet{Names: []string{"events"}}
	reconnect.ReconnectClaim = &enginev1.ReconnectClaim{
		ClientId: initialized.GetClientId(), ExpectedOwnershipEpoch: initialized.GetOwnershipEpoch(),
	}
	next, err := api.Initialize(ctx, reconnect)
	if err != nil || next.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_OK {
		t.Fatalf("reconnect = %#v, %v", next, err)
	}
	stale := receiveOnlyEventControl(t, api, ctx, streamEventRequestForSession(initialized, "run-stale"))
	if stale.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_STALE_CLIENT || host.calls.Load() != 0 {
		t.Fatalf("stale stream = %#v, calls %d", stale, host.calls.Load())
	}

	host.block = true
	cancelCtx, cancel := context.WithCancel(ctx)
	stream, err := api.StreamEvents(cancelCtx, streamEventRequestForSession(next, "run-cancel"))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_, err = stream.Recv()
	if status.Code(err) != codes.Canceled {
		t.Fatalf("canceled stream error = %v", err)
	}
}

type fixtureEventObservation struct {
	page   event.ReplayPage
	ctx    context.Context //nolint:containedctx // direct stream fixture owns this lifetime.
	closed atomic.Bool
}

func (observation *fixtureEventObservation) Page() event.ReplayPage   { return observation.page }
func (observation *fixtureEventObservation) Context() context.Context { return observation.ctx }
func (observation *fixtureEventObservation) Close()                   { observation.closed.Store(true) }

type leasedEventObservation struct {
	page   event.ReplayPage
	lease  *daemon.StreamLease
	closed atomic.Bool
}

func (observation *leasedEventObservation) Page() event.ReplayPage { return observation.page }
func (observation *leasedEventObservation) Context() context.Context {
	return observation.lease.Context()
}

func (observation *leasedEventObservation) Close() {
	observation.closed.Store(true)
	observation.lease.Close()
}

type recordingEventStream struct {
	ctx       context.Context //nolint:containedctx // direct gRPC stream fixture owns this lifetime.
	mu        sync.Mutex
	responses []*enginev1.StreamEventsResponse
	sent      chan struct{}
}

func newRecordingEventStream(ctx context.Context) *recordingEventStream {
	return &recordingEventStream{ctx: ctx, sent: make(chan struct{}, 16)}
}

type blockingEventStream struct {
	*recordingEventStream
	sendStarted chan struct{}
	startOnce   sync.Once
}

func newBlockingEventStream(ctx context.Context) *blockingEventStream {
	return &blockingEventStream{
		recordingEventStream: newRecordingEventStream(ctx), sendStarted: make(chan struct{}),
	}
}

func (stream *blockingEventStream) Send(*enginev1.StreamEventsResponse) error {
	stream.startOnce.Do(func() { close(stream.sendStarted) })
	<-stream.Context().Done()
	return stream.Context().Err()
}

func (stream *recordingEventStream) Send(value *enginev1.StreamEventsResponse) error {
	if err := stream.ctx.Err(); err != nil {
		return err
	}
	stream.mu.Lock()
	stream.responses = append(stream.responses, proto.CloneOf(value))
	stream.mu.Unlock()
	stream.sent <- struct{}{}
	return nil
}

func (stream *recordingEventStream) Responses() []*enginev1.StreamEventsResponse {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	result := make([]*enginev1.StreamEventsResponse, len(stream.responses))
	for index := range stream.responses {
		result[index] = proto.CloneOf(stream.responses[index])
	}
	return result
}

func (*recordingEventStream) SetHeader(metadata.MD) error     { return nil }
func (*recordingEventStream) SendHeader(metadata.MD) error    { return nil }
func (*recordingEventStream) SetTrailer(metadata.MD)          {}
func (stream *recordingEventStream) Context() context.Context { return stream.ctx }
func (*recordingEventStream) SendMsg(any) error               { return nil }
func (*recordingEventStream) RecvMsg(any) error               { return io.EOF }

type eventGRPCHost struct {
	*grpcFixtureHost
	calls     atomic.Int32
	replayErr error
	block     bool
}

func eventGRPCHostFixture(t *testing.T) *eventGRPCHost {
	t.Helper()
	_, _, health, definitions := wireFixtureValues(t)
	description, err := daemon.NewRunHostDescription(definitions, health)
	if err != nil {
		t.Fatal(err)
	}
	return &eventGRPCHost{grpcFixtureHost: &grpcFixtureHost{description: description, health: health}}
}

func (host *eventGRPCHost) ReplayEvents(
	ctx context.Context,
	_ daemon.Session,
	_ client.RunRef,
	_ event.ReplayRequest,
) (ownedEventObservation, error) {
	host.calls.Add(1)
	if host.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, host.replayErr
}

func eventEnvelope(t *testing.T, sequence uint64, kind event.Kind, data []byte) event.Envelope {
	t.Helper()
	value, err := event.Reconstruct(
		"run-events", sequence, time.Unix(1_700_000_000, int64(sequence)), kind, data,
	)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func eventLogFixture(t *testing.T, subscriberEvents int) *event.Log {
	t.Helper()
	limits := event.DefaultLogLimits()
	limits.SubscriberMaxEvents = subscriberEvents
	limits.SubscriberMaxBytes = 1 << 20
	log, err := event.NewLog("run-events", limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(log.Close)
	return log
}

func streamEventLimits(t *testing.T) *commonv1.Limits {
	t.Helper()
	_, limits, _, _ := wireFixtureValues(t)
	wire, err := limitsToWire(limits)
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func streamEventRequest(runID string, tail bool) *enginev1.StreamEventsRequest {
	return &enginev1.StreamEventsRequest{
		ClientId: "0123456789abcdef0123456789abcdef", OwnershipEpoch: 1,
		RunId: runID, ReplayLimit: 8, Tail: tail,
	}
}

func streamEventRequestForSession(initialized *enginev1.InitializeResponse, runID string) *enginev1.StreamEventsRequest {
	request := streamEventRequest(runID, false)
	request.ClientId = initialized.GetClientId()
	request.OwnershipEpoch = initialized.GetOwnershipEpoch()
	return request
}

func receiveOnlyEventControl(
	t *testing.T,
	api enginev1.EngineServiceClient,
	ctx context.Context,
	request *enginev1.StreamEventsRequest,
) *enginev1.StreamControl {
	t.Helper()
	stream, err := api.StreamEvents(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if response.GetEvent() != nil || response.GetControl() == nil {
		t.Fatalf("expected only a control frame, got %#v", response)
	}
	if _, err = stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected clean stream completion, got %v", err)
	}
	return response.GetControl()
}

func waitForEventSends(t *testing.T, sent <-chan struct{}, count int) {
	t.Helper()
	for range count {
		select {
		case <-sent:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for event stream send")
		}
	}
}

var (
	_ grpc.ServerStreamingServer[enginev1.StreamEventsResponse] = (*recordingEventStream)(nil)
	_ grpc.ServerStreamingServer[enginev1.StreamEventsResponse] = (*blockingEventStream)(nil)
)

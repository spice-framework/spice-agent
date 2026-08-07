package grpcclient

import (
	"context"
	"errors"
	"io"
	"math"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestEventAndInteractionStreamNextBoundaries(t *testing.T) {
	t.Parallel()
	eventLifetime, _ := newStreamLifetime(nil)
	eventFrames := make(chan streamResult[client.EventFrame], 1)
	eventFrames <- streamResult[client.EventFrame]{err: errors.New("stream result")}
	eventStream := &eventStream{lifetime: eventLifetime, initial: []client.EventFrame{{}}, frames: eventFrames}
	if _, err := eventStream.Next(nil); //nolint:staticcheck // Boundary verifies nil-context rejection.
	errorCode(err) != client.ErrorInvalidArgument {
		t.Fatalf("nil event context = %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := eventStream.Next(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled event context = %v", err)
	}
	if _, err := eventStream.Next(t.Context()); err != nil {
		t.Fatalf("initial event = %v", err)
	}
	if _, err := eventStream.Next(t.Context()); err == nil || err.Error() != "stream result" {
		t.Fatalf("event stream result = %v", err)
	}
	close(eventFrames)
	if _, err := eventStream.Next(t.Context()); !errors.Is(err, io.EOF) {
		t.Fatalf("event EOF = %v", err)
	}
	if err := eventStream.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := eventStream.Next(t.Context()); !errors.Is(err, client.ErrClosed) {
		t.Fatalf("closed event stream = %v", err)
	}

	interactionLifetime, _ := newStreamLifetime(nil)
	interactionFrames := make(chan streamResult[client.InteractionFrame], 1)
	interactionFrames <- streamResult[client.InteractionFrame]{err: errors.New("interaction result")}
	interactionStream := &interactionStream{
		lifetime: interactionLifetime, initial: []client.InteractionFrame{{}}, frames: interactionFrames,
	}
	if _, err := interactionStream.Next(nil); //nolint:staticcheck // Boundary verifies nil-context rejection.
	errorCode(err) != client.ErrorInvalidArgument {
		t.Fatalf("nil interaction context = %v", err)
	}
	if _, err := interactionStream.Next(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled interaction context = %v", err)
	}
	if _, err := interactionStream.Next(t.Context()); err != nil {
		t.Fatalf("initial interaction = %v", err)
	}
	if _, err := interactionStream.Next(t.Context()); err == nil || err.Error() != "interaction result" {
		t.Fatalf("interaction stream result = %v", err)
	}
	if err := interactionStream.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := interactionStream.Next(t.Context()); !errors.Is(err, client.ErrClosed) {
		t.Fatalf("closed interaction stream = %v", err)
	}
}

func TestEventReplayPageAndTailTranslation(t *testing.T) {
	t.Parallel()
	connection := unaryTestConnection(t)
	run := mustRun(t, "run-events")
	cursor, err := client.NewCursor(run, 0)
	if err != nil {
		t.Fatal(err)
	}
	options, err := client.NewEventStreamOptions(2, true, connection.Limits())
	if err != nil {
		t.Fatal(err)
	}
	wireLimits, err := limitsToWire(connection.Limits())
	if err != nil {
		t.Fatal(err)
	}
	pageLast := uint64(1)
	control := &enginev1.StreamControl{
		Status: commonv1.OKStatus(), EarliestSequence: 1, LatestSequence: 1,
		LastDeliveredSequence: 1, PageLastSequence: &pageLast, Tailing: true,
	}
	pageStream := newScriptedServerStream(
		t.Context(),
		eventResponse(wireEvent("run-events", 1, enginev1.EventKind_EVENT_KIND_RUN_STARTED, `{"definition":"coding"}`)),
		eventControlResponse(control),
	)
	frames, acceptedControl, err := receiveEventPage(pageStream, cursor, options, connection.Limits(), wireLimits)
	if err != nil || len(frames) != 2 || acceptedControl != control {
		t.Fatalf("event page = %#v, %#v, %v", frames, acceptedControl, err)
	}

	tailLifetime, _ := newStreamLifetime(nil)
	tailStream := newScriptedServerStream(
		t.Context(),
		eventResponse(wireEvent("run-events", 2, enginev1.EventKind_EVENT_KIND_MODEL_DELTA, `{"text":"tail"}`)),
	)
	output := make(chan streamResult[client.EventFrame], 2)
	receiveEventTail(tailLifetime, tailStream, run, control, wireLimits, output)
	first := <-output
	second := <-output
	if first.err != nil || first.value.Kind() != client.EventFrameEvent || !errors.Is(second.err, io.EOF) {
		t.Fatalf("event tail results = %#v, %#v", first, second)
	}
}

func TestEventReplayDoesNotPreallocateNegotiatedMaximum(t *testing.T) {
	t.Parallel()
	limits, err := client.NewLimits(1<<20, 1, math.MaxUint32, math.MaxUint32, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	run := mustRun(t, "run-bounded-allocation")
	cursor, err := client.NewCursor(run, 0)
	if err != nil {
		t.Fatal(err)
	}
	options, err := client.NewEventStreamOptions(math.MaxUint32, false, limits)
	if err != nil {
		t.Fatal(err)
	}
	pageLast := uint64(0)
	control := &enginev1.StreamControl{
		Status: commonv1.OKStatus(), EarliestSequence: 1, LatestSequence: 0,
		LastDeliveredSequence: 0, PageLastSequence: &pageLast,
	}
	wireLimits := &commonv1.Limits{MaxMessageBytes: 1 << 20}
	frames, _, err := receiveEventPage(
		newScriptedServerStream(t.Context(), eventControlResponse(control)), cursor, options, limits, wireLimits,
	)
	if err != nil || len(frames) != 1 {
		t.Fatalf("maximum replay page = %d frames, %v", len(frames), err)
	}
	if capacity := cap(frames); capacity > 16 {
		t.Fatalf("maximum replay limit preallocated %d frames", capacity)
	}
}

func TestEventReplayRejectsCorrelationAndErrorControl(t *testing.T) {
	t.Parallel()
	connection := unaryTestConnection(t)
	run := mustRun(t, "run-events")
	cursor, err := client.NewCursor(run, 3)
	if err != nil {
		t.Fatal(err)
	}
	options, err := client.NewEventStreamOptions(1, false, connection.Limits())
	if err != nil {
		t.Fatal(err)
	}
	wireLimits, err := limitsToWire(connection.Limits())
	if err != nil {
		t.Fatal(err)
	}
	wrong := newScriptedServerStream(t.Context(), eventResponse(
		wireEvent("different-run", 4, enginev1.EventKind_EVENT_KIND_MODEL_DELTA, `{"text":"bad"}`),
	))
	if _, _, err = receiveEventPage(wrong, cursor, options, connection.Limits(), wireLimits); errorCode(err) != client.ErrorInternal {
		t.Fatalf("wrong event correlation = %v", err)
	}
	after := uint64(3)
	failure := failedStatus(commonv1.ErrorCode_ERROR_CODE_OUT_OF_RANGE, func(value *commonv1.Status) {
		value.Detail = &commonv1.Status_ReplayBounds{ReplayBounds: &commonv1.ReplayBounds{
			RequestedAfterSequence: after, EarliestSequence: 5, LatestSequence: 7, RecoverySequence: 4,
		}}
	})
	failureStream := newScriptedServerStream(t.Context(), eventControlResponse(&enginev1.StreamControl{Status: failure}))
	_, _, err = receiveEventPage(failureStream, cursor, options, connection.Limits(), wireLimits)
	if _, ok := errors.AsType[*client.CursorGapError](err); !ok {
		t.Fatalf("cursor gap = %T %v", err, err)
	}
}

func TestInteractionPageAndTailTranslation(t *testing.T) {
	t.Parallel()
	connection := unaryTestConnection(t)
	wireLimits, err := limitsToWire(connection.Limits())
	if err != nil {
		t.Fatal(err)
	}
	pending := wirePending("run-interactions", "prompt")
	snapshot := &enginev1.InteractionSnapshot{Revision: 1, Pending: []*enginev1.PendingInteraction{pending}}
	control := &enginev1.InteractionStreamControl{
		Status: commonv1.OKStatus(), LatestRevision: 1, PageLastRevision: 1, Tailing: true,
	}
	pageStream := newScriptedServerStream(t.Context(), interactionSnapshotResponse(snapshot), interactionControlResponse(control))
	frames, validator, err := receiveInteractionPage(
		pageStream, client.NewInteractionStreamOptions(true), connection.Limits(), wireLimits,
	)
	if err != nil || len(frames) != 2 || validator == nil {
		t.Fatalf("interaction page = %#v, %#v, %v", frames, validator, err)
	}

	delta := &enginev1.InteractionDelta{
		Revision: 2, Kind: enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_OPENED,
		Interaction: wirePending("run-interactions", "second"),
	}
	tailLifetime, _ := newStreamLifetime(nil)
	tailStream := newScriptedServerStream(t.Context(), interactionDeltaResponse(delta))
	output := make(chan streamResult[client.InteractionFrame], 2)
	receiveInteractionTail(tailLifetime, tailStream, validator, wireLimits, output)
	first := <-output
	second := <-output
	if first.err != nil || first.value.Kind() != client.InteractionFrameUpdate || !errors.Is(second.err, io.EOF) {
		t.Fatalf("interaction tail results = %#v, %#v", first, second)
	}

	delta.Kind = enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_CLOSED
	if update, convertErr := publicInteractionDelta(delta); convertErr != nil || update.Kind() != client.InteractionClosed {
		t.Fatalf("closed delta = %#v, %v", update, convertErr)
	}
	delta.Kind = enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_UNSPECIFIED
	if _, convertErr := publicInteractionDelta(delta); convertErr == nil {
		t.Fatal("unsupported interaction delta was accepted")
	}
}

func TestStreamLifetimeWaitAndInterrupt(t *testing.T) {
	t.Parallel()
	lifetime, _ := newStreamLifetime(nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := lifetime.waitFor(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait timeout = %v", err)
	}
	lifetime.finishRPC()
	if err := lifetime.waitFor(t.Context()); err != nil {
		t.Fatal(err)
	}
	interruptCause := errors.New("transport failed")
	lifetime.interrupt(interruptCause)
	if !errors.Is(lifetime.cause(), interruptCause) {
		t.Fatalf("interrupt cause = %v", lifetime.cause())
	}
	lifetime.finish()
	lifetime.finish()
	lifetime.close()
	lifetime.close()
}

func TestSessionStreamOpeningRejectsInvalidInputsAndTransportFailure(t *testing.T) {
	t.Parallel()
	connection := unaryTestConnection(t)
	session := unaryTestSession(t, connection, &unaryEngineClient{})
	run := mustRun(t, "run")
	cursor, err := client.NewCursor(run, 0)
	if err != nil {
		t.Fatal(err)
	}
	options, err := client.NewEventStreamOptions(1, false, connection.Limits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.Events(t.Context(), client.Cursor{}, options); errorCode(err) != client.ErrorInvalidArgument {
		t.Fatalf("invalid cursor = %v", err)
	}
	if _, err = session.Events(t.Context(), cursor, client.EventStreamOptions{}); errorCode(err) != client.ErrorInvalidArgument {
		t.Fatalf("invalid options = %v", err)
	}
	if _, err = session.Events(nil, cursor, options); //nolint:staticcheck // Boundary verifies nil-context rejection.
	errorCode(err) != client.ErrorInvalidArgument {
		t.Fatalf("nil event open context = %v", err)
	}
	if _, err = session.Events(t.Context(), cursor, options); errorCode(err) != client.ErrorInternal {
		t.Fatalf("event transport failure = %v", err)
	}
	if _, err = session.Interactions(nil, client.NewInteractionStreamOptions(false)); //nolint:staticcheck // Boundary verifies nil-context rejection.
	errorCode(err) != client.ErrorInvalidArgument {
		t.Fatalf("nil interaction open context = %v", err)
	}
	if _, err = session.Interactions(t.Context(), client.NewInteractionStreamOptions(false)); errorCode(err) != client.ErrorInternal {
		t.Fatalf("interaction transport failure = %v", err)
	}
}

func TestEventTailRejectsProtocolViolations(t *testing.T) {
	t.Parallel()
	run := mustRun(t, "run-events")
	connection := unaryTestConnection(t)
	wireLimits, err := limitsToWire(connection.Limits())
	if err != nil {
		t.Fatal(err)
	}
	failure := &commonv1.Status{
		Code: commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE, Message: "tail unavailable", Retryable: true,
	}
	tests := []struct {
		name    string
		control *enginev1.StreamControl
		frame   *enginev1.StreamEventsResponse
		code    client.ErrorCode
	}{
		{
			name: "non-tailing data", control: &enginev1.StreamControl{LastDeliveredSequence: 0},
			frame: eventResponse(wireEvent("run-events", 1, enginev1.EventKind_EVENT_KIND_RUN_STARTED, `{"definition":"coding"}`)),
			code:  client.ErrorInternal,
		},
		{
			name: "sequence gap", control: &enginev1.StreamControl{Tailing: true, LastDeliveredSequence: 1},
			frame: eventResponse(wireEvent("run-events", 3, enginev1.EventKind_EVENT_KIND_MODEL_DELTA, `{"text":"gap"}`)),
			code:  client.ErrorInternal,
		},
		{
			name: "invalid event payload", control: &enginev1.StreamControl{Tailing: true},
			frame: eventResponse(wireEvent("run-events", 1, enginev1.EventKind_EVENT_KIND_MODEL_DELTA, `{"text":`)),
			code:  client.ErrorInternal,
		},
		{
			name: "success control in tail", control: &enginev1.StreamControl{Tailing: true},
			frame: eventControlResponse(&enginev1.StreamControl{Status: commonv1.OKStatus()}),
			code:  client.ErrorInternal,
		},
		{
			name: "application failure", control: &enginev1.StreamControl{Tailing: true},
			frame: eventControlResponse(&enginev1.StreamControl{Status: failure}),
			code:  client.ErrorUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lifetime, _ := newStreamLifetime(nil)
			output := make(chan streamResult[client.EventFrame], 1)
			receiveEventTail(
				lifetime, newScriptedServerStream(t.Context(), test.frame), run, test.control, wireLimits, output,
			)
			if result := <-output; errorCode(result.err) != test.code {
				t.Fatalf("tail error = %T %v, want %s", result.err, result.err, test.code)
			}
		})
	}
}

func TestInteractionTailRejectsProtocolViolations(t *testing.T) {
	t.Parallel()
	connection := unaryTestConnection(t)
	wireLimits, err := limitsToWire(connection.Limits())
	if err != nil {
		t.Fatal(err)
	}
	failure := &commonv1.Status{
		Code: commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE, Message: "tail unavailable", Retryable: true,
	}
	snapshot := &enginev1.InteractionSnapshot{}
	control := &enginev1.InteractionStreamControl{Status: commonv1.OKStatus(), Tailing: true}
	validator, err := enginev1.NewInteractionTailValidator(snapshot, control, wireLimits)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		validator *enginev1.InteractionTailValidator
		frame     *enginev1.StreamInteractionsResponse
		code      client.ErrorCode
	}{
		{
			name: "missing validator", frame: interactionDeltaResponse(&enginev1.InteractionDelta{
				Revision: 1, Kind: enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_OPENED,
				Interaction: wirePending("run", "prompt"),
			}), code: client.ErrorInternal,
		},
		{
			name: "success control in tail", validator: validator,
			frame: interactionControlResponse(&enginev1.InteractionStreamControl{Status: commonv1.OKStatus()}),
			code:  client.ErrorInternal,
		},
		{
			name: "application failure", validator: validator,
			frame: interactionControlResponse(&enginev1.InteractionStreamControl{Status: failure}),
			code:  client.ErrorUnavailable,
		},
		{
			name: "revision gap", validator: validator,
			frame: interactionDeltaResponse(&enginev1.InteractionDelta{
				Revision: 2, Kind: enginev1.InteractionDeltaKind_INTERACTION_DELTA_KIND_OPENED,
				Interaction: wirePending("run", "prompt"),
			}), code: client.ErrorInternal,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lifetime, _ := newStreamLifetime(nil)
			output := make(chan streamResult[client.InteractionFrame], 1)
			receiveInteractionTail(
				lifetime, newScriptedServerStream(t.Context(), test.frame), test.validator, wireLimits, output,
			)
			if result := <-output; errorCode(result.err) != test.code {
				t.Fatalf("tail error = %T %v, want %s", result.err, result.err, test.code)
			}
		})
	}
}

func TestInteractionPageRejectsFailureAndIncompletePage(t *testing.T) {
	t.Parallel()
	connection := unaryTestConnection(t)
	wireLimits, err := limitsToWire(connection.Limits())
	if err != nil {
		t.Fatal(err)
	}
	failure := &commonv1.Status{
		Code: commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE, Message: "stream unavailable", Retryable: true,
	}
	failureStream := newScriptedServerStream(t.Context(), interactionControlResponse(&enginev1.InteractionStreamControl{Status: failure}))
	_, _, err = receiveInteractionPage(
		failureStream, client.NewInteractionStreamOptions(false), connection.Limits(), wireLimits,
	)
	if errorCode(err) != client.ErrorUnavailable {
		t.Fatalf("failure control = %v", err)
	}
	incomplete := newScriptedServerStream(t.Context(), interactionSnapshotResponse(&enginev1.InteractionSnapshot{}))
	_, _, err = receiveInteractionPage(
		incomplete, client.NewInteractionStreamOptions(false), connection.Limits(), wireLimits,
	)
	if errorCode(err) != client.ErrorInternal {
		t.Fatalf("incomplete page = %v", err)
	}
}

func TestEventControlAndStreamInterruptionBoundaries(t *testing.T) {
	t.Parallel()
	legacy, err := publicEventControl(&enginev1.StreamControl{
		EarliestSequence: 1, LatestSequence: 1, LastDeliveredSequence: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, hasPage := legacy.PageLastSequence(); hasPage {
		t.Fatal("legacy event control unexpectedly has a page cursor")
	}
	connection := unaryTestConnection(t)
	run := mustRun(t, "run")
	cursor, err := client.NewCursor(run, 0)
	if err != nil {
		t.Fatal(err)
	}
	options, err := client.NewEventStreamOptions(1, false, connection.Limits())
	if err != nil {
		t.Fatal(err)
	}
	state := eventPageState{cursor: cursor, options: options, limits: connection.Limits()}
	pageLast := uint64(0)
	mismatchedPageLast := uint64(1)
	for _, value := range []*enginev1.StreamControl{
		nil,
		{Status: commonv1.OKStatus(), EarliestSequence: 1, LatestSequence: 1, LastDeliveredSequence: 1, PageLastSequence: &pageLast},
		{Status: commonv1.OKStatus(), EarliestSequence: 1, LatestSequence: 1, PageLastSequence: &mismatchedPageLast},
		{Status: commonv1.OKStatus(), EarliestSequence: 1, LatestSequence: 1, PageLastSequence: &pageLast, Tailing: true},
	} {
		if err = state.acceptControl(value); err == nil {
			t.Fatalf("invalid control was accepted: %#v", value)
		}
	}

	eventLifetime, _ := newStreamLifetime(nil)
	eventLifetime.interrupt(status.Error(codes.Unavailable, "lost"))
	events := &eventStream{lifetime: eventLifetime, frames: make(chan streamResult[client.EventFrame])}
	if _, err = events.Next(t.Context()); errorCode(err) != client.ErrorUnavailable {
		t.Fatalf("interrupted event stream = %v", err)
	}
	interactionLifetime, _ := newStreamLifetime(nil)
	interactionLifetime.interrupt(status.Error(codes.DeadlineExceeded, "late"))
	interactions := &interactionStream{
		lifetime: interactionLifetime, frames: make(chan streamResult[client.InteractionFrame]),
	}
	if _, err = interactions.Next(t.Context()); errorCode(err) != client.ErrorDeadlineExceeded {
		t.Fatalf("interrupted interaction stream = %v", err)
	}
	closedLifetime, _ := newStreamLifetime(nil)
	closedLifetime.close()
	if sendStreamResult(closedLifetime, make(chan streamResult[int]), streamResult[int]{value: 1}) {
		t.Fatal("result was sent after stream cancellation")
	}
}

type scriptedServerStream[T any] struct {
	ctx    context.Context //nolint:containedctx // immutable fixture stream lifetime.
	values []*T
	next   int
}

func newScriptedServerStream[T any](ctx context.Context, values ...*T) *scriptedServerStream[T] {
	return &scriptedServerStream[T]{ctx: ctx, values: values}
}

func (stream *scriptedServerStream[T]) Recv() (*T, error) {
	if stream.next == len(stream.values) {
		return nil, io.EOF
	}
	value := stream.values[stream.next]
	stream.next++
	return value, nil
}

func (*scriptedServerStream[T]) Header() (metadata.MD, error) { return metadata.MD{}, nil }
func (*scriptedServerStream[T]) Trailer() metadata.MD         { return nil }
func (*scriptedServerStream[T]) CloseSend() error             { return nil }
func (stream *scriptedServerStream[T]) Context() context.Context {
	return stream.ctx
}
func (*scriptedServerStream[T]) SendMsg(any) error { return nil }
func (*scriptedServerStream[T]) RecvMsg(any) error { return io.EOF }

func wireEvent(run string, sequence uint64, kind enginev1.EventKind, payload string) *enginev1.RunEvent {
	return &enginev1.RunEvent{
		RunId: run, Sequence: sequence, UnixNano: time.Now().UnixNano(), Kind: kind,
		PayloadJson: []byte(payload),
	}
}

func eventResponse(value *enginev1.RunEvent) *enginev1.StreamEventsResponse {
	return &enginev1.StreamEventsResponse{Payload: &enginev1.StreamEventsResponse_Event{Event: value}}
}

func eventControlResponse(value *enginev1.StreamControl) *enginev1.StreamEventsResponse {
	return &enginev1.StreamEventsResponse{Payload: &enginev1.StreamEventsResponse_Control{Control: value}}
}

func wirePending(run, id string) *enginev1.PendingInteraction {
	return &enginev1.PendingInteraction{
		RunId: run, InteractionId: id, Kind: "confirmation", Prompt: "continue?",
		SchemaJson: []byte(`{"type":"boolean"}`),
	}
}

func interactionSnapshotResponse(value *enginev1.InteractionSnapshot) *enginev1.StreamInteractionsResponse {
	return &enginev1.StreamInteractionsResponse{Payload: &enginev1.StreamInteractionsResponse_Snapshot{Snapshot: value}}
}

func interactionControlResponse(value *enginev1.InteractionStreamControl) *enginev1.StreamInteractionsResponse {
	return &enginev1.StreamInteractionsResponse{Payload: &enginev1.StreamInteractionsResponse_Control{Control: value}}
}

func interactionDeltaResponse(value *enginev1.InteractionDelta) *enginev1.StreamInteractionsResponse {
	return &enginev1.StreamInteractionsResponse{Payload: &enginev1.StreamInteractionsResponse_Delta{Delta: value}}
}

func errorCode(err error) client.ErrorCode {
	value, ok := errors.AsType[*client.StatusError](err)
	if !ok {
		return ""
	}
	return value.Code()
}

package grpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"github.com/spice-framework/spice-agent/interaction"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestServeInteractionObservationFiniteAndTailingContracts(t *testing.T) {
	t.Parallel()
	limits := interactionStreamLimits(t)
	pending := interactionPending(t, "run-1", "approval-1", "Continue?")

	t.Run("finite", func(t *testing.T) {
		t.Parallel()
		observation := newInteractionFixtureObservation(
			context.Background(), daemon.PendingSnapshot{Revision: 4, Pending: []daemon.Pending{pending}}, false,
		)
		stream := newInteractionFixtureStream(t.Context())
		if err := serveOwnedInteractionObservation(stream, observation, false, limits); err != nil {
			t.Fatal(err)
		}
		frames := stream.frames()
		if len(frames) != 2 || enginev1.ValidateInteractionStreamPage(frames, false, limits) != nil ||
			!observation.closed.Load() {
			t.Fatalf("finite frames = %#v, closed %t", frames, observation.closed.Load())
		}
	})

	t.Run("tail", func(t *testing.T) {
		t.Parallel()
		observation := newInteractionFixtureObservation(
			context.Background(), daemon.PendingSnapshot{}, true,
		)
		observation.deltas <- daemon.Delta{Revision: 1, Kind: daemon.DeltaOpened, Pending: pending}
		observation.deltas <- daemon.Delta{Revision: 2, Kind: daemon.DeltaClosed, Pending: pending}
		close(observation.deltas)
		stream := newInteractionFixtureStream(t.Context())
		if err := serveOwnedInteractionObservation(stream, observation, true, limits); err != nil {
			t.Fatal(err)
		}
		frames := stream.frames()
		if len(frames) != 4 || enginev1.ValidateInteractionStreamPage(frames[:2], true, limits) != nil {
			t.Fatalf("tail initial frames = %#v", frames)
		}
		validator, err := enginev1.NewInteractionTailValidator(
			frames[0].GetSnapshot(), frames[1].GetControl(), limits,
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, frame := range frames[2:] {
			if err = validator.Accept(frame); err != nil {
				t.Fatal(err)
			}
		}
		if validator.Revision() != 2 || !observation.closed.Load() {
			t.Fatalf("tail revision = %d, closed %t", validator.Revision(), observation.closed.Load())
		}
	})
}

func TestServeInteractionObservationRejectsCompleteWrapperBeforeFirstDataFrame(t *testing.T) {
	t.Parallel()
	pending := interactionPending(t, "run-1", "approval-1", strings.Repeat("x", 180))
	wide := interactionStreamLimits(t)
	snapshot, err := interactionSnapshotToWire(
		daemon.PendingSnapshot{Revision: 1, Pending: []daemon.Pending{pending}}, wide,
	)
	if err != nil {
		t.Fatal(err)
	}
	innerBytes := proto.Size(snapshot)
	wrapper := &enginev1.StreamInteractionsResponse{
		Payload: &enginev1.StreamInteractionsResponse_Snapshot{Snapshot: snapshot},
	}
	if proto.Size(wrapper) <= innerBytes {
		t.Fatal("test snapshot wrapper has no framing overhead")
	}
	limits := proto.CloneOf(wide)
	limits.MaxMessageBytes = uint64(innerBytes)
	observation := newInteractionFixtureObservation(
		context.Background(), daemon.PendingSnapshot{Revision: 1, Pending: []daemon.Pending{pending}}, false,
	)
	stream := newInteractionFixtureStream(t.Context())
	if err = serveOwnedInteractionObservation(stream, observation, false, limits); err != nil {
		t.Fatal(err)
	}
	frames := stream.frames()
	if len(frames) != 1 || frames[0].GetControl().GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INTERNAL ||
		frames[0].GetSnapshot() != nil {
		t.Fatalf("wrapper overflow frames = %#v", frames)
	}
}

func TestEngineServiceStreamInteractionsRejectsMinorOneWrongAndStaleSessions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		prepare  func(*enginev1.InitializeRequest)
		mutate   func(*enginev1.StreamInteractionsRequest)
		expected commonv1.ErrorCode
	}{
		{
			name: "minor one",
			prepare: func(request *enginev1.InitializeRequest) {
				request.Protocol.Maximum.Minor = 1
			},
			expected: commonv1.ErrorCode_ERROR_CODE_INCOMPATIBLE_VERSION,
		},
		{
			name: "wrong client",
			mutate: func(request *enginev1.StreamInteractionsRequest) {
				request.ClientId = "0123456789abcdef0123456789abcdef"
			},
			expected: commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE,
		},
		{
			name: "stale epoch",
			mutate: func(request *enginev1.StreamInteractionsRequest) {
				request.OwnershipEpoch++
			},
			expected: commonv1.ErrorCode_ERROR_CODE_STALE_CLIENT,
		},
		{
			name:     "host unavailable",
			expected: commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, request, ctx := initializedInteractionService(t, test.prepare)
			streamRequest := &enginev1.StreamInteractionsRequest{
				ClientId: request.GetClientId(), OwnershipEpoch: request.GetOwnershipEpoch(),
			}
			if test.mutate != nil {
				test.mutate(streamRequest)
			}
			stream := newInteractionFixtureStream(ctx)
			if err := service.StreamInteractions(streamRequest, stream); err != nil {
				t.Fatal(err)
			}
			frames := stream.frames()
			if len(frames) != 1 || frames[0].GetControl().GetStatus().GetCode() != test.expected {
				t.Fatalf("failure frames = %#v, want %s", frames, test.expected)
			}
			if test.expected == commonv1.ErrorCode_ERROR_CODE_INCOMPATIBLE_VERSION {
				mismatch := frames[0].GetControl().GetStatus().GetVersionMismatch()
				if mismatch.GetClient().GetMinimum().GetMinor() != 1 ||
					mismatch.GetClient().GetMaximum().GetMinor() != 1 ||
					mismatch.GetServer().GetMinimum().GetMinor() != 2 ||
					mismatch.GetServer().GetMaximum().GetMinor() != 2 {
					t.Fatalf("minor cut mismatch = %#v", mismatch)
				}
			}
		})
	}
}

func TestInteractionStreamSendFailureIsStableTransportUnavailable(t *testing.T) {
	t.Parallel()
	stream := newInteractionFixtureStream(t.Context())
	stream.sendErr = errors.New("sensitive transport implementation detail")
	observation := newInteractionFixtureObservation(context.Background(), daemon.PendingSnapshot{}, false)
	err := serveOwnedInteractionObservation(stream, observation, false, interactionStreamLimits(t))
	if status.Code(err) != codes.Unavailable || status.Convert(err).Message() != "interaction stream transport failed" ||
		!observation.closed.Load() {
		t.Fatalf("send failure = %v, closed %t", err, observation.closed.Load())
	}
}

func TestInteractionInitialPageFenceDistinguishesDisclosureState(t *testing.T) {
	t.Parallel()
	limits := interactionStreamLimits(t)

	t.Run("before snapshot is quiet", func(t *testing.T) {
		t.Parallel()
		observationContext, cancelObservation := context.WithCancel(context.Background())
		cancelObservation()
		observation := newInteractionFixtureObservation(
			observationContext, daemon.PendingSnapshot{}, false,
		)
		stream := newInteractionFixtureStream(t.Context())
		if err := serveOwnedInteractionObservation(stream, observation, false, limits); err != nil {
			t.Fatal(err)
		}
		if frames := stream.frames(); len(frames) != 0 || !observation.closed.Load() {
			t.Fatalf("pre-disclosure fence frames = %#v, closed %t", frames, observation.closed.Load())
		}
	})

	t.Run("after snapshot is unavailable", func(t *testing.T) {
		t.Parallel()
		observationContext, cancelObservation := context.WithCancel(context.Background())
		observation := newInteractionFixtureObservation(
			observationContext, daemon.PendingSnapshot{}, false,
		)
		stream := newInteractionFixtureStream(t.Context())
		stream.afterSend = func(sent int) {
			if sent == 1 {
				cancelObservation()
			}
		}
		err := serveOwnedInteractionObservation(stream, observation, false, limits)
		if status.Code(err) != codes.Unavailable ||
			status.Convert(err).Message() != "interaction stream initial page was interrupted" {
			t.Fatalf("partial initial page = %v", err)
		}
		frames := stream.frames()
		if len(frames) != 1 || frames[0].GetSnapshot() == nil || !observation.closed.Load() {
			t.Fatalf("partial initial frames = %#v, closed %t", frames, observation.closed.Load())
		}
	})
}

func TestInteractionStreamFailureFrameFailsClosedWhenNoControlFits(t *testing.T) {
	t.Parallel()
	limits := interactionStreamLimits(t)
	limits.MaxMessageBytes = 1
	stream := newInteractionFixtureStream(t.Context())
	if err := sendInteractionFailure(
		stream, invalidLifecycleRequest("deliberately oversized application failure"), limits,
	); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("unrepresentable failure = %v", err)
	}
	if frames := stream.frames(); len(frames) != 0 {
		t.Fatalf("unrepresentable failure emitted frames %#v", frames)
	}
}

func TestInteractionObservationAcquisitionFencingClosesQuietly(t *testing.T) {
	t.Parallel()
	service, initialized, ctx := initializedInteractionService(t, nil)
	host, ok := service.host.(*grpcFixtureHost)
	if !ok {
		t.Fatal("fixture service host type changed")
	}
	host.interactionErr = context.Canceled
	stream := newInteractionFixtureStream(ctx)
	err := service.StreamInteractions(&enginev1.StreamInteractionsRequest{
		ClientId: initialized.GetClientId(), OwnershipEpoch: initialized.GetOwnershipEpoch(),
	}, stream)
	if err != nil || len(stream.frames()) != 0 {
		t.Fatalf("fenced acquisition = %v, frames %#v", err, stream.frames())
	}
}

func TestInteractionObservationAcquisitionClassifiesRootAndRPCcancellation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		cancelRPC bool
		wantCode  codes.Code
	}{
		{name: "adapter root", wantCode: codes.OK},
		{name: "RPC", cancelRPC: true, wantCode: codes.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, initialized, authenticated := initializedInteractionService(t, nil)
			base, ok := service.host.(*grpcFixtureHost)
			if !ok {
				t.Fatal("fixture service host type changed")
			}
			blocking := &blockingInteractionHost{
				grpcFixtureHost: base, entered: make(chan struct{}),
			}
			service.host = blocking
			root, stopRoot := context.WithCancel(context.Background())
			defer stopRoot()
			service.root = root
			rpcContext, cancelRPC := context.WithCancel(authenticated)
			defer cancelRPC()
			stream := newInteractionFixtureStream(rpcContext)
			done := make(chan error, 1)
			go func() {
				done <- service.StreamInteractions(&enginev1.StreamInteractionsRequest{
					ClientId: initialized.GetClientId(), OwnershipEpoch: initialized.GetOwnershipEpoch(),
				}, stream)
			}()
			<-blocking.entered
			if test.cancelRPC {
				cancelRPC()
			} else {
				stopRoot()
			}
			err := <-done
			if status.Code(err) != test.wantCode || len(stream.frames()) != 0 {
				t.Fatalf("acquisition cancellation = %v, frames %#v", err, stream.frames())
			}
		})
	}
}

func TestEngineServiceStreamInteractionsRequiresAuthentication(t *testing.T) {
	t.Parallel()
	stream := newInteractionFixtureStream(context.Background())
	err := (&engineService{}).StreamInteractions(&enginev1.StreamInteractionsRequest{}, stream)
	if status.Code(err) != codes.Unauthenticated || len(stream.frames()) != 0 {
		t.Fatalf("unauthenticated interaction stream = %v, frames %#v", err, stream.frames())
	}
}

func TestServeInteractionObservationRetainsReconnectFenceUntilSenderExit(t *testing.T) {
	t.Parallel()
	root, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sessions, err := daemon.NewSessionStore(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Shutdown(context.Background()) })
	session, err := sessions.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := sessions.AcquireStream(t.Context(), session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	observation := newInteractionFixtureObservation(lease.Context(), daemon.PendingSnapshot{}, true)
	observation.closeFn = lease.Close
	stream := newInteractionFixtureStream(t.Context())
	served := make(chan error, 1)
	go func() {
		served <- serveOwnedInteractionObservation(stream, observation, true, interactionStreamLimits(t))
	}()
	stream.waitForFrames(t, 2)

	reconnected := make(chan daemon.Session, 1)
	reconnectErr := make(chan error, 1)
	go func() {
		next, reconnectFailure := sessions.ReconnectContext(
			context.Background(), session.ClientID(), session.Epoch(),
		)
		reconnected <- next
		reconnectErr <- reconnectFailure
	}()
	select {
	case <-lease.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("reconnect did not fence the RPC observation")
	}
	if err = <-served; err != nil {
		t.Fatal(err)
	}
	if !observation.closed.Load() {
		t.Fatal("RPC sender exited without closing its observation")
	}
	next := <-reconnected
	if err = <-reconnectErr; err != nil || next.Epoch() != session.Epoch()+1 {
		t.Fatalf("reconnect = %#v, %v", next, err)
	}
	if len(stream.frames()) != 2 {
		t.Fatalf("old epoch emitted frames after fencing: %#v", stream.frames())
	}
}

func TestInteractionStreamFlowControlRequiresRPCDrainBeforeReconnectAdvance(t *testing.T) {
	t.Parallel()
	root, cancelRoot := context.WithCancel(context.Background())
	t.Cleanup(cancelRoot)
	sessions, err := daemon.NewSessionStore(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Shutdown(context.Background()) })
	session, err := sessions.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := sessions.AcquireStream(t.Context(), session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	observation := newInteractionFixtureObservation(lease.Context(), daemon.PendingSnapshot{}, false)
	observation.closeFn = lease.Close
	rpcContext, cancelRPC := context.WithCancel(t.Context())
	stream := newBlockingInteractionStream(rpcContext)
	limits := interactionStreamLimits(t)
	served := make(chan error, 1)
	go func() {
		served <- serveOwnedInteractionObservation(
			stream, observation, false, limits,
		)
	}()
	select {
	case <-stream.entered:
	case <-time.After(time.Second):
		t.Fatal("interaction sender did not enter blocked transport send")
	}

	bounded, cancelReconnect := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err = sessions.ReconnectContext(bounded, session.ClientID(), session.Epoch())
	cancelReconnect()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked reconnect = %v", err)
	}
	if err = sessions.Check(session.ClientID(), session.Epoch()); err != nil {
		t.Fatalf("timed-out reconnect advanced ownership: %v", err)
	}
	if observation.closed.Load() {
		t.Fatal("blocked sender released its stream lease before RPC cancellation")
	}

	cancelRPC()
	select {
	case err = <-served:
		if status.Code(err) != codes.Canceled {
			t.Fatalf("canceled blocked sender = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RPC cancellation did not release the blocked interaction sender")
	}
	if !observation.closed.Load() {
		t.Fatal("canceled RPC did not close its interaction observation")
	}

	retry, cancelRetry := context.WithTimeout(context.Background(), time.Second)
	next, err := sessions.ReconnectContext(retry, session.ClientID(), session.Epoch())
	cancelRetry()
	if err != nil || next.ClientID() != session.ClientID() || next.Epoch() != session.Epoch()+1 {
		t.Fatalf("reconnect retry = %#v, %v", next, err)
	}
	if err = sessions.Check(session.ClientID(), session.Epoch()); !errors.Is(err, daemon.ErrStaleSession) {
		t.Fatalf("old ownership after reconnect retry = %v", err)
	}
	if err = sessions.Check(next.ClientID(), next.Epoch()); err != nil {
		t.Fatalf("new ownership after reconnect retry = %v", err)
	}
}

type interactionFixtureObservation struct {
	snapshot daemon.PendingSnapshot
	deltas   chan daemon.Delta
	tailing  bool
	ctx      context.Context //nolint:containedctx // immutable test-owned observation lifetime.
	waitErr  error
	closeFn  func()
	closed   atomic.Bool
}

func newInteractionFixtureObservation(
	ctx context.Context,
	snapshot daemon.PendingSnapshot,
	tailing bool,
) *interactionFixtureObservation {
	return &interactionFixtureObservation{
		snapshot: snapshot, deltas: make(chan daemon.Delta, 8), tailing: tailing, ctx: ctx,
	}
}

func (observation *interactionFixtureObservation) Snapshot() daemon.PendingSnapshot {
	return observation.snapshot
}

func (observation *interactionFixtureObservation) Deltas() <-chan daemon.Delta {
	return observation.deltas
}

func (observation *interactionFixtureObservation) Tailing() bool { return observation.tailing }

func (observation *interactionFixtureObservation) Context() context.Context { return observation.ctx }

func (observation *interactionFixtureObservation) Wait(context.Context) error {
	return observation.waitErr
}

func (observation *interactionFixtureObservation) Close() {
	if observation.closed.CompareAndSwap(false, true) && observation.closeFn != nil {
		observation.closeFn()
	}
}

type interactionFixtureStream struct {
	grpc.ServerStream
	ctx context.Context //nolint:containedctx // immutable test-owned RPC lifetime.

	mu        sync.Mutex
	sent      []*enginev1.StreamInteractionsResponse
	notify    chan struct{}
	sendErr   error
	afterSend func(int)
}

type blockingInteractionStream struct {
	grpc.ServerStream
	ctx     context.Context //nolint:containedctx // immutable test-owned RPC lifetime.
	entered chan struct{}
	once    sync.Once
}

func newBlockingInteractionStream(ctx context.Context) *blockingInteractionStream {
	return &blockingInteractionStream{ctx: ctx, entered: make(chan struct{})}
}

func (stream *blockingInteractionStream) Context() context.Context { return stream.ctx }

func (stream *blockingInteractionStream) Send(*enginev1.StreamInteractionsResponse) error {
	stream.once.Do(func() { close(stream.entered) })
	<-stream.ctx.Done()
	return stream.ctx.Err()
}

func newInteractionFixtureStream(ctx context.Context) *interactionFixtureStream {
	return &interactionFixtureStream{ctx: ctx, notify: make(chan struct{}, 16)}
}

func (stream *interactionFixtureStream) Context() context.Context { return stream.ctx }

func (stream *interactionFixtureStream) Send(value *enginev1.StreamInteractionsResponse) error {
	if stream.sendErr != nil {
		return stream.sendErr
	}
	if err := stream.ctx.Err(); err != nil {
		return err
	}
	stream.mu.Lock()
	stream.sent = append(stream.sent, proto.CloneOf(value))
	sent := len(stream.sent)
	stream.mu.Unlock()
	if stream.afterSend != nil {
		stream.afterSend(sent)
	}
	stream.notify <- struct{}{}
	return nil
}

func (stream *interactionFixtureStream) frames() []*enginev1.StreamInteractionsResponse {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	result := make([]*enginev1.StreamInteractionsResponse, len(stream.sent))
	for index, frame := range stream.sent {
		result[index] = proto.CloneOf(frame)
	}
	return result
}

func (stream *interactionFixtureStream) waitForFrames(t *testing.T, count int) {
	t.Helper()
	for len(stream.frames()) < count {
		select {
		case <-stream.notify:
		case <-time.After(time.Second):
			t.Fatalf("stream delivered %d frames, want %d", len(stream.frames()), count)
		}
	}
}

func interactionPending(t *testing.T, runID, interactionID, prompt string) daemon.Pending {
	t.Helper()
	scope, err := interaction.NewScope(runID)
	if err != nil {
		t.Fatal(err)
	}
	request, err := interaction.NewRequest(
		interaction.ID(interactionID), "approval", prompt, json.RawMessage(`{"type":"boolean"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	return daemon.Pending{Scope: scope, Request: request}
}

func interactionStreamLimits(t *testing.T) *commonv1.Limits {
	t.Helper()
	_, limits, _, _ := wireFixtureValues(t)
	result, err := limitsToWire(limits)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func initializedInteractionService(
	t *testing.T,
	prepare func(*enginev1.InitializeRequest),
) (*engineService, *enginev1.InitializeResponse, context.Context) {
	t.Helper()
	root, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	build, limits, health, definitions := wireFixtureValues(t)
	description, err := daemon.NewRunHostDescription(definitions, health)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := daemon.NewSessionStore(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Shutdown(context.Background()) })
	registry, err := newNegotiatedSessionRegistry(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	wireBuild, err := buildToWire(build)
	if err != nil {
		t.Fatal(err)
	}
	wireLimits, err := limitsToWire(limits)
	if err != nil {
		t.Fatal(err)
	}
	service := &engineService{
		root: root, host: &grpcFixtureHost{description: description, health: health},
		sessions: sessions, registry: registry, build: wireBuild,
		capabilities: &commonv1.CapabilitySet{Names: []string{"events"}}, limits: wireLimits,
	}
	ctx := context.WithValue(t.Context(), transportAuthenticationKey{}, true)
	initialize := grpcInitializeRequest(limits)
	if prepare != nil {
		prepare(initialize)
	}
	response, err := service.Initialize(ctx, initialize)
	if err != nil || response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_OK {
		t.Fatalf("initialize = %#v, %v", response, err)
	}
	return service, response, ctx
}

var _ interactionObservation = (*interactionFixtureObservation)(nil)

type blockingInteractionHost struct {
	*grpcFixtureHost
	entered chan struct{}
}

func (host *blockingInteractionHost) SnapshotInteractions(
	ctx context.Context,
	_ daemon.Session,
) (interactionObservation, error) {
	close(host.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

var _ runHostBoundary = (*blockingInteractionHost)(nil)

func TestInteractionObservationFailureIsApplicationControl(t *testing.T) {
	t.Parallel()
	observation := newInteractionFixtureObservation(context.Background(), daemon.PendingSnapshot{}, true)
	observation.waitErr = errors.New("fixture observer failure")
	close(observation.deltas)
	stream := newInteractionFixtureStream(t.Context())
	if err := serveOwnedInteractionObservation(
		stream, observation, true, interactionStreamLimits(t),
	); err != nil {
		t.Fatal(err)
	}
	frames := stream.frames()
	if len(frames) != 3 || frames[2].GetControl().GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INTERNAL {
		t.Fatalf("observer failure frames = %#v", frames)
	}
}

func TestInteractionObserverExhaustionPreservesExactOverloadFacts(t *testing.T) {
	t.Parallel()
	limits := daemon.DefaultPendingLimits()
	limits.ObserverQueueBytes = 1
	limits.ReservedQueueBytes = limits.Observers
	limits.ReservedQueueBytesPerClient = limits.ObserversPerClient
	limits.QueuedBytes = limits.Observers
	limits.QueuedBytesPerClient = limits.ObserversPerClient
	hub, err := daemon.NewPendingHub(limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hub.Close)
	scope, err := interaction.NewScope("run-overload")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := hub.BindRun("client-overload", scope)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(binding.Release)
	subscription, err := hub.Subscribe(t.Context(), "client-overload")
	if err != nil {
		t.Fatal(err)
	}
	request, err := interaction.NewRequest(
		interaction.ID("approval-overload"), "approval", "Continue?", json.RawMessage(`{"type":"boolean"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	requestContext, cancelRequest := context.WithCancel(t.Context())
	requestDone := make(chan error, 1)
	go func() {
		_, requestErr := hub.Request(requestContext, scope, request)
		requestDone <- requestErr
	}()
	waitContext, cancelWait := context.WithTimeout(t.Context(), time.Second)
	defer cancelWait()
	err = subscription.Wait(waitContext)
	var exhausted *daemon.ObserverExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("observer exhaustion = %v", err)
	}
	statusValue := interactionStreamFailureStatus(err)
	if commonv1.ValidateStatus(statusValue) != nil ||
		statusValue.GetCode() != commonv1.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED ||
		statusValue.GetOverload().GetResource() != exhausted.Resource() ||
		statusValue.GetOverload().GetLimit() != exhausted.Limit() ||
		statusValue.GetOverload().GetObserved() != exhausted.Observed() {
		t.Fatalf("observer overload = %#v for %v", statusValue, exhausted)
	}
	cancelRequest()
	if requestErr := <-requestDone; !errors.Is(requestErr, context.Canceled) {
		t.Fatalf("pending request cleanup = %v", requestErr)
	}
}

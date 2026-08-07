package localclient

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
	"github.com/spice-framework/spice-agent/client/managed"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon/endpoint"
	"github.com/spice-framework/spice-agent/daemon/localipc"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestConnectorUsesExactLocalIPCAndDoesNotRetryInitialize(t *testing.T) {
	metadata := currentMetadataFixture(t)
	service := &localEngineFixture{metadata: metadata}
	stop := serveLocalEngineFixture(t, metadata.Address(), service)
	defer stop()

	connector, err := New(metadata)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connector.Close() })
	session, err := connector.Initialize(t.Context(), initializeRequestFixture(t, metadata))
	if err != nil {
		t.Fatal(err)
	}
	if session.Connection().Server().Component() != metadata.Server().Component() {
		t.Fatal("session did not preserve the published server identity")
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	if service.calls.Load() != 1 {
		t.Fatalf("successful Initialize calls = %d, want 1", service.calls.Load())
	}

	stop()
	failingMetadata := currentMetadataFixture(t)
	failingService := &localEngineFixture{metadata: failingMetadata, unavailable: true}
	stopFailing := serveLocalEngineFixture(t, failingMetadata.Address(), failingService)
	defer stopFailing()
	failingConnector, err := New(failingMetadata)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = failingConnector.Close() })
	if _, err = failingConnector.Initialize(
		t.Context(), initializeRequestFixture(t, failingMetadata),
	); err == nil {
		t.Fatal("unavailable Initialize succeeded")
	}
	if failingService.calls.Load() != 1 {
		t.Fatalf("unavailable Initialize calls = %d, want exactly 1", failingService.calls.Load())
	}
}

func TestManagedReconnectReusesLocalAdapterAndClosesOldStreamOverIPC(t *testing.T) {
	metadata := currentMetadataFixture(t)
	oldNext := make(chan error, 1)
	observedOldNext := make(chan error, 1)
	service := &localEngineFixture{
		metadata: metadata, streamStarted: make(chan struct{}), priorNext: oldNext,
		observedPriorNext: observedOldNext, reconnectReached: make(chan struct{}),
		releaseReconnect: make(chan struct{}),
	}
	stop := serveLocalEngineFixture(t, metadata.Address(), service)
	t.Cleanup(stop)
	discovery, err := newDiscovery(fixtureEndpointDiscovery{metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = discovery.Close() })
	managedConnector, err := managed.New(managed.Config{
		Discovery: discovery, Starter: forbiddenManagedStarter{}, StartupLock: forbiddenManagedStartupLock{},
		StartupTimeout: time.Second, RetryInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = managedConnector.Close() })

	oldSession, err := managedConnector.Initialize(t.Context(), initializeRequestFixture(t, metadata))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = oldSession.Close() })
	stream, err := oldSession.Interactions(t.Context(), client.NewInteractionStreamOptions(true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	for range 2 {
		if _, err = stream.Next(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-service.streamStarted:
	case <-time.After(time.Second):
		t.Fatal("local interaction stream did not start")
	}
	oldNextContext, cancelOldNext := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelOldNext()
	go func() {
		_, nextErr := stream.Next(oldNextContext)
		oldNext <- nextErr
	}()
	claim, err := client.NewReconnectClaim(
		oldSession.Connection().ClientID(), oldSession.Connection().OwnershipEpoch(),
	)
	if err != nil {
		t.Fatal(err)
	}
	reconnectRequest := reconnectRequestFixture(t, metadata, claim)
	reconnected := make(chan struct {
		session client.Session
		err     error
	}, 1)
	reconnectContext, cancelReconnect := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelReconnect()
	go func() {
		session, initializeErr := managedConnector.Initialize(reconnectContext, reconnectRequest)
		reconnected <- struct {
			session client.Session
			err     error
		}{session: session, err: initializeErr}
	}()
	select {
	case <-service.reconnectReached:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect RPC did not reach local service")
	}
	select {
	case nextErr := <-observedOldNext:
		if !errors.Is(nextErr, client.ErrClosed) {
			t.Fatalf("old local stream Next error = %v, want ErrClosed", nextErr)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnect did not close old local Next")
	}
	close(service.releaseReconnect)
	var result struct {
		session client.Session
		err     error
	}
	select {
	case result = <-reconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not complete")
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.session.Connection().OwnershipEpoch() != oldSession.Connection().OwnershipEpoch()+1 {
		t.Fatalf("reconnect epoch = %d", result.session.Connection().OwnershipEpoch())
	}
	if err = result.session.Close(); err != nil {
		t.Fatal(err)
	}
}

type localEngineFixture struct {
	enginev1.UnimplementedEngineServiceServer
	metadata             endpoint.Metadata
	unavailable          bool
	calls                atomic.Int32
	streamStarted        chan struct{}
	reconnectReached     chan struct{}
	releaseReconnect     chan struct{}
	priorNext            <-chan error
	observedPriorNext    chan<- error
	streamStartedOnce    sync.Once
	reconnectReachedOnce sync.Once
}

func (service *localEngineFixture) Initialize(
	ctx context.Context,
	request *enginev1.InitializeRequest,
) (*enginev1.InitializeResponse, error) {
	service.calls.Add(1)
	if service.unavailable {
		return nil, status.Error(codes.Unavailable, "fixture unavailable")
	}
	protocol := service.metadata.Protocol()
	build := service.metadata.Server()
	limits := request.GetRequestedLimits()
	negotiation, failure := enginev1.PreflightInitialize(
		request,
		&commonv1.ProtocolRange{
			Minimum: &commonv1.ProtocolVersion{Major: protocol.Major(), Minor: protocol.Minor(), Patch: protocol.Patch()},
			Maximum: &commonv1.ProtocolVersion{Major: protocol.Major(), Minor: protocol.Minor(), Patch: protocol.Patch()},
		},
		&commonv1.BuildIdentity{
			Component: build.Component(), Version: build.Version(), Commit: build.Commit(), GoVersion: build.GoVersion(),
		},
		&commonv1.CapabilitySet{},
		limits,
		&commonv1.Health{State: commonv1.HealthState_HEALTH_STATE_READY, Limits: limits},
		&enginev1.DefinitionSet{
			Revision: "catalog-1",
			Definitions: []*enginev1.Definition{{
				Id: "fixture", Revision: "revision-1", Model: "fixture-model", MaxTurns: 1,
			}},
		},
	)
	if failure != nil {
		return failure, nil
	}
	epoch := uint64(1)
	if claim := request.GetReconnectClaim(); claim != nil {
		epoch = claim.GetExpectedOwnershipEpoch() + 1
		if service.priorNext != nil {
			select {
			case nextErr := <-service.priorNext:
				if service.observedPriorNext != nil {
					service.observedPriorNext <- nextErr
				}
			case <-ctx.Done():
				return nil, status.Error(codes.Canceled, "prior stream did not close")
			}
		}
		if service.reconnectReached != nil {
			service.reconnectReachedOnce.Do(func() { close(service.reconnectReached) })
		}
		if service.releaseReconnect != nil {
			select {
			case <-service.releaseReconnect:
			case <-ctx.Done():
				return nil, status.Error(codes.Canceled, "reconnect fixture canceled")
			}
		}
	}
	return enginev1.CompleteInitialize(negotiation, "local-client-1", epoch), nil
}

func (service *localEngineFixture) StreamInteractions(
	_ *enginev1.StreamInteractionsRequest,
	stream grpc.ServerStreamingServer[enginev1.StreamInteractionsResponse],
) error {
	if err := stream.Send(&enginev1.StreamInteractionsResponse{
		Payload: &enginev1.StreamInteractionsResponse_Snapshot{Snapshot: &enginev1.InteractionSnapshot{}},
	}); err != nil {
		return err
	}
	if err := stream.Send(&enginev1.StreamInteractionsResponse{
		Payload: &enginev1.StreamInteractionsResponse_Control{Control: &enginev1.InteractionStreamControl{
			Status: commonv1.OKStatus(), Tailing: true,
		}},
	}); err != nil {
		return err
	}
	if service.streamStarted != nil {
		service.streamStartedOnce.Do(func() { close(service.streamStarted) })
	}
	<-stream.Context().Done()
	return context.Cause(stream.Context())
}

type forbiddenManagedStarter struct{}

func (forbiddenManagedStarter) Start(context.Context) (managed.Candidate, error) {
	return nil, errors.New("managed startup unexpectedly attempted")
}

type forbiddenManagedStartupLock struct{}

func (forbiddenManagedStartupLock) Acquire(context.Context) (managed.StartupLease, error) {
	return nil, errors.New("managed startup lock unexpectedly acquired")
}

func serveLocalEngineFixture(
	tb testing.TB,
	address string,
	service enginev1.EngineServiceServer,
) func() {
	tb.Helper()
	listener, err := localipc.Listen(address)
	if err != nil {
		tb.Fatal(err)
	}
	server := grpc.NewServer()
	enginev1.RegisterEngineServiceServer(server, service)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	var stopped atomic.Bool
	return func() {
		if !stopped.CompareAndSwap(false, true) {
			return
		}
		server.Stop()
		_ = listener.Close()
		if serveErr := <-done; serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			tb.Errorf("serve local gRPC fixture: %v", serveErr)
		}
	}
}

var _ client.Connector = (*Connector)(nil)

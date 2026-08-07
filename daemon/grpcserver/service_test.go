package grpcserver

import (
	"context"
	"errors"
	"math"
	"net"
	"sync/atomic"
	"testing"

	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"github.com/spice-framework/spice-agent/event"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestServerInitializeHealthAndReconnectOverAuthenticatedGRPC(t *testing.T) {
	t.Parallel()
	root, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	build, limits, health, definitions := wireFixtureValues(t)
	description, err := daemon.NewRunHostDescription(definitions, health)
	if err != nil {
		t.Fatal(err)
	}
	host := &grpcFixtureHost{description: description, health: health}
	sessions, err := daemon.NewSessionStore(root, 4)
	if err != nil {
		t.Fatal(err)
	}
	token := endpointTokenFixture(t, 9)
	server, err := newServer(serverDependencies{
		root: root, token: token, host: host, sessions: sessions,
		build: build, capabilities: []string{"tools", "events"}, maximumSessions: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	clientConnection := serveGRPCFixture(t, server)
	clientAPI := enginev1.NewEngineServiceClient(clientConnection)
	authorization, _ := token.AuthorizationValue()
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs(AuthenticationMetadataKey, authorization))

	request := grpcInitializeRequest(limits)
	initialized, err := clientAPI.Initialize(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err = enginev1.ValidateInitializeResponseForRequest(request, initialized); err != nil {
		t.Fatalf("initialize response: %v", err)
	}
	clientID, epoch := initialized.GetClientId(), initialized.GetOwnershipEpoch()
	if clientID == "" || epoch != 1 {
		t.Fatalf("fresh ownership = %q/%d", clientID, epoch)
	}
	initialized.Server.Component = "caller mutation"
	initialized.Definitions.Definitions[0].Model = "caller mutation"

	healthResponse, err := clientAPI.Health(ctx, &enginev1.HealthRequest{
		ClientId: clientID, OwnershipEpoch: epoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = enginev1.ValidateHealthResponse(healthResponse, initialized.GetLimits()); err != nil {
		// The caller intentionally mutated initialized.Server but not its limits.
		t.Fatalf("health response: %v", err)
	}
	if healthResponse.GetServer().GetComponent() != build.Component() || host.healthCalls.Load() != 1 {
		t.Fatalf("health used mutable response or skipped host: %#v, calls %d", healthResponse, host.healthCalls.Load())
	}
	invalidHealth, err := clientAPI.Health(ctx, &enginev1.HealthRequest{})
	if err != nil || invalidHealth.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT ||
		host.healthCalls.Load() != 1 {
		t.Fatalf("invalid health = %#v, %v, calls %d", invalidHealth, err, host.healthCalls.Load())
	}

	reconnect := grpcInitializeRequest(limits)
	reconnect.ReconnectClaim = &enginev1.ReconnectClaim{
		ClientId: clientID, ExpectedOwnershipEpoch: epoch,
	}
	reconnected, err := clientAPI.Initialize(ctx, reconnect)
	if err != nil {
		t.Fatal(err)
	}
	if err = enginev1.ValidateInitializeResponseForRequest(reconnect, reconnected); err != nil {
		t.Fatalf("reconnect response: %v", err)
	}
	if reconnected.GetClientId() != clientID || reconnected.GetOwnershipEpoch() != epoch+1 {
		t.Fatalf("reconnected ownership = %q/%d", reconnected.GetClientId(), reconnected.GetOwnershipEpoch())
	}
	staleReconnect, err := clientAPI.Initialize(ctx, reconnect)
	if err != nil || staleReconnect.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_STALE_CLIENT ||
		staleReconnect.GetStatus().GetStaleClient().GetExpectedEpoch() != epoch+1 ||
		staleReconnect.GetStatus().GetStaleClient().GetObservedEpoch() != epoch {
		t.Fatalf("stale reconnect response = %#v, %v", staleReconnect, err)
	}

	stale, err := clientAPI.Health(ctx, &enginev1.HealthRequest{ClientId: clientID, OwnershipEpoch: epoch})
	if err != nil {
		t.Fatal(err)
	}
	if stale.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_STALE_CLIENT ||
		stale.GetStatus().GetStaleClient().GetExpectedEpoch() != epoch+1 ||
		stale.GetStatus().GetStaleClient().GetObservedEpoch() != epoch {
		t.Fatalf("stale health response = %#v", stale)
	}
	unknown, err := clientAPI.Health(ctx, &enginev1.HealthRequest{
		ClientId: "0123456789abcdef0123456789abcdef", OwnershipEpoch: 1,
	})
	if err != nil || unknown.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE ||
		unknown.GetStatus().GetDetail() != nil {
		t.Fatalf("unknown health response = %#v, %v", unknown, err)
	}
}

func TestServerInitializationFailuresDoNotAllocateOrExposeState(t *testing.T) {
	t.Parallel()
	root, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	build, limits, health, definitions := wireFixtureValues(t)
	description, err := daemon.NewRunHostDescription(definitions, health)
	if err != nil {
		t.Fatal(err)
	}
	host := &grpcFixtureHost{description: description, health: health}
	sessions, err := daemon.NewSessionStore(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Shutdown(context.Background()) })
	token := endpointTokenFixture(t, 10)
	server, err := newServer(serverDependencies{
		root: root, token: token, host: host, sessions: sessions,
		build: build, capabilities: []string{"events"}, maximumSessions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	connection := serveGRPCFixture(t, server)
	api := enginev1.NewEngineServiceClient(connection)
	authorization, _ := token.AuthorizationValue()
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs(AuthenticationMetadataKey, authorization))

	invalid := grpcInitializeRequest(limits)
	invalid.RequiredCapabilities = &commonv1.CapabilitySet{Names: []string{"unavailable"}}
	response, err := api.Initialize(ctx, invalid)
	if err != nil || response.GetStatus().GetCode() == commonv1.ErrorCode_ERROR_CODE_OK ||
		response.GetClientId() != "" || response.GetDefinitions() != nil {
		t.Fatalf("invalid initialize = %#v, %v", response, err)
	}
	valid := grpcInitializeRequest(limits)
	initialized, err := api.Initialize(ctx, valid)
	if err != nil || initialized.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_OK {
		t.Fatalf("valid initialize after preflight failure = %#v, %v", initialized, err)
	}
	capacity, err := api.Initialize(ctx, valid)
	if err != nil || capacity.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED ||
		capacity.GetStatus().GetOverload().GetResource() != "negotiated sessions" ||
		capacity.GetStatus().GetOverload().GetLimit() != 1 ||
		capacity.GetStatus().GetOverload().GetObserved() != 2 || capacity.GetClientId() != "" {
		t.Fatalf("capacity initialize = %#v, %v", capacity, err)
	}
}

func TestServerReconnectWaiterCapacityReturnsOverloadFacts(t *testing.T) {
	t.Parallel()
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
	token := endpointTokenFixture(t, 18)
	server, err := newServer(serverDependencies{
		root: root, token: token, host: &grpcFixtureHost{description: description, health: health},
		sessions: sessions, build: build, capabilities: []string{"events"}, maximumSessions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	connection := serveGRPCFixture(t, server)
	api := enginev1.NewEngineServiceClient(connection)
	authorization, _ := token.AuthorizationValue()
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs(AuthenticationMetadataKey, authorization))
	initialized, err := api.Initialize(ctx, grpcInitializeRequest(limits))
	if err != nil || initialized.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_OK {
		t.Fatalf("fresh initialize = %#v, %v", initialized, err)
	}

	server.registry.mu.Lock()
	entry := server.registry.entries[initialized.GetClientId()]
	server.registry.mu.Unlock()
	<-entry.gate.token
	t.Cleanup(func() {
		select {
		case entry.gate.token <- struct{}{}:
		default:
		}
	})
	waitContext, cancelWaiters := context.WithCancel(ctx)
	waiterDone := make(chan error, maximumReconnectWaitersPerClient)
	reconnect := grpcInitializeRequest(limits)
	reconnect.ReconnectClaim = &enginev1.ReconnectClaim{
		ClientId: initialized.GetClientId(), ExpectedOwnershipEpoch: initialized.GetOwnershipEpoch(),
	}
	for range maximumReconnectWaitersPerClient {
		go func() {
			_, initializeErr := api.Initialize(waitContext, reconnect)
			waiterDone <- initializeErr
		}()
	}
	waitForReconnectWaiters(t, server.registry, initialized.GetClientId(), maximumReconnectWaitersPerClient)
	overflow, err := api.Initialize(ctx, reconnect)
	if err != nil || overflow.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED ||
		overflow.GetStatus().GetOverload().GetResource() != "reconnect initialization waiters" ||
		overflow.GetStatus().GetOverload().GetLimit() != maximumReconnectWaitersPerClient ||
		overflow.GetStatus().GetOverload().GetObserved() != maximumReconnectWaitersPerClient+1 {
		t.Fatalf("overflow reconnect = %#v, %v", overflow, err)
	}
	cancelWaiters()
	for range maximumReconnectWaitersPerClient {
		if waiterErr := <-waiterDone; status.Code(waiterErr) != codes.Canceled {
			t.Fatalf("canceled reconnect waiter = %v", waiterErr)
		}
	}
}

func TestEngineServiceFailsClosedWithoutAuthenticationMiddleware(t *testing.T) {
	t.Parallel()
	service := &engineService{}
	response, err := service.Initialize(context.Background(), &enginev1.InitializeRequest{})
	if response != nil || status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct unauthenticated initialize = %#v, %v", response, err)
	}
	health, err := service.Health(context.Background(), &enginev1.HealthRequest{})
	if health != nil || status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct unauthenticated health = %#v, %v", health, err)
	}
	start, err := service.StartRun(context.Background(), &enginev1.StartRunRequest{})
	if start != nil || status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct unauthenticated start = %#v, %v", start, err)
	}
	cancel, err := service.CancelRun(context.Background(), &enginev1.CancelRunRequest{})
	if cancel != nil || status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct unauthenticated cancel = %#v, %v", cancel, err)
	}
	respond, err := service.RespondInteraction(context.Background(), &enginev1.RespondInteractionRequest{})
	if respond != nil || status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct unauthenticated respond = %#v, %v", respond, err)
	}
	suspend, err := service.SuspendRun(context.Background(), &enginev1.SuspendRunRequest{})
	if suspend != nil || status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct unauthenticated suspend = %#v, %v", suspend, err)
	}
	resume, err := service.ResumeRun(context.Background(), &enginev1.ResumeRunRequest{})
	if resume != nil || status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct unauthenticated resume = %#v, %v", resume, err)
	}
	exported, err := service.ExportSnapshot(context.Background(), &enginev1.ExportSnapshotRequest{})
	if exported != nil || status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct unauthenticated export = %#v, %v", exported, err)
	}
	imported, err := service.ImportSnapshot(context.Background(), &enginev1.ImportSnapshotRequest{})
	if imported != nil || status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct unauthenticated import = %#v, %v", imported, err)
	}
}

func TestNewServerRejectsInvalidBoundaries(t *testing.T) {
	t.Parallel()
	if _, err := NewServer(ServerConfig{}); err == nil {
		t.Fatal("zero server config succeeded")
	}
	root := context.Background()
	build, _, health, definitions := wireFixtureValues(t)
	description, err := daemon.NewRunHostDescription(definitions, health)
	if err != nil {
		t.Fatal(err)
	}
	host := &grpcFixtureHost{description: description, health: health}
	sessions, err := daemon.NewSessionStore(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Shutdown(context.Background()) })
	valid := serverDependencies{
		root: root, token: endpointTokenFixture(t, 11), host: host, sessions: sessions,
		build: build, capabilities: []string{"events"}, maximumSessions: 1,
	}
	for name, mutate := range map[string]func(*serverDependencies){
		"nil root":       func(value *serverDependencies) { value.root = nil },
		"zero token":     func(value *serverDependencies) { value.token = EndpointToken{} },
		"zero build":     func(value *serverDependencies) { value.build = client.Build{} },
		"duplicate caps": func(value *serverDependencies) { value.capabilities = []string{"events", "events"} },
		"zero capacity":  func(value *serverDependencies) { value.maximumSessions = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			candidate := valid
			mutate(&candidate)
			if server, createErr := newServer(candidate); createErr == nil || server != nil {
				t.Fatalf("invalid server = %#v, %v", server, createErr)
			}
		})
	}

	hugeLimits, err := client.NewLimits(math.MaxUint64, 64, 128, 1<<20, 8, 16)
	if err != nil {
		t.Fatal(err)
	}
	hugeHealth, err := client.NewHealth(client.HealthReady, nil, 0, hugeLimits)
	if err != nil {
		t.Fatal(err)
	}
	hugeDescription, err := daemon.NewRunHostDescription(definitions, hugeHealth)
	if err != nil {
		t.Fatal(err)
	}
	huge := valid
	huge.host = &grpcFixtureHost{description: hugeDescription, health: hugeHealth}
	if server, createErr := newServer(huge); createErr == nil || server != nil {
		t.Fatalf("oversized platform limit server = %#v, %v", server, createErr)
	}
}

func TestServerLifecycleAndProtocolFailureSeparation(t *testing.T) {
	t.Parallel()
	root := context.Background()
	build, _, health, definitions := wireFixtureValues(t)
	description, err := daemon.NewRunHostDescription(definitions, health)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := daemon.NewSessionStore(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Shutdown(context.Background()) })
	server, err := newServer(serverDependencies{
		root: root, token: endpointTokenFixture(t, 12),
		host: &grpcFixtureHost{description: description, health: health}, sessions: sessions,
		build: build, capabilities: []string{"events"}, maximumSessions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Serve(nil); err == nil {
		t.Fatal("server accepted a nil listener")
	}
	var nilServer *Server
	if err = nilServer.Serve(nil); err == nil {
		t.Fatal("nil server accepted a listener")
	}
	nilServer.Stop()
	nilServer.GracefulStop()
	server.GracefulStop()
	if _, err = server.registry.lookup("client", 1); !errors.Is(err, errNegotiatedSessionClosed) {
		t.Fatalf("gracefully stopped registry lookup = %v", err)
	}

	internal := initializeInternalFailure("adapter invariant failed")
	if internal.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INTERNAL ||
		enginev1.ValidateInitializeResponse(internal) == nil {
		// A valid failure response intentionally validates as its typed StatusError.
		t.Fatalf("internal initialize failure = %#v", internal)
	}
	invalid := healthFailure(commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "health request is invalid")
	if invalid.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT ||
		commonv1.ValidateStatus(invalid.GetStatus()) != nil {
		t.Fatalf("health application failure = %#v", invalid)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if status.Code(contextTransportError(canceled, context.Canceled)) != codes.Canceled ||
		status.Code(contextTransportError(context.Background(), context.DeadlineExceeded)) != codes.DeadlineExceeded ||
		contextTransportError(context.Background(), errors.New("application failure")) != nil {
		t.Fatal("context failure was not separated from application failure")
	}
}

type grpcFixtureHost struct {
	description    daemon.RunHostDescription
	health         client.Health
	describeErr    error
	healthErr      error
	interactionErr error
	healthCalls    atomic.Int32
}

func (host *grpcFixtureHost) Describe(ctx context.Context) (daemon.RunHostDescription, error) {
	if host == nil {
		return daemon.RunHostDescription{}, errors.New("fixture host is nil")
	}
	if err := ctx.Err(); err != nil {
		return daemon.RunHostDescription{}, err
	}
	return host.description, host.describeErr
}

func (host *grpcFixtureHost) Health(ctx context.Context, session daemon.Session) (client.Health, error) {
	if host == nil {
		return client.Health{}, errors.New("fixture host is nil")
	}
	if err := ctx.Err(); err != nil {
		return client.Health{}, err
	}
	if session.Context() == nil || session.Context().Err() != nil {
		return client.Health{}, daemon.ErrStaleSession
	}
	host.healthCalls.Add(1)
	return host.health, host.healthErr
}

func (*grpcFixtureHost) Start(context.Context, daemon.Session, client.StartRequest) (client.StartResult, error) {
	return client.StartResult{}, daemon.ErrRunHostUnavailable
}

func (*grpcFixtureHost) Cancel(context.Context, daemon.Session, client.CancelRequest) (client.CancelResult, error) {
	return client.CancelResult{}, daemon.ErrRunHostUnavailable
}

func (*grpcFixtureHost) Respond(context.Context, daemon.Session, client.RespondRequest) (client.RespondResult, error) {
	return client.RespondResult{}, daemon.ErrRunHostUnavailable
}

func (*grpcFixtureHost) Suspend(context.Context, daemon.Session, client.RunMutation) (client.SuspendResult, error) {
	return client.SuspendResult{}, daemon.ErrRunHostUnavailable
}

func (*grpcFixtureHost) Resume(context.Context, daemon.Session, client.RunMutation) (client.ResumeResult, error) {
	return client.ResumeResult{}, daemon.ErrRunHostUnavailable
}

func (*grpcFixtureHost) Export(context.Context, daemon.Session, client.RunRef) (client.Snapshot, error) {
	return client.Snapshot{}, daemon.ErrRunHostUnavailable
}

func (*grpcFixtureHost) Import(context.Context, daemon.Session, client.ImportRequest) (client.ImportResult, error) {
	return client.ImportResult{}, daemon.ErrRunHostUnavailable
}

func (*grpcFixtureHost) ReplayEvents(
	context.Context,
	daemon.Session,
	client.RunRef,
	event.ReplayRequest,
) (ownedEventObservation, error) {
	return nil, daemon.ErrRunHostUnavailable
}

func (host *grpcFixtureHost) SnapshotInteractions(
	context.Context,
	daemon.Session,
) (interactionObservation, error) {
	if host.interactionErr != nil {
		return nil, host.interactionErr
	}
	return nil, daemon.ErrRunHostUnavailable
}

func (host *grpcFixtureHost) SubscribeInteractions(
	context.Context,
	daemon.Session,
) (interactionObservation, error) {
	if host.interactionErr != nil {
		return nil, host.interactionErr
	}
	return nil, daemon.ErrRunHostUnavailable
}

func grpcInitializeRequest(limits client.Limits) *enginev1.InitializeRequest {
	wireLimits, _ := limitsToWire(limits)
	return &enginev1.InitializeRequest{
		Protocol: commonv1.SupportedProtocolRange(),
		Client: &commonv1.BuildIdentity{
			Component: "test-client", Version: "0.1.0", Commit: "01234567", GoVersion: "go1.26.5",
		},
		SupportedCapabilities: &commonv1.CapabilitySet{Names: []string{"events"}},
		RequiredCapabilities:  &commonv1.CapabilitySet{Names: []string{"events"}},
		RequestedLimits:       wireLimits,
	}
}

func serveGRPCFixture(t *testing.T, server *Server) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	connection, err := grpc.NewClient(
		"passthrough:///spice-agent-server-fixture",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

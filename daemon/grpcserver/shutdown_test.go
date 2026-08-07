package grpcserver

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

func TestServiceStreamContextStopsIdleObservationWithAdapterRoot(t *testing.T) {
	t.Parallel()
	root, stopRoot := context.WithCancel(context.Background())
	service := &engineService{root: root}
	ctx, release := service.streamContext(t.Context())
	t.Cleanup(release)
	stopRoot()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("adapter root did not stop the stream observation context")
	}
}

func TestServerShutdownDeadlineForceStopsBlockedRPC(t *testing.T) {
	t.Parallel()
	root, cancelRoot := context.WithCancel(context.Background())
	grpcServer := grpc.NewServer()
	service := &shutdownFixtureService{entered: make(chan struct{}), done: make(chan struct{})}
	enginev1.RegisterEngineServiceServer(grpcServer, service)
	server := &Server{
		grpc: grpcServer, cancel: cancelRoot, shutdownDone: make(chan struct{}),
	}
	listener := bufconn.Listen(1 << 20)
	t.Cleanup(func() { _ = listener.Close() })
	go func() { _ = server.Serve(listener) }()

	connection, err := grpc.NewClient(
		"passthrough:///shutdown-fixture",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	stream, err := enginev1.NewEngineServiceClient(connection).StreamEvents(
		t.Context(), &enginev1.StreamEventsRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-service.entered:
	case <-time.After(time.Second):
		t.Fatal("blocking RPC did not start")
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelShutdown()
	if err = server.Shutdown(shutdownContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded shutdown = %v", err)
	}
	select {
	case <-service.done:
	case <-time.After(time.Second):
		t.Fatal("forced gRPC stop did not join the blocked RPC")
	}
	if root.Err() == nil {
		t.Fatal("shutdown did not stop adapter admission")
	}
	if _, err = stream.Recv(); err == nil {
		t.Fatal("forced shutdown left the client stream open")
	}
	if err = server.Shutdown(context.Background()); err != nil {
		t.Fatalf("idempotent shutdown = %v", err)
	}
	canceled, cancelCanceled := context.WithCancel(context.Background())
	cancelCanceled()
	if err = server.Shutdown(canceled); err != nil {
		t.Fatalf("completed shutdown lost to canceled caller context = %v", err)
	}
}

func TestServerShutdownGracefullyClosesIdleSpiceTailAndLease(t *testing.T) {
	t.Parallel()
	root, cancelRoot := context.WithCancel(context.Background())
	t.Cleanup(cancelRoot)
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
	host := &shutdownInteractionHost{
		grpcFixtureHost: &grpcFixtureHost{description: description, health: health},
		sessions:        sessions,
		opened:          make(chan *interactionFixtureObservation, 1),
	}
	token := endpointTokenFixture(t, 93)
	server, err := newServer(serverDependencies{
		root: root, token: token, host: host, sessions: sessions, build: build,
		capabilities: []string{"events"}, maximumSessions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	connection := serveGRPCFixture(t, server)
	authorization, err := token.AuthorizationValue()
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.NewOutgoingContext(
		t.Context(), metadata.Pairs(AuthenticationMetadataKey, authorization),
	)
	api := enginev1.NewEngineServiceClient(connection)
	initialized, err := api.Initialize(ctx, grpcInitializeRequest(limits))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := api.StreamInteractions(ctx, &enginev1.StreamInteractionsRequest{
		ClientId: initialized.GetClientId(), OwnershipEpoch: initialized.GetOwnershipEpoch(), Tail: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = stream.Recv(); err != nil {
			t.Fatalf("receive initial interaction page: %v", err)
		}
	}
	observation := <-host.opened
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
	defer cancelShutdown()
	if err = server.Shutdown(shutdownContext); err != nil {
		t.Fatalf("graceful idle-tail shutdown = %v", err)
	}
	if !observation.closed.Load() {
		t.Fatal("graceful shutdown did not close the idle observation")
	}
	next, err := sessions.ReconnectContext(
		context.Background(), initialized.GetClientId(), initialized.GetOwnershipEpoch(),
	)
	if err != nil || next.Epoch() != initialized.GetOwnershipEpoch()+1 {
		t.Fatalf("shutdown stream lease remained fenced: %#v, %v", next, err)
	}
}

func TestServerShutdownRejectsMissingContext(t *testing.T) {
	t.Parallel()
	//nolint:staticcheck // this is the public nil-context boundary regression.
	if err := (&Server{grpc: grpc.NewServer(), shutdownDone: make(chan struct{})}).Shutdown(nil); err == nil {
		t.Fatal("nil shutdown context succeeded")
	}
}

func TestRunHostAdapterNormalizesTypedNilObservations(t *testing.T) {
	t.Parallel()
	events, eventErr := normalizeEventObservation(nil, nil)
	interactions, interactionErr := normalizeInteractionObservation(nil, nil)
	if events != nil || eventErr != nil || interactions != nil || interactionErr != nil {
		t.Fatalf(
			"typed nil normalization = events %#v/%v interactions %#v/%v",
			events, eventErr, interactions, interactionErr,
		)
	}
}

type shutdownInteractionHost struct {
	*grpcFixtureHost
	sessions *daemon.SessionStore
	opened   chan *interactionFixtureObservation
}

func (host *shutdownInteractionHost) SubscribeInteractions(
	ctx context.Context,
	session daemon.Session,
) (interactionObservation, error) {
	lease, err := host.sessions.AcquireStream(ctx, session.ClientID(), session.Epoch())
	if err != nil {
		return nil, err
	}
	observation := newInteractionFixtureObservation(ctx, daemon.PendingSnapshot{}, true)
	observation.closeFn = lease.Close
	host.opened <- observation
	return observation, nil
}

var _ runHostBoundary = (*shutdownInteractionHost)(nil)

type shutdownFixtureService struct {
	enginev1.UnimplementedEngineServiceServer
	entered chan struct{}
	done    chan struct{}
}

func (service *shutdownFixtureService) StreamEvents(
	_ *enginev1.StreamEventsRequest,
	stream grpc.ServerStreamingServer[enginev1.StreamEventsResponse],
) error {
	close(service.entered)
	<-stream.Context().Done()
	close(service.done)
	return stream.Context().Err()
}

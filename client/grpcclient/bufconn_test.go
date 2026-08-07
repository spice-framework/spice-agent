package grpcclient

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon"
	"github.com/spice-framework/spice-agent/daemon/endpoint"
	"github.com/spice-framework/spice-agent/daemon/grpcserver"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestConnectorAcceptanceAgainstDaemonGRPCServer(t *testing.T) {
	t.Parallel()
	fixture := newDaemonFixture(t)
	session := fixture.initialize(t, nil)
	if session.Connection().OwnershipEpoch() != 1 {
		t.Fatalf("ownership epoch = %d, want 1", session.Connection().OwnershipEpoch())
	}
	health, err := session.Health(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if health.State() != client.HealthReady {
		t.Fatalf("health state = %q", health.State())
	}

	definition := session.Connection().Catalog().Definitions()[0].Ref()
	operation, err := client.NewOperationID("start-acceptance")
	if err != nil {
		t.Fatal(err)
	}
	input, err := client.NewInput("message-acceptance", "complete the scripted turn")
	if err != nil {
		t.Fatal(err)
	}
	startRequest, err := client.NewStartRequest(operation, definition, input)
	if err != nil {
		t.Fatal(err)
	}
	started, err := session.Start(t.Context(), startRequest)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := client.NewCursor(started.Run(), 0)
	if err != nil {
		t.Fatal(err)
	}
	options, err := client.NewEventStreamOptions(64, true, session.Connection().Limits())
	if err != nil {
		t.Fatal(err)
	}
	events, err := session.Events(t.Context(), cursor, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })
	readContext, cancelRead := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelRead()
	completed := false
	for !completed {
		frame, nextErr := events.Next(readContext)
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		value, ok := frame.Event()
		completed = ok && value.Kind() == client.EventRunCompleted
	}

	interactions, err := session.Interactions(t.Context(), client.NewInteractionStreamOptions(false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = interactions.Close() })
	first, err := interactions.Next(t.Context())
	if err != nil || first.Kind() != client.InteractionFrameUpdate {
		t.Fatalf("first interaction frame = %#v, %v", first, err)
	}
	second, err := interactions.Next(t.Context())
	if err != nil || second.Kind() != client.InteractionFrameControl {
		t.Fatalf("second interaction frame = %#v, %v", second, err)
	}
}

func TestReconnectClosesAndJoinsPriorStream(t *testing.T) {
	t.Parallel()
	fixture := newDaemonFixture(t)
	oldSession := fixture.initialize(t, nil)
	stream, err := oldSession.Interactions(t.Context(), client.NewInteractionStreamOptions(true))
	if err != nil {
		t.Fatal(err)
	}
	if err = stream.Close(); err != nil {
		t.Fatal(err)
	}
	claim, err := client.NewReconnectClaim(
		oldSession.Connection().ClientID(), oldSession.Connection().OwnershipEpoch(),
	)
	if err != nil {
		t.Fatal(err)
	}
	newSession := fixture.initialize(t, &claim)
	if newSession.Connection().OwnershipEpoch() != 2 {
		t.Fatalf("ownership epoch = %d, want 2", newSession.Connection().OwnershipEpoch())
	}
	if _, err = stream.Next(t.Context()); !errors.Is(err, client.ErrClosed) {
		t.Fatalf("old stream Next error = %v, want ErrClosed", err)
	}
	if _, err = oldSession.Health(t.Context()); !errors.Is(err, client.ErrClosed) {
		t.Fatalf("old session Health error = %v, want ErrClosed", err)
	}
}

func TestNextTimeoutDoesNotCloseInteractionStream(t *testing.T) {
	t.Parallel()
	fixture := newDaemonFixture(t)
	session := fixture.initialize(t, nil)
	stream, err := session.Interactions(t.Context(), client.NewInteractionStreamOptions(true))
	if err != nil {
		t.Fatal(err)
	}
	cancelledContext, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = stream.Next(cancelledContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled Next error = %v", err)
	}
	for range 2 {
		if _, err = stream.Next(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	timeoutContext, cancelTimeout := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancelTimeout()
	if _, err = stream.Next(timeoutContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed Next error = %v", err)
	}
	nextDone := make(chan error, 1)
	go func() {
		_, nextErr := stream.Next(context.Background())
		nextDone <- nextErr
	}()
	if err = stream.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-nextDone:
		if !errors.Is(err, client.ErrClosed) {
			t.Fatalf("Next after timeout and Close = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not unblock Next")
	}
}

func TestAuthenticationFailureIsTransportNeutral(t *testing.T) {
	t.Parallel()
	fixture := newDaemonFixture(t)
	wrongToken, err := endpoint.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	connector, err := New(Config{Connection: fixture.connection, Token: wrongToken})
	if err != nil {
		t.Fatal(err)
	}
	_, err = connector.Initialize(t.Context(), fixture.initializeRequest(t, nil))
	var statusErr *client.StatusError
	if !errors.As(err, &statusErr) || statusErr.Code() != client.ErrorUnauthenticated {
		t.Fatalf("Initialize error = %T %v", err, err)
	}
}

type daemonFixture struct {
	connector  *Connector
	connection *grpc.ClientConn
	build      client.Build
	limits     client.Limits
}

func newDaemonFixture(t *testing.T) *daemonFixture {
	t.Helper()
	root, cancelRoot := context.WithCancel(context.Background())
	t.Cleanup(cancelRoot)
	limits, err := client.NewLimits(2<<20, 256, 128, 2<<20, 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := daemon.NewSessionStore(root, 8)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := stage.NewDispatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := agent.NewEngine(
		completedProvider{}, dispatcher, &agent.AtomicIDSource{}, time.Now, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	agentDefinition, err := agent.NewDefinition("acceptance", "scripted", 1)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := daemon.NewDefinition("acceptance", "revision-1", agentDefinition)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := daemon.NewDefinitionSet([]daemon.Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	authority, err := daemon.NewRunAuthority(daemon.RunAuthorityConfig{
		Directory: filepath.Join(authorityTestRoot(t), "authority"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := daemon.NewLedger(8, 128)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := daemon.NewPendingHub(daemon.DefaultPendingLimits())
	if err != nil {
		t.Fatal(err)
	}
	host, err := daemon.NewRunHost(daemon.RunHostConfig{
		Root: root, Engine: engine, Authority: authority, Sessions: sessions,
		Ledger: ledger, Pending: pending, Definitions: definitions, Limits: limits,
		TerminalRuns: 16, TerminalBytes: 2 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := host.Shutdown(shutdownContext); shutdownErr != nil {
			t.Errorf("shutdown host: %v", shutdownErr)
		}
	})
	serverBuild, err := client.NewBuild("spice-agentd", "test", "commit", "go1.26.5")
	if err != nil {
		t.Fatal(err)
	}
	token, err := endpoint.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	server, err := grpcserver.NewServer(grpcserver.ServerConfig{
		Root: root, EndpointToken: token, Host: host, Sessions: sessions,
		Build: serverBuild, Capabilities: []string{"events", "interactions"}, MaximumSessions: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(16 << 20)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := server.Shutdown(shutdownContext); shutdownErr != nil {
			t.Errorf("shutdown gRPC server: %v", shutdownErr)
		}
		_ = listener.Close()
		if serveErr := <-serveDone; serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			t.Errorf("serve gRPC: %v", serveErr)
		}
	})
	connection, err := grpc.NewClient(
		"passthrough:///spice-agent-bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	connector, err := New(Config{Connection: connection, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	clientBuild, err := client.NewBuild("acceptance-client", "test", "commit", "go1.26.5")
	if err != nil {
		t.Fatal(err)
	}
	return &daemonFixture{
		connector: connector, connection: connection, build: clientBuild, limits: limits,
	}
}

func (fixture *daemonFixture) initialize(t *testing.T, reconnect *client.ReconnectClaim) client.Session {
	t.Helper()
	request := fixture.initializeRequest(t, reconnect)
	session, err := fixture.connector.Initialize(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func (fixture *daemonFixture) initializeRequest(
	t *testing.T,
	reconnect *client.ReconnectClaim,
) client.InitializeRequest {
	t.Helper()
	version, err := client.NewProtocolVersion(
		commonv1.ProtocolMajor, commonv1.ProtocolMinor, commonv1.ProtocolPatch,
	)
	if err != nil {
		t.Fatal(err)
	}
	protocol, err := client.NewProtocolRange(version, version)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := []string{"events", "interactions"}
	if reconnect == nil {
		request, requestErr := client.NewInitializeRequest(
			protocol, fixture.build, capabilities, capabilities, fixture.limits,
		)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return request
	}
	request, err := client.NewReconnectRequest(
		protocol, fixture.build, capabilities, capabilities, fixture.limits, *reconnect,
	)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

type completedProvider struct{}

func (completedProvider) Stream(context.Context, model.Request) (model.Stream, error) {
	completed, err := model.Completed(model.NewUsage(1, 1))
	if err != nil {
		return nil, err
	}
	return &completedStream{completed: completed}, nil
}

type completedStream struct {
	completed model.StreamEvent
	delivered bool
}

func (stream *completedStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return model.StreamEvent{}, err
	}
	if stream.delivered {
		return model.StreamEvent{}, io.EOF
	}
	stream.delivered = true
	return stream.completed, nil
}

func (*completedStream) Close() error { return nil }

func authorityTestRoot(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return t.TempDir()
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(cache, "spice-agent-grpcclient-tests")
	if err = os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "authority-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(root); removeErr != nil {
			t.Errorf("remove secure authority test root: %v", removeErr)
		}
	})
	return root
}

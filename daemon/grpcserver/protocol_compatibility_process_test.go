package grpcserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
	"github.com/spice-framework/spice-agent/client/grpcclient"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon"
	"github.com/spice-framework/spice-agent/daemon/endpoint"
	"github.com/spice-framework/spice-agent/daemon/localipc"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const compatibilityChildEnvironment = "SPICE_AGENT_ENGINE_COMPATIBILITY_CHILD"

const (
	compatibilityFaultNone              = ""
	compatibilityFaultUnavailableOnce   = "unavailable-once-after-commit"
	compatibilityFaultCancelAfterCommit = "cancel-after-commit"
)

type compatibilityChildConfig struct {
	Address       string `json:"address"`
	Authorization string `json:"authorization"`
	MaximumMinor  uint32 `json:"maximum_minor"`
	Fault         string `json:"fault,omitempty"`
}

type compatibilityChildStatus struct {
	State           string `json:"state"`
	MaximumMinor    uint32 `json:"maximum_minor"`
	InitializeCalls int    `json:"initialize_calls"`
	Commits         int    `json:"commits"`
}

func TestEngineProtocolSourceBuiltProcessMatrix(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("the decisive source-built matrix currently runs on Linux and Windows")
	}

	t.Run("exact legacy 1.2", func(t *testing.T) {
		fixture := startCompatibilityChild(t, 2, compatibilityFaultNone)
		connector, connection := compatibilityConnector(t, fixture.address, fixture.token)
		request := compatibilityLegacyRequest(t, 2)
		session, err := connector.Initialize(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if got := session.Connection().Protocol().Minor(); got != 2 {
			t.Fatalf("negotiated protocol minor = %d, want 2", got)
		}
		if _, present := request.AttemptID(); present {
			t.Fatal("legacy request unexpectedly carried an initialization attempt")
		}
		closeCompatibilityClient(t, session, connection)
		fixture.stop(t, 0, 0)
	})

	t.Run("adaptive current 1.3", func(t *testing.T) {
		fixture := startCompatibilityChild(t, 3, compatibilityFaultNone)
		connector, connection := compatibilityConnector(t, fixture.address, fixture.token)
		request := compatibilityAdaptiveRequest(t, 3, compatibilityAttempt(t, 1), "source-current")
		if _, present := request.AttemptID(); !present {
			t.Fatal("current request omitted its initialization attempt")
		}
		session, err := connector.Initialize(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if got := session.Connection().Protocol().Minor(); got != 3 {
			t.Fatalf("negotiated protocol minor = %d, want 3", got)
		}
		closeCompatibilityClient(t, session, connection)
		fixture.stop(t, 0, 0)
	})

	t.Run("explicit proven downgrade", func(t *testing.T) {
		fixture := startCompatibilityChild(t, 2, compatibilityFaultNone)
		connector, connection := compatibilityConnector(t, fixture.address, fixture.token)
		current := compatibilityAdaptiveRequest(t, 3, compatibilityAttempt(t, 2), "source-current")
		if session, err := connector.Initialize(t.Context(), current); session != nil {
			t.Fatal("incompatible current request created a session")
		} else if mismatch, ok := errors.AsType[*client.VersionMismatchError](err); !ok ||
			mismatch.Server().Maximum().Minor() != 2 || mismatch.Retryable() {
			t.Fatalf("current-to-previous mismatch = %T %v", err, err)
		}

		downgraded := compatibilityAdaptiveRequest(t, 2, client.InitializationAttemptID{}, "source-current")
		if _, present := downgraded.AttemptID(); present {
			t.Fatal("downgraded request retained protocol-1.3 replay semantics")
		}
		session, err := connector.Initialize(t.Context(), downgraded)
		if err != nil {
			t.Fatal(err)
		}
		if got := session.Connection().Protocol().Minor(); got != 2 {
			t.Fatalf("downgraded protocol minor = %d, want 2", got)
		}
		closeCompatibilityClient(t, session, connection)
		fixture.stop(t, 0, 0)
	})

	t.Run("authentication failure is definitive", func(t *testing.T) {
		fixture := startCompatibilityChild(t, 3, compatibilityFaultNone)
		wrong, err := endpoint.GenerateToken()
		if err != nil {
			t.Fatal(err)
		}
		connector, connection := compatibilityConnector(t, fixture.address, wrong)
		session, initializeErr := connector.Initialize(
			t.Context(),
			compatibilityAdaptiveRequest(t, 3, compatibilityAttempt(t, 3), "source-current"),
		)
		if session != nil || initializeErr == nil {
			t.Fatalf("wrong-token initialize = %#v, %v", session, initializeErr)
		}
		if replay, ok := errors.AsType[*client.InitializationReplayError](initializeErr); ok || replay != nil {
			t.Fatalf("authentication failure became replayable: %T %v", initializeErr, initializeErr)
		}
		if err = connection.Close(); err != nil {
			t.Fatal(err)
		}
		fixture.stop(t, 0, 0)
	})

	t.Run("current exact replay after response loss", func(t *testing.T) {
		fixture := startCompatibilityChild(t, 3, compatibilityFaultUnavailableOnce)
		connector, connection := compatibilityConnector(t, fixture.address, fixture.token)
		request := compatibilityAdaptiveRequest(t, 3, compatibilityAttempt(t, 4), "source-current")
		session, err := connector.Initialize(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		closeCompatibilityClient(t, session, connection)
		fixture.stop(t, 2, 1)
	})

	t.Run("legacy ambiguity never retries", func(t *testing.T) {
		fixture := startCompatibilityChild(t, 2, compatibilityFaultUnavailableOnce)
		connector, connection := compatibilityConnector(t, fixture.address, fixture.token)
		session, err := connector.Initialize(t.Context(), compatibilityLegacyRequest(t, 2))
		if session != nil || err == nil {
			t.Fatalf("ambiguous legacy initialize = %#v, %v", session, err)
		}
		if replay, ok := errors.AsType[*client.InitializationReplayError](err); ok || replay != nil {
			t.Fatalf("legacy ambiguity became exact-replay capable: %T %v", err, err)
		}
		if failure, ok := errors.AsType[client.StatusFailure](err); !ok || failure.Retryable() {
			t.Fatalf("legacy ambiguity retry contract = %T %v", err, err)
		}
		if closeErr := connection.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		fixture.stop(t, 1, 1)
	})

	t.Run("cancellation preserves exact replay only", func(t *testing.T) {
		fixture := startCompatibilityChild(t, 3, compatibilityFaultCancelAfterCommit)
		connector, connection := compatibilityConnector(t, fixture.address, fixture.token)
		attempt := compatibilityAttempt(t, 5)
		request := compatibilityAdaptiveRequest(t, 3, attempt, "source-current")
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		defer cancel()
		session, err := connector.Initialize(ctx, request)
		replay, replayable := errors.AsType[*client.InitializationReplayError](err)
		if session != nil || !replayable || replay.AttemptID() != attempt ||
			!errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("canceled initialize = %#v, %T %v", session, err, err)
		}

		conflicting := compatibilityAdaptiveRequest(t, 3, attempt, "changed-source")
		if conflictingSession, conflictingErr := connector.Initialize(t.Context(), conflicting); conflictingSession != nil {
			t.Fatal("conflicting exact-replay request created a session")
		} else if statusFailure, ok := errors.AsType[*client.StatusError](conflictingErr); !ok ||
			statusFailure.Code() != client.ErrorConflict {
			t.Fatalf("conflicting replay = %T %v", conflictingErr, conflictingErr)
		}

		session, err = connector.Initialize(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		closeCompatibilityClient(t, session, connection)
		fixture.stop(t, 3, 1)
	})
}

func TestEngineProtocolCompatibilityChild(t *testing.T) {
	if os.Getenv(compatibilityChildEnvironment) != "1" {
		t.Skip("compatibility child entrypoint")
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var config compatibilityChildConfig
	if err = json.Unmarshal(bytes.TrimSpace(line), &config); err != nil {
		t.Fatal(err)
	}
	if config.MaximumMinor != 2 && config.MaximumMinor != 3 {
		t.Fatalf("unsupported compatibility maximum minor %d", config.MaximumMinor)
	}
	token, err := endpoint.ParseAuthorizationValue(config.Authorization)
	if err != nil {
		t.Fatal("compatibility child authorization is invalid")
	}
	listener, err := localipc.Listen(config.Address)
	if err != nil {
		t.Fatal(err)
	}

	stop, stats := serveCompatibilityChild(t, listener, token, config)
	ready := compatibilityChildStatus{State: "ready", MaximumMinor: config.MaximumMinor}
	writeCompatibilityChildStatus(t, ready)
	command, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(command) != "STOP" {
		t.Fatalf("compatibility child stop command = %q, %v", command, err)
	}
	stop()
	stopped := stats()
	stopped.State = "stopped"
	stopped.MaximumMinor = config.MaximumMinor
	writeCompatibilityChildStatus(t, stopped)
}

func serveCompatibilityChild(
	t *testing.T,
	listener net.Listener,
	token endpoint.Token,
	config compatibilityChildConfig,
) (func(), func() compatibilityChildStatus) {
	t.Helper()
	if config.Fault != compatibilityFaultNone {
		return serveCompatibilityFaultChild(t, listener, token, config)
	}

	root, cancel := context.WithCancel(context.Background())
	build, _, health, definitions := wireFixtureValues(t)
	description, err := daemon.NewRunHostDescription(definitions, health)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := daemon.NewSessionStore(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	server, err := newServer(serverDependencies{
		root: root, token: token, host: &grpcFixtureHost{description: description, health: health},
		sessions: sessions, build: build, capabilities: []string{"events"}, maximumSessions: 1,
		protocolRange: compatibilityProtocolRange(config.MaximumMinor),
	})
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	stop := func() {
		shutdown, stopShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopShutdown()
		if shutdownErr := server.Shutdown(shutdown); shutdownErr != nil {
			t.Errorf("compatibility server shutdown: %v", shutdownErr)
		}
		if sessionErr := sessions.Shutdown(shutdown); sessionErr != nil {
			t.Errorf("compatibility session shutdown: %v", sessionErr)
		}
		cancel()
		if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			t.Errorf("compatibility listener close: %v", closeErr)
		}
		if serveErr := <-serveDone; serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) &&
			!errors.Is(serveErr, net.ErrClosed) {
			t.Errorf("compatibility server serve: %v", serveErr)
		}
	}
	return stop, func() compatibilityChildStatus { return compatibilityChildStatus{} }
}

func serveCompatibilityFaultChild(
	t *testing.T,
	listener net.Listener,
	token endpoint.Token,
	config compatibilityChildConfig,
) (func(), func() compatibilityChildStatus) {
	t.Helper()
	if config.Fault != compatibilityFaultUnavailableOnce && config.Fault != compatibilityFaultCancelAfterCommit {
		t.Fatalf("unsupported compatibility fault %q", config.Fault)
	}
	build, limits, health, definitions := wireFixtureValues(t)
	wireBuild, err := buildToWire(build)
	if err != nil {
		t.Fatal(err)
	}
	wireCapabilities, err := capabilitiesToWire([]string{"events"})
	if err != nil {
		t.Fatal(err)
	}
	wireLimits, err := limitsToWire(limits)
	if err != nil {
		t.Fatal(err)
	}
	wireHealth, err := healthToWire(health)
	if err != nil {
		t.Fatal(err)
	}
	wireDefinitions, err := definitionsToWire(definitions, wireLimits)
	if err != nil {
		t.Fatal(err)
	}
	service := &compatibilityFaultService{
		mode: config.Fault, protocol: compatibilityProtocolRange(config.MaximumMinor),
		build: wireBuild, capabilities: wireCapabilities, limits: wireLimits,
		health: wireHealth, definitions: wireDefinitions,
	}
	unary, stream, err := newAuthenticationInterceptors(token)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(enginev1.InitializeBootstrapMaximumBytes),
		grpc.MaxSendMsgSize(enginev1.InitializeBootstrapMaximumBytes),
		grpc.UnaryInterceptor(unary), grpc.StreamInterceptor(stream),
	)
	enginev1.RegisterEngineServiceServer(server, service)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	stop := func() {
		server.Stop()
		if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			t.Errorf("compatibility fault listener close: %v", closeErr)
		}
		if serveErr := <-serveDone; serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) &&
			!errors.Is(serveErr, net.ErrClosed) {
			t.Errorf("compatibility fault server serve: %v", serveErr)
		}
	}
	return stop, service.stats
}

type compatibilityFaultService struct {
	enginev1.UnimplementedEngineServiceServer

	mu           sync.Mutex
	mode         string
	protocol     *commonv1.ProtocolRange
	build        *commonv1.BuildIdentity
	capabilities *commonv1.CapabilitySet
	limits       *commonv1.Limits
	health       *commonv1.Health
	definitions  *enginev1.DefinitionSet
	request      *enginev1.InitializeRequest
	response     *enginev1.InitializeResponse
	calls        int
	commits      int
}

func (service *compatibilityFaultService) Initialize(
	ctx context.Context,
	request *enginev1.InitializeRequest,
) (*enginev1.InitializeResponse, error) {
	service.mu.Lock()
	service.calls++
	if service.response != nil {
		if !proto.Equal(service.request, request) {
			service.mu.Unlock()
			return &enginev1.InitializeResponse{Status: &commonv1.Status{
				Code: commonv1.ErrorCode_ERROR_CODE_CONFLICT, Message: "initialization attempt conflicts with committed intent",
			}}, nil
		}
		response := proto.CloneOf(service.response)
		service.mu.Unlock()
		return response, nil
	}
	response := enginev1.NegotiateInitialize(
		request, proto.CloneOf(service.protocol), proto.CloneOf(service.build),
		proto.CloneOf(service.capabilities), proto.CloneOf(service.limits), proto.CloneOf(service.health),
		proto.CloneOf(service.definitions), "compatibility-client", 1,
	)
	if response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_OK {
		service.mu.Unlock()
		return response, nil
	}
	service.request = proto.CloneOf(request)
	service.response = proto.CloneOf(response)
	service.commits++
	mode := service.mode
	service.mu.Unlock()

	switch mode {
	case compatibilityFaultUnavailableOnce:
		return nil, status.Error(codes.Unavailable, "committed initialization acknowledgement was lost")
	case compatibilityFaultCancelAfterCommit:
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	default:
		return response, nil
	}
}

func (service *compatibilityFaultService) stats() compatibilityChildStatus {
	service.mu.Lock()
	defer service.mu.Unlock()
	return compatibilityChildStatus{InitializeCalls: service.calls, Commits: service.commits}
}

type compatibilityFixture struct {
	process *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	stderr  *bytes.Buffer
	address string
	token   endpoint.Token
	stopped bool
}

func startCompatibilityChild(t *testing.T, maximumMinor uint32, fault string) *compatibilityFixture {
	t.Helper()
	token, err := endpoint.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := token.AuthorizationValue()
	if err != nil {
		t.Fatal(err)
	}
	address := compatibilityAddress(t)
	// #nosec G204 -- the exact current source-built test executable is the compatibility fixture.
	command := exec.Command(os.Args[0], "-test.run=^TestEngineProtocolCompatibilityChild$", "-test.count=1")
	command.Env = append(os.Environ(), compatibilityChildEnvironment+"=1")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := new(bytes.Buffer)
	command.Stderr = stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	fixture := &compatibilityFixture{
		process: command, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: stderr,
		address: address, token: token,
	}
	t.Cleanup(func() {
		if !fixture.stopped {
			_ = fixture.process.Process.Kill()
			_ = fixture.process.Wait()
		}
	})
	config := compatibilityChildConfig{
		Address: address, Authorization: authorization, MaximumMinor: maximumMinor, Fault: fault,
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stdin.Write(append(encoded, '\n')); err != nil {
		t.Fatal(err)
	}
	statusValue := fixture.readStatus(t, 10*time.Second)
	if statusValue.State != "ready" || statusValue.MaximumMinor != maximumMinor {
		t.Fatalf("compatibility child ready = %#v", statusValue)
	}
	return fixture
}

func (fixture *compatibilityFixture) stop(t *testing.T, wantCalls, wantCommits int) {
	t.Helper()
	if fixture.stopped {
		t.Fatal("compatibility fixture stopped twice")
	}
	fixture.stopped = true
	if _, err := io.WriteString(fixture.stdin, "STOP\n"); err != nil {
		t.Fatalf("stop compatibility child: %v", err)
	}
	statusValue := fixture.readStatus(t, 10*time.Second)
	if statusValue.State != "stopped" || statusValue.InitializeCalls != wantCalls ||
		statusValue.Commits != wantCommits {
		t.Fatalf("compatibility child stopped = %#v, want calls/commits %d/%d", statusValue, wantCalls, wantCommits)
	}
	if err := fixture.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.process.Wait(); err != nil {
		t.Fatalf("compatibility child exit: %v; stderr: %s", err, fixture.stderr.String())
	}
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(fixture.address); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("compatibility socket remains after shutdown: %v", err)
		}
	}
}

func (fixture *compatibilityFixture) readStatus(t *testing.T, timeout time.Duration) compatibilityChildStatus {
	t.Helper()
	type result struct {
		line []byte
		err  error
	}
	completed := make(chan result, 1)
	go func() {
		line, err := fixture.stdout.ReadBytes('\n')
		completed <- result{line: line, err: err}
	}()
	select {
	case <-time.After(timeout):
		t.Fatalf("compatibility child status timed out; stderr: %s", fixture.stderr.String())
	case observed := <-completed:
		if observed.err != nil {
			t.Fatalf("read compatibility child status: %v; stderr: %s", observed.err, fixture.stderr.String())
		}
		var statusValue compatibilityChildStatus
		if err := json.Unmarshal(bytes.TrimSpace(observed.line), &statusValue); err != nil {
			t.Fatalf("decode compatibility child status %q: %v; stderr: %s", observed.line, err, fixture.stderr.String())
		}
		return statusValue
	}
	return compatibilityChildStatus{}
}

func writeCompatibilityChildStatus(t *testing.T, value compatibilityChildStatus) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintln(os.Stdout, string(encoded)); err != nil {
		t.Fatal(err)
	}
}

func compatibilityAddress(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`\\.\pipe\spice-agent-engine-compat-%d-%d`, os.Getpid(), time.Now().UnixNano())
	}
	directory, err := os.MkdirTemp("", "spice-engine-compat-")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "engine.sock")
}

func compatibilityConnector(
	t *testing.T,
	address string,
	token endpoint.Token,
) (*grpcclient.Connector, *grpc.ClientConn) {
	t.Helper()
	connection, err := grpc.NewClient(
		"passthrough:///spice-agent-engine-compatibility",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDisableServiceConfig(),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return localipc.Dial(ctx, address)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := grpcclient.New(grpcclient.Config{Connection: connection, Token: token})
	if err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	return connector, connection
}

func closeCompatibilityClient(t *testing.T, session client.Session, connection *grpc.ClientConn) {
	t.Helper()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
}

func compatibilityAdaptiveRequest(
	t *testing.T,
	serverMaximumMinor uint32,
	attempt client.InitializationAttemptID,
	version string,
) client.InitializeRequest {
	t.Helper()
	if serverMaximumMinor < enginev1.InitializationAttemptMinimumMinor {
		return compatibilityLegacyRequest(t, min(serverMaximumMinor, uint32(2)))
	}
	protocol := compatibilityClientProtocolRange(t, 0, 3)
	build := compatibilityClientBuild(t, version)
	request, err := client.NewInitializeRequestWithAttempt(
		protocol, build, []string{"events"}, []string{"events"}, compatibilityClientLimits(t), attempt,
	)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func compatibilityLegacyRequest(t *testing.T, maximumMinor uint32) client.InitializeRequest {
	t.Helper()
	protocol := compatibilityClientProtocolRange(t, maximumMinor, maximumMinor)
	request, err := client.NewLegacyInitializeRequest(
		protocol, compatibilityClientBuild(t, "source-previous"),
		[]string{"events"}, []string{"events"}, compatibilityClientLimits(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func compatibilityClientProtocolRange(t *testing.T, minimumMinor, maximumMinor uint32) client.ProtocolRange {
	t.Helper()
	minimum, err := client.NewProtocolVersion(1, minimumMinor, 0)
	if err != nil {
		t.Fatal(err)
	}
	maximum, err := client.NewProtocolVersion(1, maximumMinor, 0)
	if err != nil {
		t.Fatal(err)
	}
	value, err := client.NewProtocolRange(minimum, maximum)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func compatibilityClientBuild(t *testing.T, version string) client.Build {
	t.Helper()
	value, err := client.NewBuild("compatibility-client", version, "source-built", runtime.Version())
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func compatibilityClientLimits(t *testing.T) client.Limits {
	t.Helper()
	value, err := client.NewLimits(1<<20, 64, 128, 1<<20, 8, 16)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func compatibilityAttempt(t *testing.T, suffix byte) client.InitializationAttemptID {
	t.Helper()
	encoded := fmt.Sprintf("000000000000000000000000000000%02x", suffix)
	value, err := client.ParseInitializationAttemptID(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func compatibilityProtocolRange(maximumMinor uint32) *commonv1.ProtocolRange {
	return &commonv1.ProtocolRange{
		Minimum: &commonv1.ProtocolVersion{Major: 1},
		Maximum: &commonv1.ProtocolVersion{Major: 1, Minor: maximumMinor},
	}
}

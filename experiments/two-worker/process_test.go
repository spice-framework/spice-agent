package twoworker

import (
	"bufio"
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
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/client"
	"github.com/spice-framework/spice-agent/client/grpcclient"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon"
	"github.com/spice-framework/spice-agent/daemon/endpoint"
	"github.com/spice-framework/spice-agent/daemon/grpcserver"
	"github.com/spice-framework/spice-agent/daemon/localipc"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	helperEnvironment    = "SPICE_TWO_WORKER_HELPER"
	addressEnvironment   = "SPICE_TWO_WORKER_ADDRESS"
	authorityEnvironment = "SPICE_TWO_WORKER_AUTHORITY"
)

func TestTwoWorkerUsesAuthenticatedCurrentUserProcessBoundary(t *testing.T) {
	root := secureProcessRoot(t)
	address := processAddress(root)
	token, err := endpoint.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := token.AuthorizationValue()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestWorkerHostProcess$")
	command.Env = append(
		os.Environ(),
		helperEnvironment+"=1",
		addressEnvironment+"="+address,
		authorityEnvironment+"="+filepath.Join(root, "authority"),
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped && command.Process != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	if _, err = io.WriteString(stdin, authorization+"\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	ready := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		line, readErr := reader.ReadString('\n')
		ready <- struct {
			line string
			err  error
		}{line: line, err: readErr}
	}()
	select {
	case value := <-ready:
		if value.err != nil || value.line != "READY\n" {
			t.Fatalf("worker readiness = %q, %v; stderr=%q", value.line, value.err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("worker readiness timed out; stderr=%q", stderr.String())
	}

	connection, err := grpc.NewClient(
		"passthrough:///spice-agent-two-worker",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return localipc.Dial(ctx, address)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	wrongToken, err := endpoint.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	wrongConnector, err := grpcclient.New(grpcclient.Config{Connection: connection, Token: wrongToken})
	if err != nil {
		t.Fatal(err)
	}
	request := initializeRequest(t)
	if _, err = wrongConnector.Initialize(t.Context(), request); err == nil {
		t.Fatal("wrong endpoint credential was accepted")
	}
	statusFailure, ok := errors.AsType[*client.StatusError](err)
	if !ok || statusFailure.Code() != client.ErrorUnauthenticated {
		t.Fatalf("wrong credential error = %T %v", err, err)
	}

	connector, err := grpcclient.New(grpcclient.Config{Connection: connection, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	session, err := connector.Initialize(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	reference, err := client.NewDefinitionRef("worker", "revision-1")
	if err != nil {
		t.Fatal(err)
	}
	delegate, err := NewDelegate(session, Options{Definition: reference, MaximumEvents: 64})
	if err != nil {
		t.Fatal(err)
	}
	cancelContext, cancel := context.WithCancel(t.Context())
	_, err = delegate.Execute(
		cancelContext,
		mustCall(t, "process-cancellation", `{"task":"wait for cancellation"}`),
		reporterFunc(func(context.Context, tool.Progress) error {
			cancel()
			return nil
		}),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("distributed cancellation error = %T %v", err, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		health, healthErr := session.Health(t.Context())
		if healthErr == nil && health.ActiveRuns() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("delegated cancellation did not terminate the remote run: health=%#v err=%v", health, healthErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	result, err := delegate.Execute(
		t.Context(), mustCall(t, "process-delegation", `{"task":"inspect the delegated package"}`), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	var payload resultPayload
	if err = jsonUnmarshal(result.Content(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Text != "worker handled: inspect the delegated package" {
		t.Fatalf("worker response = %q", payload.Text)
	}
	if _, err = io.WriteString(stdin, "STOP\n"); err != nil {
		t.Fatal(err)
	}
	if err = stdin.Close(); err != nil {
		t.Fatal(err)
	}
	remaining, readErr := io.ReadAll(reader)
	if err = command.Wait(); err != nil {
		t.Fatalf("worker exit: %v; stderr=%q", err, stderr.String())
	}
	stopped = true
	if readErr != nil || len(remaining) != 0 || stderr.Len() != 0 {
		t.Fatalf("worker output after readiness = %q, %v; stderr=%q", remaining, readErr, stderr.String())
	}
}

// TestWorkerHostProcess is an OS-process fixture. The endpoint credential is
// read from inherited stdin and never appears in arguments, environment,
// stdout, stderr, events, or generated files.
func TestWorkerHostProcess(t *testing.T) {
	if os.Getenv(helperEnvironment) != "1" {
		t.Skip("worker host helper")
	}
	os.Exit(runWorkerHostProcess())
}

func runWorkerHostProcess() int {
	reader := bufio.NewReader(os.Stdin)
	authorization, err := reader.ReadString('\n')
	if err != nil {
		return 2
	}
	token, err := endpoint.ParseAuthorizationValue(strings.TrimSuffix(authorization, "\n"))
	if err != nil {
		return 2
	}
	address := os.Getenv(addressEnvironment)
	authorityDirectory := os.Getenv(authorityEnvironment)
	root, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	host, sessions, err := newProcessRunHost(root, authorityDirectory)
	if err != nil {
		return 3
	}
	build, err := client.NewBuild("two-worker-host", "experimental", "process-fixture", "go1.26.5")
	if err != nil {
		return 3
	}
	server, err := grpcserver.NewServer(grpcserver.ServerConfig{
		Root: root, EndpointToken: token, Host: host, Sessions: sessions,
		Build: build, Capabilities: []string{"events"}, MaximumSessions: 4,
	})
	if err != nil {
		return 3
	}
	listener, err := localipc.Listen(address)
	if err != nil {
		return 4
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	if _, err = io.WriteString(os.Stdout, "READY\n"); err != nil {
		return 5
	}
	line, err := reader.ReadString('\n')
	if err != nil || line != "STOP\n" {
		return 5
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverErr := server.Shutdown(shutdownContext)
	listenerErr := listener.Close()
	serveErr := <-serveDone
	hostErr := host.Shutdown(shutdownContext)
	if serverErr != nil || listenerErr != nil || hostErr != nil || serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
		return 6
	}
	return 0
}

func newProcessRunHost(root context.Context, authorityDirectory string) (*daemon.RunHost, *daemon.SessionStore, error) {
	limits, err := client.NewLimits(2<<20, 256, 128, 2<<20, 8, 8)
	if err != nil {
		return nil, nil, err
	}
	sessions, err := daemon.NewSessionStore(root, 4)
	if err != nil {
		return nil, nil, err
	}
	dispatcher, err := stage.NewDispatcher(nil)
	if err != nil {
		return nil, nil, err
	}
	engine, err := agent.NewEngine(echoProvider{}, dispatcher, &agent.AtomicIDSource{}, time.Now, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	agentDefinition, err := agent.NewDefinition("worker", "scripted", 1)
	if err != nil {
		return nil, nil, err
	}
	definition, err := daemon.NewDefinition("worker", "revision-1", agentDefinition)
	if err != nil {
		return nil, nil, err
	}
	definitions, err := daemon.NewDefinitionSet([]daemon.Definition{definition})
	if err != nil {
		return nil, nil, err
	}
	authority, err := daemon.NewRunAuthority(daemon.RunAuthorityConfig{Directory: authorityDirectory})
	if err != nil {
		return nil, nil, err
	}
	ledger, err := daemon.NewLedger(4, 64)
	if err != nil {
		return nil, nil, err
	}
	pending, err := daemon.NewPendingHub(daemon.DefaultPendingLimits())
	if err != nil {
		return nil, nil, err
	}
	host, err := daemon.NewRunHost(daemon.RunHostConfig{
		Root: root, Engine: engine, Authority: authority, Sessions: sessions,
		Ledger: ledger, Pending: pending, Definitions: definitions, Limits: limits,
		TerminalRuns: 16, TerminalBytes: 2 << 20,
	})
	return host, sessions, err
}

type echoProvider struct{}

func (echoProvider) Stream(ctx context.Context, request model.Request) (model.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	text := "worker handled"
	input := ""
	messages := request.Messages()
	if len(messages) != 0 {
		parts := messages[len(messages)-1].Parts()
		for _, part := range parts {
			if value, ok := part.TextValue(); ok && messages[len(messages)-1].Role() == message.RoleUser {
				input = value
				text += ": " + input
				break
			}
		}
	}
	if input == "wait for cancellation" {
		return blockingProcessStream{}, nil
	}
	delta, err := model.TextDelta(text)
	if err != nil {
		return nil, err
	}
	completed, err := model.Completed(model.NewUsage(1, 1))
	if err != nil {
		return nil, err
	}
	return &processStream{events: []model.StreamEvent{delta, completed}}, nil
}

type processStream struct {
	events []model.StreamEvent
	next   int
}

func (stream *processStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return model.StreamEvent{}, err
	}
	if stream.next == len(stream.events) {
		return model.StreamEvent{}, io.EOF
	}
	value := stream.events[stream.next]
	stream.next++
	return value, nil
}

func (*processStream) Close() error { return nil }

type blockingProcessStream struct{}

func (blockingProcessStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	<-ctx.Done()
	return model.StreamEvent{}, ctx.Err()
}

func (blockingProcessStream) Close() error { return nil }

func initializeRequest(t testing.TB) client.InitializeRequest {
	t.Helper()
	version, err := client.NewProtocolVersion(commonv1.ProtocolMajor, commonv1.ProtocolMinor, commonv1.ProtocolPatch)
	if err != nil {
		t.Fatal(err)
	}
	protocol, err := client.NewProtocolRange(version, version)
	if err != nil {
		t.Fatal(err)
	}
	build, err := client.NewBuild("two-worker-client", "experimental", "process-test", "go1.26.5")
	if err != nil {
		t.Fatal(err)
	}
	limits, err := client.NewLimits(2<<20, 256, 128, 2<<20, 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := client.NewInitializationAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	request, err := client.NewInitializeRequestWithAttempt(
		protocol, build, []string{"events"}, []string{"events"}, limits, attempt,
	)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func secureProcessRoot(t *testing.T) string {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(cache, "spice-agent-two-worker-tests")
	if err = os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "process-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func processAddress(root string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`\\.\pipe\spice-agent-two-worker-%d-%d`, os.Getpid(), time.Now().UnixNano())
	}
	return filepath.Join(root, "worker.sock")
}

func jsonUnmarshal(value []byte, destination any) error {
	return json.Unmarshal(value, destination)
}

var _ tool.Tool = (*Delegate)(nil)

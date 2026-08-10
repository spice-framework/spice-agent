package pluginconformanceacceptance_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	breadthEchoInput = "echo through runtime plugin"
	breadthWaitInput = "wait in runtime plugin"
	breadthLossInput = "execute after runtime plugin loss"
)

func TestRuntimePluginLanguagesBehindExactEngineModes(t *testing.T) {
	root := repositoryRoot(t)
	buildContext, cancelBuild := context.WithTimeout(context.Background(), fixtureBuildTimeout)
	goExecutable := buildFixture(t, buildContext, root)
	cancelBuild()

	type languageFixture struct {
		name  string
		start func(*testing.T) *breadthPluginProcess
	}
	languages := []languageFixture{
		{name: "go", start: func(t *testing.T) *breadthPluginProcess {
			t.Helper()
			return startGoBreadthPlugin(t, root, goExecutable)
		}},
		{name: "python", start: func(t *testing.T) *breadthPluginProcess {
			t.Helper()
			if os.Getenv(pythonConformanceEnvironment) != "1" {
				t.Skip("set " + pythonConformanceEnvironment + "=1 to run the Python breadth matrix")
			}
			return startPythonBreadthPlugin(t, root)
		}},
	}

	results := make(map[string]string)
	for _, language := range languages {
		t.Run(language.name, func(t *testing.T) {
			for _, engineMinor := range []uint32{2, 3} {
				t.Run(fmt.Sprintf("engine-1.%d", engineMinor), func(t *testing.T) {
					plugin := language.start(t)
					source := newBreadthPlanSource(t, language.name, engineMinor, plugin.tools(t))
					harness := newBreadthEngineHarness(t, language.name, engineMinor, source)

					successEvents := harness.run(t, "success", breadthEchoInput, nil)
					assertBreadthSuccess(t, successEvents)
					observedResults := harness.provider.resultsSnapshot()
					if len(observedResults) != 1 || observedResults[0] != `{"value":"hello"}` {
						t.Fatalf("provider-observed plugin results = %q", observedResults)
					}
					results[language.name+fmt.Sprintf("-1.%d", engineMinor)] = observedResults[0]
					harness.waitForReleasedLeases(t, 1)

					cancelled := false
					cancellationEvents := harness.run(t, "cancel", breadthWaitInput, func(value client.Event) {
						if value.Kind() != client.EventToolProgress || cancelled {
							return
						}
						cancelled = true
						operation, err := client.NewOperationID("plugin-breadth-cancel-" + language.name + fmt.Sprint(engineMinor))
						if err != nil {
							t.Fatal(err)
						}
						request, err := client.NewCancelRequest(value.Run(), operation, "compatibility cancellation")
						if err != nil {
							t.Fatal(err)
						}
						outcome, err := harness.session.Cancel(t.Context(), request)
						if err != nil || !outcome.Requested() {
							t.Fatalf("cancel runtime-plugin run = %#v, %v", outcome, err)
						}
					})
					if !cancelled {
						t.Fatal("runtime-plugin cancellation never observed admission progress")
					}
					assertBreadthCancellation(t, cancellationEvents)
					harness.waitForReleasedLeases(t, 2)

					plugin.shutdown(t)
					lossEvents := harness.run(t, "loss", breadthLossInput, nil)
					assertBreadthProcessLoss(t, lossEvents)
					harness.waitForReleasedLeases(t, 3)
					if got := source.acquired.Load(); got != 3 {
						t.Fatalf("tool-plan acquisitions = %d, want 3", got)
					}
					if plugin.protocolMinor != pluginv1.ProtocolMinor {
						t.Fatalf("plugin protocol changed with engine mode: 1.%d", plugin.protocolMinor)
					}
					harness.close(t)
					plugin.close(t)
				})
			}
		})
	}

	var expected string
	for identity, result := range results {
		if expected == "" {
			expected = result
		}
		if result != expected {
			t.Fatalf("runtime-plugin result for %s = %q, want %q", identity, result, expected)
		}
	}
}

type breadthPluginProcess struct {
	language      string
	command       *exec.Cmd
	stderr        bytes.Buffer
	stdoutTail    <-chan outputCapture
	connection    *grpc.ClientConn
	client        pluginv1.PluginServiceClient
	sessionID     []byte
	limits        *pluginv1.Limits
	catalog       pluginv1.Catalog
	secretEncoded string
	protocolMinor uint32
	waitOnce      sync.Once
	waitDone      chan struct{}
	waitErr       error
	stopped       bool
}

func startGoBreadthPlugin(t *testing.T, root, executable string) *breadthPluginProcess {
	t.Helper()
	address := fixtureAddress(t)
	command := exec.Command(executable) // #nosec G204 -- exact source-built Go fixture.
	command.Dir = root
	return startBreadthPlugin(t, "go", command, address, localipc.Dial)
}

func startPythonBreadthPlugin(t *testing.T, root string) *breadthPluginProcess {
	t.Helper()
	uv, err := exec.LookPath("uv")
	if err != nil {
		t.Fatal("Python runtime-plugin breadth requires uv on PATH")
	}
	fixture := filepath.Join(root, "testdata", "runtimeplugin", "python")
	command := exec.Command( // #nosec G204 -- exact uv executable and locked offline fixture.
		uv, "run", "--frozen", "--offline", "--directory", fixture,
		"python", "-m", "spice_agent_python_fixture.main",
	)
	command.Dir = root
	address := pythonFixtureAddress(t)
	return startBreadthPlugin(t, "python", command, address, func(ctx context.Context, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", address)
	})
}

func startBreadthPlugin(
	t *testing.T,
	language string,
	command *exec.Cmd,
	address string,
	dial func(context.Context, string) (net.Conn, error),
) *breadthPluginProcess {
	t.Helper()
	secret := bytes.Repeat([]byte{0x61 + byte(len(language))}, pluginv1.HandshakeSecretBytes)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	process := &breadthPluginProcess{
		language: language, command: command,
		secretEncoded: base64.RawURLEncoding.EncodeToString(secret),
	}
	command.Stderr = &process.stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		process.cleanup()
		if process.connection != nil {
			_ = process.connection.Close()
		}
	})
	bootstrap := map[string]string{"address": address, "secret": process.secretEncoded}
	if err = json.NewEncoder(stdin).Encode(bootstrap); err != nil {
		t.Fatal(err)
	}
	if err = stdin.Close(); err != nil {
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
	case <-time.After(acceptanceTimeout):
		t.Fatalf("%s plugin readiness timed out; stderr=%q", language, process.stderr.String())
	case observed := <-ready:
		if observed.err != nil || observed.line != "{\"ready\":true}\n" {
			t.Fatalf("%s plugin readiness = %q, %v; stderr=%q", language, observed.line, observed.err, process.stderr.String())
		}
	}
	process.stdoutTail = captureOutput(reader, 1024)
	connection, err := grpc.NewClient(
		"passthrough:///spice-agent-plugin-breadth",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDisableServiceConfig(),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return dial(ctx, address) }),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(pluginv1.InitializeBootstrapMaximumBytes),
			grpc.MaxCallSendMsgSize(pluginv1.InitializeBootstrapMaximumBytes),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	process.connection = connection
	process.client = pluginv1.NewPluginServiceClient(connection)
	process.initialize(t, secret)
	clear(secret)
	return process
}

func (process *breadthPluginProcess) initialize(t *testing.T, secret []byte) {
	t.Helper()
	launchID := bytes.Repeat([]byte{0x31}, pluginv1.LaunchIDBytes)
	challenge := bytes.Repeat([]byte{0x42}, pluginv1.HandshakeChallengeBytes)
	request := &pluginv1.InitializeRequest{
		Protocol: pluginv1.SupportedProtocolRange(),
		Host: &pluginv1.BuildIdentity{
			Component: "spice-agent-plugin-breadth", Version: "source", Commit: "test", Runtime: runtime.Version(),
		},
		SupportedCapabilities: &commonv1.CapabilitySet{Names: []string{pluginv1.CapabilityRuntimeToolsV1}},
		RequiredCapabilities:  &commonv1.CapabilitySet{Names: []string{pluginv1.CapabilityRuntimeToolsV1}},
		RequestedLimits:       conformanceLimits(),
		LaunchId:              launchID,
		HandshakeChallenge:    challenge,
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	response, err := process.client.Initialize(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err = pluginv1.ValidateInitializeResponseForRequest(request, response, secret); err != nil {
		t.Fatal(err)
	}
	catalog, err := pluginv1.DecodeManifest(response.GetManifest(), response.GetLimits())
	if err != nil {
		t.Fatal(err)
	}
	process.sessionID = slices.Clone(response.GetSessionId())
	process.limits = response.GetLimits()
	process.catalog = catalog
	process.protocolMinor = response.GetProtocol().GetMinor()
}

func (process *breadthPluginProcess) tools(t *testing.T) map[string]tool.Tool {
	t.Helper()
	result := make(map[string]tool.Tool)
	for _, definition := range process.catalog.Definitions() {
		if definition.Name() != "conformance.echo" && definition.Name() != "conformance.wait" {
			continue
		}
		result[definition.Name()] = &breadthRemoteTool{
			client: process.client, sessionID: slices.Clone(process.sessionID),
			limits: process.limits, definition: definition,
		}
	}
	if len(result) != 2 {
		t.Fatalf("%s fixture tool catalog = %v", process.language, process.catalog.Definitions())
	}
	return result
}

func (process *breadthPluginProcess) shutdown(t *testing.T) {
	t.Helper()
	if process.stopped {
		t.Fatal("runtime-plugin process stopped twice")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	drained, err := process.client.Drain(ctx, &pluginv1.DrainRequest{SessionId: slices.Clone(process.sessionID)})
	if err != nil {
		t.Fatalf("drain %s runtime-plugin fixture: %v", process.language, err)
	}
	if err = pluginv1.ValidateDrainResponse(drained, process.limits); err != nil {
		t.Fatalf("validate %s runtime-plugin drain: %v", process.language, err)
	}
	response, err := process.client.Shutdown(ctx, &pluginv1.ShutdownRequest{SessionId: slices.Clone(process.sessionID)})
	if err != nil {
		t.Fatalf("shutdown %s runtime-plugin fixture: %v", process.language, err)
	}
	if err = pluginv1.ValidateShutdownResponse(response, process.limits); err != nil {
		t.Fatalf("validate %s runtime-plugin shutdown: %v", process.language, err)
	}
	select {
	case <-ctx.Done():
		t.Fatalf("%s runtime-plugin process did not exit after shutdown: %v", process.language, ctx.Err())
	case <-process.wait():
		if process.waitErr != nil {
			t.Fatalf("%s runtime-plugin process exit: %v; stderr=%q", process.language, process.waitErr, process.stderr.String())
		}
	}
	process.stopped = true
	tail := <-process.stdoutTail
	if tail.err != nil && !errors.Is(tail.err, os.ErrClosed) {
		t.Fatal(tail.err)
	}
	if len(tail.value) != 0 {
		t.Fatalf("%s fixture contaminated stdout after readiness: %q", process.language, tail.value)
	}
	if strings.Contains(process.stderr.String(), process.secretEncoded) ||
		strings.Contains(strings.Join(process.command.Args, "\x00"), process.secretEncoded) {
		t.Fatal("runtime-plugin fixture exposed its launch secret")
	}
}

func (process *breadthPluginProcess) wait() <-chan struct{} {
	process.waitOnce.Do(func() {
		process.waitDone = make(chan struct{})
		go func() {
			process.waitErr = process.command.Wait()
			close(process.waitDone)
		}()
	})
	return process.waitDone
}

func (process *breadthPluginProcess) cleanup() {
	if process.stopped {
		return
	}
	if process.client != nil && len(process.sessionID) != 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		drained, drainErr := process.client.Drain(ctx, &pluginv1.DrainRequest{SessionId: slices.Clone(process.sessionID)})
		if drainErr == nil && pluginv1.ValidateDrainResponse(drained, process.limits) == nil {
			shutdown, shutdownErr := process.client.Shutdown(ctx, &pluginv1.ShutdownRequest{SessionId: slices.Clone(process.sessionID)})
			if shutdownErr == nil && pluginv1.ValidateShutdownResponse(shutdown, process.limits) == nil {
				select {
				case <-process.wait():
					process.stopped = true
					cancel()
					return
				case <-ctx.Done():
				}
			}
		}
		cancel()
	}
	_ = process.command.Process.Kill()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-process.wait():
		process.stopped = true
	case <-timer.C:
	}
}

func (process *breadthPluginProcess) close(t *testing.T) {
	t.Helper()
	if !process.stopped {
		t.Fatal("runtime-plugin process was not joined")
	}
	if process.connection != nil {
		if err := process.connection.Close(); err != nil {
			t.Fatal(err)
		}
		process.connection = nil
	}
}

type breadthRemoteTool struct {
	client     pluginv1.PluginServiceClient
	sessionID  []byte
	limits     *pluginv1.Limits
	definition tool.Definition
}

func (implementation *breadthRemoteTool) Definition() tool.Definition {
	if implementation == nil {
		return tool.Definition{}
	}
	return implementation.definition.Clone()
}

func (implementation *breadthRemoteTool) Execute(
	ctx context.Context,
	call tool.Call,
	reporter tool.Reporter,
) (tool.Result, error) {
	if implementation == nil || implementation.client == nil || ctx == nil ||
		call.Validate() != nil || call.Name() != implementation.definition.Name() {
		return breadthExecutionFailure(call.ID(), context.Canceled)
	}
	request := &pluginv1.ExecuteRequest{
		SessionId: slices.Clone(implementation.sessionID), CallId: string(call.ID()),
		ToolName: call.Name(), ArgumentsJson: call.Arguments(),
	}
	validator, err := pluginv1.NewStreamValidator(request, implementation.sessionID, implementation.limits)
	if err != nil {
		return breadthExecutionFailure(call.ID(), errors.New("runtime plugin request validation failed"))
	}
	stream, err := implementation.client.Execute(ctx, request)
	if err != nil {
		return breadthExecutionFailure(call.ID(), breadthExecutionCause(ctx))
	}
	for {
		response, receiveErr := stream.Recv()
		if receiveErr != nil {
			return breadthExecutionFailure(call.ID(), breadthExecutionCause(ctx))
		}
		frame, validationErr := validator.Accept(response)
		if validationErr != nil {
			return breadthExecutionFailure(call.ID(), errors.New("runtime plugin response validation failed"))
		}
		switch frame.Kind() {
		case pluginv1.FrameProgress:
			progress, _ := frame.Progress()
			if reporter != nil {
				if reportErr := reporter.Report(ctx, progress); reportErr != nil {
					return breadthExecutionFailure(call.ID(), breadthExecutionCause(ctx))
				}
			}
		case pluginv1.FrameResult:
			result, _ := frame.Result()
			if finishErr := finishBreadthPluginStream(stream, validator); finishErr != nil {
				return breadthExecutionFailure(call.ID(), finishErr)
			}
			return result, nil
		case pluginv1.FrameFailure:
			failure, _ := frame.Failure()
			if finishErr := finishBreadthPluginStream(stream, validator); finishErr != nil {
				return breadthExecutionFailure(call.ID(), finishErr)
			}
			return tool.Result{}, failure
		default:
			return breadthExecutionFailure(call.ID(), errors.New("runtime plugin response kind is invalid"))
		}
	}
}

func finishBreadthPluginStream(
	stream grpc.ServerStreamingClient[pluginv1.ExecuteResponse],
	validator *pluginv1.StreamValidator,
) error {
	if err := validator.Finish(); err != nil {
		return errors.New("runtime plugin terminal validation failed")
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		return errors.New("runtime plugin sent traffic after its terminal frame")
	}
	return nil
}

func breadthExecutionCause(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New("runtime plugin process is unavailable")
}

func breadthExecutionFailure(callID tool.CallID, cause error) (tool.Result, error) {
	failure, err := tool.NewExecutionError(callID, tool.ExecutionDefinitive, tool.RetryAllowed, cause)
	if err != nil {
		return tool.Result{}, errors.New("runtime plugin execution failure is invalid")
	}
	return tool.Result{}, failure
}

type breadthPlanSource struct {
	id         stage.PlanID
	dispatcher stage.ToolDispatcher
	acquired   atomic.Int32
	released   atomic.Int32
	active     atomic.Int32
}

func newBreadthPlanSource(
	t *testing.T,
	language string,
	engineMinor uint32,
	tools map[string]tool.Tool,
) *breadthPlanSource {
	t.Helper()
	dispatcher, err := stage.NewDispatcher(tools)
	if err != nil {
		t.Fatal(err)
	}
	id, err := stage.NewPlanID(fmt.Sprintf("plugin-breadth:%s:engine-1.%d:plugin-1.0", language, engineMinor))
	if err != nil {
		t.Fatal(err)
	}
	return &breadthPlanSource{id: id, dispatcher: dispatcher}
}

func (source *breadthPlanSource) LeaseCurrent(ctx context.Context) (*stage.ToolPlanLease, error) {
	if source == nil || ctx == nil {
		return nil, errors.New("runtime-plugin breadth plan source is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source.acquired.Add(1)
	source.active.Add(1)
	lease, err := stage.NewToolPlanLease(source.id, source.dispatcher, func() error {
		source.active.Add(-1)
		source.released.Add(1)
		return nil
	})
	if err != nil {
		source.active.Add(-1)
		return nil, err
	}
	return lease, nil
}

func (source *breadthPlanSource) LeaseGeneration(
	ctx context.Context,
	id stage.PlanID,
) (*stage.ToolPlanLease, error) {
	if source == nil || id != source.id {
		return nil, errors.New("runtime-plugin breadth generation is unavailable")
	}
	return source.LeaseCurrent(ctx)
}

type breadthProvider struct {
	mu      sync.Mutex
	results []string
}

func (provider *breadthProvider) Stream(ctx context.Context, request model.Request) (model.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, current := range request.Messages() {
		for _, part := range current.Parts() {
			if part.Kind() != message.PartToolResult {
				continue
			}
			value := string(part.Data())
			provider.mu.Lock()
			provider.results = append(provider.results, value)
			provider.mu.Unlock()
			delta, err := model.TextDelta(value)
			if err != nil {
				return nil, err
			}
			completed, err := model.Completed(model.NewUsage(1, 1))
			if err != nil {
				return nil, err
			}
			return &breadthModelStream{events: []model.StreamEvent{delta, completed}}, nil
		}
	}
	input := breadthUserInput(request.Messages())
	name, arguments := "conformance.echo", json.RawMessage(`{"value":"hello"}`)
	if input == breadthWaitInput {
		name, arguments = "conformance.wait", json.RawMessage(`{}`)
	}
	call, err := tool.NewCall(tool.CallID("call-"+strings.Split(input, " ")[0]), name, arguments)
	if err != nil {
		return nil, err
	}
	callEvent, err := model.ToolCallEvent(call)
	if err != nil {
		return nil, err
	}
	completed, err := model.Completed(model.NewUsage(1, 1))
	if err != nil {
		return nil, err
	}
	return &breadthModelStream{events: []model.StreamEvent{callEvent, completed}}, nil
}

func (provider *breadthProvider) resultsSnapshot() []string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return slices.Clone(provider.results)
}

func breadthUserInput(messages []message.Message) string {
	for _, current := range slices.Backward(messages) {
		if current.Role() != message.RoleUser {
			continue
		}
		for _, part := range current.Parts() {
			if value, ok := part.TextValue(); ok {
				return value
			}
		}
	}
	return breadthLossInput
}

type breadthModelStream struct {
	events []model.StreamEvent
	next   int
}

func (stream *breadthModelStream) Recv(ctx context.Context) (model.StreamEvent, error) {
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

func (*breadthModelStream) Close() error { return nil }

type breadthEngineHarness struct {
	rootCancel context.CancelFunc
	provider   *breadthProvider
	source     *breadthPlanSource
	host       *daemon.RunHost
	server     *grpcserver.Server
	listener   net.Listener
	serveDone  chan error
	connection *grpc.ClientConn
	session    client.Session
	definition daemon.Definition
	closed     bool
}

func newBreadthEngineHarness(
	t *testing.T,
	language string,
	engineMinor uint32,
	source *breadthPlanSource,
) *breadthEngineHarness {
	t.Helper()
	root, cancelRoot := context.WithCancel(context.Background())
	pending, err := daemon.NewPendingHub(daemon.DefaultPendingLimits())
	if err != nil {
		t.Fatal(err)
	}
	provider := &breadthProvider{}
	options := agent.DefaultEngineOptions()
	options.SnapshotCompatibilityIdentity = "plugin-breadth:v1"
	options.WorkspaceFingerprint = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	engine, err := agent.NewEngineWithToolPlanSourceAndInteractionBroker(
		provider, source, pending, &agent.AtomicIDSource{}, time.Now, nil, nil, options,
	)
	if err != nil {
		t.Fatal(err)
	}
	agentDefinition, err := agent.NewDefinition("plugin-breadth", "scripted", 2)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := daemon.NewDefinition("plugin-breadth", "revision-1", agentDefinition)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := daemon.NewDefinitionSet([]daemon.Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := daemon.NewSessionStore(root, 4)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := daemon.NewLedger(8, 128)
	if err != nil {
		t.Fatal(err)
	}
	limits, err := client.NewLimits(2<<20, 256, 128, 2<<20, 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := daemon.NewRunAuthority(daemon.RunAuthorityConfig{
		Directory: filepath.Join(breadthSecureRoot(t), "authority"),
	})
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
	token, err := endpoint.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	build, err := client.NewBuild("plugin-breadth-server", "source", "test", runtime.Version())
	if err != nil {
		t.Fatal(err)
	}
	server, err := grpcserver.NewServer(grpcserver.ServerConfig{
		Root: root, EndpointToken: token, Host: host, Sessions: sessions,
		Build: build, Capabilities: []string{"events"}, MaximumSessions: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	address := fixtureAddress(t)
	listener, err := localipc.Listen(address)
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	connection, err := grpc.NewClient(
		"passthrough:///spice-agent-plugin-breadth-engine",
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
		t.Fatal(err)
	}
	initialize := breadthInitializeRequest(t, language, engineMinor, limits)
	session, err := connector.Initialize(t.Context(), initialize)
	if err != nil {
		t.Fatal(err)
	}
	if got := session.Connection().Protocol().Minor(); got != engineMinor {
		t.Fatalf("engine protocol minor = %d, want %d", got, engineMinor)
	}
	return &breadthEngineHarness{
		rootCancel: cancelRoot, provider: provider, source: source, host: host,
		server: server, listener: listener, serveDone: serveDone,
		connection: connection, session: session, definition: definition,
	}
}

func breadthInitializeRequest(
	t *testing.T,
	language string,
	minor uint32,
	limits client.Limits,
) client.InitializeRequest {
	t.Helper()
	version, err := client.NewProtocolVersion(1, minor, 0)
	if err != nil {
		t.Fatal(err)
	}
	protocol, err := client.NewProtocolRange(version, version)
	if err != nil {
		t.Fatal(err)
	}
	build, err := client.NewBuild("plugin-breadth-"+language, "source", "test", runtime.Version())
	if err != nil {
		t.Fatal(err)
	}
	if minor == 2 {
		request, requestErr := client.NewLegacyInitializeRequest(
			protocol, build, []string{"events"}, []string{"events"}, limits,
		)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return request
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

func (harness *breadthEngineHarness) run(
	t *testing.T,
	operationName,
	inputText string,
	onEvent func(client.Event),
) []client.Event {
	t.Helper()
	operation, err := client.NewOperationID("plugin-breadth-" + operationName)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := client.NewDefinitionRef(harness.definition.ID(), harness.definition.Revision())
	if err != nil {
		t.Fatal(err)
	}
	input, err := client.NewInput("message-"+operationName, inputText)
	if err != nil {
		t.Fatal(err)
	}
	request, err := client.NewStartRequest(operation, reference, input)
	if err != nil {
		t.Fatal(err)
	}
	started, err := harness.session.Start(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if started.PlanID() != harness.source.id.String() {
		t.Fatalf("run plan = %q, want %q", started.PlanID(), harness.source.id)
	}
	cursor, err := client.NewCursor(started.Run(), 0)
	if err != nil {
		t.Fatal(err)
	}
	streamOptions, err := client.NewEventStreamOptions(128, true, harness.session.Connection().Limits())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stream, err := harness.session.Events(ctx, cursor, streamOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close() //nolint:errcheck // local stream close does not change the terminal event.
	var events []client.Event
	var sequence uint64
	for {
		frame, nextErr := stream.Next(ctx)
		if nextErr != nil {
			t.Fatalf("runtime-plugin engine event stream ended before terminal: %v", nextErr)
		}
		value, ok := frame.Event()
		if !ok {
			continue
		}
		if value.Sequence() != sequence+1 {
			t.Fatalf("event sequence = %d after %d", value.Sequence(), sequence)
		}
		sequence = value.Sequence()
		events = append(events, value)
		if onEvent != nil {
			onEvent(value)
		}
		switch value.Kind() {
		case client.EventRunCompleted, client.EventRunFailed, client.EventRunCancelled:
			return events
		}
	}
}

func (harness *breadthEngineHarness) waitForReleasedLeases(t *testing.T, want int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for harness.source.released.Load() != want || harness.source.active.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("tool-plan leases acquired/released/active = %d/%d/%d, want %d/%d/0",
				harness.source.acquired.Load(), harness.source.released.Load(), harness.source.active.Load(), want, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func (harness *breadthEngineHarness) close(t *testing.T) {
	t.Helper()
	if harness.closed {
		t.Fatal("engine breadth harness closed twice")
	}
	harness.closed = true
	if err := harness.session.Close(); err != nil {
		t.Error(err)
	}
	if err := harness.connection.Close(); err != nil {
		t.Error(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := harness.server.Shutdown(ctx); err != nil {
		t.Error(err)
	}
	if err := harness.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Error(err)
	}
	if err := <-harness.serveDone; err != nil && !errors.Is(err, grpc.ErrServerStopped) && !errors.Is(err, net.ErrClosed) {
		t.Error(err)
	}
	if err := harness.host.Shutdown(ctx); err != nil {
		t.Error(err)
	}
	harness.rootCancel()
}

func assertBreadthSuccess(t *testing.T, events []client.Event) {
	t.Helper()
	counts := breadthEventCounts(events)
	if counts[client.EventToolStarted] != 1 || counts[client.EventToolProgress] != 1 ||
		counts[client.EventToolCompleted] != 1 || counts[client.EventToolFailed] != 0 ||
		counts[client.EventRunCompleted] != 1 || counts[client.EventRunFailed] != 0 ||
		counts[client.EventRunCancelled] != 0 {
		t.Fatalf("successful runtime-plugin terminal counts = %v", counts)
	}
	var finalText string
	for _, value := range events {
		if value.Kind() == client.EventModelDelta {
			finalText, _ = value.Detail().Text()
		}
	}
	if finalText != `{"value":"hello"}` {
		t.Fatalf("successful runtime-plugin model continuation = %q", finalText)
	}
}

func assertBreadthCancellation(t *testing.T, events []client.Event) {
	t.Helper()
	counts := breadthEventCounts(events)
	if counts[client.EventToolStarted] != 1 || counts[client.EventToolProgress] != 1 ||
		counts[client.EventToolCompleted] != 0 || counts[client.EventToolFailed] != 1 ||
		counts[client.EventRunCompleted] != 0 || counts[client.EventRunFailed] != 0 ||
		counts[client.EventRunCancelled] != 1 {
		t.Fatalf("canceled runtime-plugin terminal counts = %v", counts)
	}
}

func assertBreadthProcessLoss(t *testing.T, events []client.Event) {
	t.Helper()
	counts := breadthEventCounts(events)
	if counts[client.EventToolStarted] != 1 || counts[client.EventToolProgress] != 0 ||
		counts[client.EventToolCompleted] != 0 || counts[client.EventToolFailed] != 1 ||
		counts[client.EventRunCompleted] != 0 || counts[client.EventRunFailed] != 1 ||
		counts[client.EventRunCancelled] != 0 {
		t.Fatalf("lost runtime-plugin terminal counts = %v", counts)
	}
	for _, value := range events {
		if value.Kind() != client.EventToolFailed {
			continue
		}
		terminal, ok := value.Detail().ToolTerminal()
		if !ok || terminal.Outcome() != client.ToolOutcomeDefinitive || terminal.Retry() != client.ToolRetryAllowed {
			t.Fatalf("lost runtime-plugin tool terminal = %#v", terminal)
		}
	}
}

func breadthEventCounts(events []client.Event) map[client.EventKind]int {
	result := make(map[client.EventKind]int)
	for _, value := range events {
		result[value.Kind()]++
	}
	return result
}

func breadthSecureRoot(t *testing.T) string {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(cache, "spice-agent-plugin-breadth-tests")
	if err = os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "engine-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

var (
	_ tool.Tool            = (*breadthRemoteTool)(nil)
	_ stage.ToolPlanSource = (*breadthPlanSource)(nil)
	_ model.Provider       = (*breadthProvider)(nil)
	_ model.Stream         = (*breadthModelStream)(nil)
)

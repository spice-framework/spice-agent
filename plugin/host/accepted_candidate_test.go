package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/process"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestAcceptedCandidateBuildsToolsAndClosesGracefully(t *testing.T) {
	owned := newLifecycleProcess()
	client := &lifecyclePluginClient{}
	client.drain = func(ctx context.Context, request *pluginv1.DrainRequest) (*pluginv1.DrainResponse, error) {
		assertLifecycleDeadline(t, ctx, 40*time.Millisecond)
		if !slices.Equal(request.GetSessionId(), lifecycleSessionID()) {
			t.Fatal("drain did not use the exact session identity")
		}
		return &pluginv1.DrainResponse{Status: commonv1.OKStatus()}, nil
	}
	client.shutdown = func(ctx context.Context, request *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResponse, error) {
		assertLifecycleDeadline(t, ctx, 70*time.Millisecond)
		if !slices.Equal(request.GetSessionId(), lifecycleSessionID()) {
			t.Fatal("shutdown did not use the exact session identity")
		}
		go func() {
			time.Sleep(10 * time.Millisecond)
			owned.finish()
		}()
		return &pluginv1.ShutdownResponse{Status: commonv1.OKStatus()}, nil
	}
	accepted, candidate, endpoint := newLifecycleAccepted(t, client, owned)

	tools := accepted.tools()
	if len(tools) != 1 || tools["remote.read"] == nil || accepted.toolSession() == nil {
		t.Fatal("accepted candidate did not expose its negotiated remote tools")
	}
	delete(tools, "remote.read")
	if len(accepted.tools()) != 1 {
		t.Fatal("caller mutated the accepted candidate tool map")
	}

	if err := accepted.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.drainCount() != 1 || client.shutdownCount() != 1 {
		t.Fatal("graceful lifecycle did not issue exactly one drain and shutdown")
	}
	if owned.forceCount() != 0 || owned.waitCount() != 1 {
		t.Fatal("contained graceful process was unnecessarily killed or not joined")
	}
	if !endpoint.isClosed() {
		t.Fatal("endpoint ownership was not released after containment")
	}
	candidate.mu.Lock()
	leaseReleased := candidate.lease == nil
	candidate.mu.Unlock()
	if !leaseReleased {
		t.Fatal("executable lease was not released after containment")
	}
	if err := accepted.close(context.Background()); err != nil ||
		client.drainCount() != 1 || client.shutdownCount() != 1 || owned.waitCount() != 1 {
		t.Fatal("accepted candidate close was not idempotent")
	}
}

func TestAcceptedCandidateDrainsLocalAdmissionBeforeRPC(t *testing.T) {
	owned := newLifecycleProcess()
	drainCalled := make(chan struct{})
	executeEntered := make(chan struct{})
	executeRelease := make(chan struct{})
	var receive int
	client := &lifecyclePluginClient{
		execute: func(ctx context.Context, _ *pluginv1.ExecuteRequest) (pluginv1.PluginService_ExecuteClient, error) {
			return &fakeExecuteStream{
				currentContext: func() context.Context { return ctx },
				receive: func() (*pluginv1.ExecuteResponse, error) {
					receive++
					if receive > 1 {
						return nil, io.EOF
					}
					close(executeEntered)
					<-executeRelease
					return remoteResult("call-1", 1, []byte(`{"ok":true}`), ""), nil
				},
			}, nil
		},
		drain: func(context.Context, *pluginv1.DrainRequest) (*pluginv1.DrainResponse, error) {
			close(drainCalled)
			return &pluginv1.DrainResponse{Status: commonv1.OKStatus()}, nil
		},
		shutdown: func(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResponse, error) {
			owned.finish()
			return &pluginv1.ShutdownResponse{Status: commonv1.OKStatus()}, nil
		},
	}
	accepted, _, _ := newLifecycleAccepted(t, client, owned)
	call, err := tool.NewCall("call-1", "remote.read", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	executeResult := make(chan error, 1)
	go func() {
		_, executeErr := accepted.tools()["remote.read"].Execute(context.Background(), call, nil)
		executeResult <- executeErr
	}()
	select {
	case <-executeEntered:
	case <-time.After(time.Second):
		t.Fatal("remote execution did not enter")
	}
	result := make(chan error, 1)
	go func() { result <- accepted.close(context.Background()) }()
	waitForDraining(t, accepted.session)
	if err := accepted.session.acquire(context.Background()); err == nil {
		t.Fatal("new execution entered after drain began")
	}
	select {
	case <-drainCalled:
		t.Fatal("remote drain began while an admitted execution remained active")
	default:
	}
	close(executeRelease)
	select {
	case executeErr := <-executeResult:
		if executeErr != nil {
			t.Fatal(executeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("remote execution did not terminate")
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not continue after the active execution terminated")
	}
}

func TestAcceptedCandidateRejectsUnsafeLifecycleResponsesWithoutRetry(t *testing.T) {
	const privateText = "private plugin lifecycle text"
	tests := map[string]struct {
		drain    func(context.Context, *pluginv1.DrainRequest) (*pluginv1.DrainResponse, error)
		shutdown func(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResponse, error)
		wantDown int
	}{
		"drain transport": {
			drain: func(context.Context, *pluginv1.DrainRequest) (*pluginv1.DrainResponse, error) {
				return nil, status.Error(codes.Internal, privateText)
			},
		},
		"drain active calls": {
			drain: func(context.Context, *pluginv1.DrainRequest) (*pluginv1.DrainResponse, error) {
				return &pluginv1.DrainResponse{Status: commonv1.OKStatus(), ActiveCalls: 1}, nil
			},
		},
		"drain status": {
			drain: func(context.Context, *pluginv1.DrainRequest) (*pluginv1.DrainResponse, error) {
				return &pluginv1.DrainResponse{Status: &commonv1.Status{
					Code: commonv1.ErrorCode_ERROR_CODE_INTERNAL, Message: privateText,
				}}, nil
			},
		},
		"shutdown malformed": {
			drain: lifecycleDrainOK,
			shutdown: func(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResponse, error) {
				return &pluginv1.ShutdownResponse{}, nil
			},
			wantDown: 1,
		},
		"shutdown transport": {
			drain: lifecycleDrainOK,
			shutdown: func(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResponse, error) {
				return nil, status.Error(codes.Internal, privateText)
			},
			wantDown: 1,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			owned := newLifecycleProcess()
			client := &lifecyclePluginClient{drain: test.drain, shutdown: test.shutdown}
			accepted, _, endpoint := newLifecycleAccepted(t, client, owned)
			first := accepted.close(context.Background())
			second := accepted.close(context.Background())
			if first == nil || second == nil || client.drainCount() != 1 ||
				client.shutdownCount() != test.wantDown || owned.forceCount() != 1 || !endpoint.isClosed() {
				t.Fatal("failed lifecycle was retried or not deterministically contained")
			}
			encoded, marshalErr := json.Marshal(first)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			rendered := fmt.Sprintf("%v|%+v|%#v|%s", first, first, first, encoded)
			if strings.Contains(rendered, privateText) {
				t.Fatalf("plugin-controlled lifecycle text leaked: %s", rendered)
			}
		})
	}
}

func TestAcceptedCandidateCancellationRetainsOwnershipForContainmentRetry(t *testing.T) {
	owned := newLifecycleProcess()
	client := &lifecyclePluginClient{
		drain: func(ctx context.Context, _ *pluginv1.DrainRequest) (*pluginv1.DrainResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	accepted, candidate, endpoint := newLifecycleAccepted(t, client, owned)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := accepted.close(canceled); err == nil {
		t.Fatal("canceled lifecycle succeeded")
	}
	if endpoint.isClosed() {
		t.Fatal("endpoint was released before containment")
	}
	candidate.mu.Lock()
	retained := candidate.lease != nil && candidate.process != nil && !candidate.closed
	candidate.mu.Unlock()
	if !retained {
		t.Fatal("candidate did not retain executable and process ownership")
	}
	if err := accepted.close(context.Background()); err == nil {
		t.Fatal("the original lifecycle failure was lost after containment")
	}
	if !endpoint.isClosed() || client.drainCount() != 1 || client.shutdownCount() != 0 || owned.forceCount() != 1 {
		t.Fatal("containment retry did not perform the first drain attempt or release ownership")
	}
}

func TestAcceptedCandidateRetainsEndpointAndLeaseUntilWaitProvesContainment(t *testing.T) {
	owned := newLifecycleProcess()
	owned.waitFailures = 1
	client := &lifecyclePluginClient{drain: lifecycleDrainOK}
	client.shutdown = func(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResponse, error) {
		return &pluginv1.ShutdownResponse{Status: commonv1.OKStatus()}, nil
	}
	accepted, candidate, endpoint := newLifecycleAccepted(t, client, owned)
	if err := accepted.close(context.Background()); err == nil {
		t.Fatal("uncontained lifecycle succeeded")
	}
	candidate.mu.Lock()
	retained := candidate.endpoint != nil && candidate.lease != nil && candidate.process != nil && !candidate.closed
	candidate.mu.Unlock()
	if !retained || endpoint.isClosed() {
		t.Fatal("endpoint or executable lease was released before containment")
	}
	if err := accepted.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !endpoint.isClosed() || client.drainCount() != 1 || client.shutdownCount() != 1 || owned.waitCount() != 2 {
		t.Fatal("containment retry was not isolated from graceful protocol work")
	}
}

func TestAcceptedCandidateCachesTerminalEndpointReleaseFailure(t *testing.T) {
	owned := newLifecycleProcess()
	client := &lifecyclePluginClient{drain: lifecycleDrainOK}
	client.shutdown = func(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResponse, error) {
		owned.finish()
		return &pluginv1.ShutdownResponse{Status: commonv1.OKStatus()}, nil
	}
	accepted, candidate, endpoint := newLifecycleAccepted(t, client, owned)
	endpoint.mu.Lock()
	endpoint.closeFailures = 1
	endpoint.mu.Unlock()
	first := accepted.close(context.Background())
	if first == nil {
		t.Fatal("failed endpoint release reported terminal success")
	}
	candidate.mu.Lock()
	released := candidate.endpoint == nil && candidate.lease == nil && candidate.closed
	candidate.mu.Unlock()
	if !released || endpoint.isClosed() {
		t.Fatal("terminal endpoint refusal retained unusable ownership")
	}
	second := accepted.close(context.Background())
	if second == nil || !errors.Is(second, first) {
		t.Fatal("terminal endpoint release failure was not cached")
	}
	if endpoint.closeCount() != 1 || client.drainCount() != 1 || client.shutdownCount() != 1 || owned.waitCount() != 1 {
		t.Fatal("terminal endpoint release was retried")
	}
}

func TestAcceptedCandidateObservesProcessAndConnectionFailure(t *testing.T) {
	for _, test := range []struct {
		name string
		fail func(*candidate, *lifecycleProcess)
	}{
		{"process", func(_ *candidate, owned *lifecycleProcess) { owned.finish() }},
		{"connection", func(candidate *candidate, _ *lifecycleProcess) { _ = candidate.connection.Close() }},
		{"stdout", func(candidate *candidate, _ *lifecycleProcess) {
			_, _ = candidate.stdout.Write([]byte("unexpected output"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			owned := newLifecycleProcess()
			client := &lifecyclePluginClient{}
			accepted, candidate, _ := newLifecycleAccepted(t, client, owned)
			test.fail(candidate, owned)
			select {
			case <-accepted.done():
			case <-time.After(time.Second):
				t.Fatal("accepted candidate did not report terminal health failure")
			}
			if accepted.healthFailure() == nil {
				t.Fatal("health signal had no safe failure")
			}
			if err := accepted.close(context.Background()); err == nil || client.drainCount() != 0 {
				t.Fatal("unhealthy candidate entered remote lifecycle")
			}
		})
	}
}

func TestAcceptedCandidateDetectsIncompleteHealthOwnership(t *testing.T) {
	accepted := &acceptedCandidate{unhealthy: make(chan struct{})}
	failure := accepted.currentHealthFailure()
	if failure == nil || accepted.healthFailure() == nil {
		t.Fatal("incomplete candidate health ownership was not retained as unhealthy")
	}
	select {
	case <-accepted.done():
	default:
		t.Fatal("incomplete candidate health ownership did not close its health signal")
	}
}

func TestLifecycleErrorsRedactPluginControlledCauses(t *testing.T) {
	const privateText = "private plugin lifecycle detail"
	cause := errors.New(privateText)
	for _, phase := range []lifecyclePhase{
		lifecyclePhaseAccept,
		lifecyclePhaseHealth,
		lifecyclePhaseDrain,
		lifecyclePhaseShutdown,
		lifecyclePhaseContainment,
		lifecyclePhase("invalid-private-phase"),
	} {
		failure := lifecycleFailure(phase, cause)
		if failure == nil || !errors.Is(failure, cause) {
			t.Fatalf("phase %q did not preserve its programmatic cause", phase)
		}
		encoded, err := json.Marshal(failure)
		if err != nil {
			t.Fatal(err)
		}
		rendered := fmt.Sprintf("%v|%+v|%#v|%s", failure, failure, failure, encoded)
		if strings.Contains(rendered, privateText) || strings.Contains(rendered, "invalid-private-phase") {
			t.Fatalf("phase %q leaked plugin-controlled detail: %s", phase, rendered)
		}
	}

	closeFailure := lifecycleCloseFailure(cause)
	if closeFailure == nil || !errors.Is(closeFailure, cause) {
		t.Fatal("graceful lifecycle failure did not preserve its programmatic cause")
	}
	encoded, err := json.Marshal(closeFailure)
	if err != nil {
		t.Fatal(err)
	}
	rendered := fmt.Sprintf("%v|%+v|%#v|%s", closeFailure, closeFailure, closeFailure, encoded)
	if strings.Contains(rendered, privateText) {
		t.Fatalf("graceful lifecycle failure leaked plugin-controlled detail: %s", rendered)
	}

	if lifecycleFailure(lifecyclePhaseDrain, nil) != nil || lifecycleCloseFailure(nil) != nil {
		t.Fatal("nil lifecycle causes produced failures")
	}
	var absentLifecycle *lifecycleError
	if absentLifecycle.Unwrap() != nil || strings.Contains(absentLifecycle.Error(), privateText) {
		t.Fatal("nil lifecycle error exposed a cause")
	}
	var absentClose *lifecycleCloseError
	if absentClose.Unwrap() != nil {
		t.Fatal("nil graceful lifecycle error exposed a cause")
	}
}

func TestAcceptedCandidateFormattingNeverExposesOwnership(t *testing.T) {
	accepted := &acceptedCandidate{
		candidate: &candidate{session: []byte("private-session")},
		toolSet: map[string]tool.Tool{
			"private-tool": newHostTestTool(t, "private-tool", "private-result"),
		},
	}
	encoded, err := json.Marshal(accepted)
	if err != nil {
		t.Fatal(err)
	}
	rendered := fmt.Sprintf(
		"%s|%s|%v|%+v|%#v|%s",
		accepted.String(),
		accepted.GoString(),
		accepted,
		accepted,
		accepted,
		encoded,
	)
	for _, private := range []string{"private-session", "private-tool", "private-result"} {
		if strings.Contains(rendered, private) {
			t.Fatalf("accepted candidate formatting leaked %q: %s", private, rendered)
		}
	}
	if !strings.Contains(rendered, "[REDACTED]") {
		t.Fatalf("accepted candidate formatting did not identify redaction: %s", rendered)
	}
}

func TestAcceptedCandidateRejectsInvalidOwnership(t *testing.T) {
	if accepted, err := newAcceptedCandidate(nil); accepted != nil || err == nil {
		t.Fatal("nil candidate ownership succeeded")
	}
	if accepted, err := newAcceptedCandidate(&candidate{}); accepted != nil || err == nil {
		t.Fatal("incomplete candidate ownership succeeded")
	}
	var absent *acceptedCandidate
	if absent.tools() != nil || absent.toolSession() != nil {
		t.Fatal("nil accepted candidate exposed tools")
	}
	select {
	case <-absent.done():
	default:
		t.Fatal("nil accepted candidate health did not terminate")
	}
	if err := absent.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := (&acceptedCandidate{}).close(context.Background()); err == nil {
		t.Fatal("incomplete accepted candidate close succeeded")
	}
}

func newLifecycleAccepted(
	t *testing.T,
	client *lifecyclePluginClient,
	owned *lifecycleProcess,
) (*acceptedCandidate, *candidate, *lifecycleEndpoint) {
	t.Helper()
	executable := testExecutable(t, "lifecycle-fixture", nil)
	executable.drainTimeout = 40 * time.Millisecond
	executable.shutdownTimeout = 70 * time.Millisecond
	executable.containmentTimeout = 100 * time.Millisecond
	lease, err := openVerifiedExecutable(context.Background(), executable)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := grpc.NewClient(
		"passthrough:///spice-agent-lifecycle-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	definition := remoteDefinition(t, tool.EffectReadOnly)
	catalog, err := pluginv1.NewCatalog("lifecycle-fixture", "v1", []tool.Definition{definition}, validHostLimits())
	if err != nil {
		t.Fatal(err)
	}
	stdout := newReadinessSink()
	if _, err = stdout.Write([]byte(pluginv1.ReadinessRecord)); err != nil {
		t.Fatal(err)
	}
	endpoint := &lifecycleEndpoint{}
	candidate := &candidate{
		executable: executable,
		lease:      lease,
		endpoint:   endpoint,
		process:    owned,
		connection: connection,
		client:     client,
		stdout:     stdout,
		stderr:     newStderrSink(),
		catalog:    catalog,
		limits:     validHostLimits(),
		session:    lifecycleSessionID(),
	}
	accepted, err := newAcceptedCandidate(candidate)
	if err != nil {
		_ = candidate.cleanup(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accepted.close(context.Background()) })
	return accepted, candidate, endpoint
}

func lifecycleSessionID() []byte { return bytes.Repeat([]byte{7}, pluginv1.SessionIDBytes) }

func lifecycleDrainOK(
	context.Context,
	*pluginv1.DrainRequest,
) (*pluginv1.DrainResponse, error) {
	return &pluginv1.DrainResponse{Status: commonv1.OKStatus()}, nil
}

func assertLifecycleDeadline(t *testing.T, ctx context.Context, maximum time.Duration) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	remaining := time.Until(deadline)
	if !ok || remaining <= 0 || remaining > maximum+10*time.Millisecond {
		t.Fatalf("lifecycle deadline remaining = %s, maximum = %s", remaining, maximum)
	}
}

func waitForDraining(t *testing.T, session *remoteSession) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		session.mu.Lock()
		draining := session.draining
		session.mu.Unlock()
		if draining {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("session did not begin draining")
}

type lifecyclePluginClient struct {
	mu        sync.Mutex
	drains    int
	shutdowns int
	execute   func(context.Context, *pluginv1.ExecuteRequest) (pluginv1.PluginService_ExecuteClient, error)
	drain     func(context.Context, *pluginv1.DrainRequest) (*pluginv1.DrainResponse, error)
	shutdown  func(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResponse, error)
}

func (*lifecyclePluginClient) Initialize(
	context.Context,
	*pluginv1.InitializeRequest,
	...grpc.CallOption,
) (*pluginv1.InitializeResponse, error) {
	return nil, errors.New("unexpected initialize")
}

func (client *lifecyclePluginClient) Execute(
	ctx context.Context,
	request *pluginv1.ExecuteRequest,
	_ ...grpc.CallOption,
) (pluginv1.PluginService_ExecuteClient, error) {
	if client.execute == nil {
		return nil, errors.New("unexpected execute")
	}
	return client.execute(ctx, request)
}

func (client *lifecyclePluginClient) Drain(
	ctx context.Context,
	request *pluginv1.DrainRequest,
	_ ...grpc.CallOption,
) (*pluginv1.DrainResponse, error) {
	client.mu.Lock()
	client.drains++
	operation := client.drain
	client.mu.Unlock()
	if operation == nil {
		return nil, errors.New("unexpected drain")
	}
	return operation(ctx, request)
}

func (client *lifecyclePluginClient) Shutdown(
	ctx context.Context,
	request *pluginv1.ShutdownRequest,
	_ ...grpc.CallOption,
) (*pluginv1.ShutdownResponse, error) {
	client.mu.Lock()
	client.shutdowns++
	operation := client.shutdown
	client.mu.Unlock()
	if operation == nil {
		return nil, errors.New("unexpected shutdown")
	}
	return operation(ctx, request)
}

func (client *lifecyclePluginClient) drainCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.drains
}

func (client *lifecyclePluginClient) shutdownCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.shutdowns
}

type lifecycleProcess struct {
	done         chan struct{}
	once         sync.Once
	mu           sync.Mutex
	forces       int
	waits        int
	waitFailures int
}

func newLifecycleProcess() *lifecycleProcess { return &lifecycleProcess{done: make(chan struct{})} }

func (owned *lifecycleProcess) Done() <-chan struct{}       { return owned.done }
func (*lifecycleProcess) Result() (process.Outcome, error)  { return process.NewExitedOutcome(0) }
func (*lifecycleProcess) RequestStop(context.Context) error { return nil }

func (owned *lifecycleProcess) ForceKill(ctx context.Context) error {
	owned.mu.Lock()
	owned.forces++
	owned.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	owned.finish()
	return nil
}

func (owned *lifecycleProcess) Wait(ctx context.Context) error {
	owned.mu.Lock()
	owned.waits++
	if owned.waitFailures > 0 {
		owned.waitFailures--
		owned.mu.Unlock()
		return errors.New("containment is not yet proved")
	}
	owned.mu.Unlock()
	select {
	case <-owned.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (owned *lifecycleProcess) finish() { owned.once.Do(func() { close(owned.done) }) }

func (owned *lifecycleProcess) forceCount() int {
	owned.mu.Lock()
	defer owned.mu.Unlock()
	return owned.forces
}

func (owned *lifecycleProcess) waitCount() int {
	owned.mu.Lock()
	defer owned.mu.Unlock()
	return owned.waits
}

type lifecycleEndpoint struct {
	mu            sync.Mutex
	closed        bool
	closeFailures int
	closes        int
}

func (*lifecycleEndpoint) Address() string { return "lifecycle-test" }
func (*lifecycleEndpoint) Dial(context.Context) (net.Conn, error) {
	return nil, errors.New("unexpected endpoint dial")
}

func (endpoint *lifecycleEndpoint) Close() error {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	endpoint.closes++
	if endpoint.closeFailures > 0 {
		endpoint.closeFailures--
		return errors.New("endpoint release failed")
	}
	endpoint.closed = true
	return nil
}

func (endpoint *lifecycleEndpoint) isClosed() bool {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	return endpoint.closed
}

func (endpoint *lifecycleEndpoint) closeCount() int {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	return endpoint.closes
}

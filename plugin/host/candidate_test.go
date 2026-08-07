package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/internal/pluginfixture"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/process"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

func TestCandidateLauncherAuthenticatesAndOwnsSuccessfulCandidate(t *testing.T) {
	t.Parallel()
	harness := newCandidateHarness(t)
	executable := testExecutable(t, "spice-agent-go-conformance", nil)
	launcher := testCandidateLauncher(t, harness, bytes.NewReader(nonzeroEntropy()))

	candidate, err := launcher.launch(context.Background(), executable)
	if err != nil {
		t.Fatal(err)
	}
	if candidate == nil || candidate.catalogSnapshot().Name() != "spice-agent-go-conformance" ||
		candidate.catalogSnapshot().Version() != "v1" || len(candidate.sessionID()) != pluginv1.SessionIDBytes {
		t.Fatal("authenticated candidate contract is incomplete")
	}
	if candidate.pluginBuild() == nil || candidate.selectedProtocol() == nil || candidate.negotiatedLimits() == nil {
		t.Fatal("negotiated candidate metadata is incomplete")
	}
	if len(candidate.launchIdentity()) != pluginv1.LaunchIDBytes ||
		len(harness.launchIdentity) != pluginv1.LaunchIDBytes*2 ||
		harness.launchIdentity != strings.ToLower(harness.launchIdentity) {
		t.Fatal("endpoint did not receive the canonical launch identity")
	}
	if !candidate.input.cleared() {
		t.Fatal("private bootstrap remained after readiness")
	}
	launchedSpec := harness.launchSpec()
	if len(launchedSpec.Arguments()) != 0 || !slices.Equal(launchedSpec.Environment(), executable.Environment()) {
		t.Fatal("launch changed arguments or exact environment")
	}
	if !slices.Contains(launchedSpec.Capabilities(), tool.CapabilityProcessExecute) {
		t.Fatal("launch omitted process execution capability")
	}
	encodedSecret := base64.RawURLEncoding.EncodeToString(harness.secret)
	if strings.Contains(strings.Join(launchedSpec.Arguments(), "\x00"), encodedSecret) ||
		strings.Contains(strings.Join(launchedSpec.Environment(), "\x00"), encodedSecret) {
		t.Fatal("launch secret escaped private stdin")
	}
	if err = candidate.cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !harness.endpoint.isClosed() || !harness.process.wasKilled() || !harness.process.wasWaited() {
		t.Fatal("candidate cleanup did not release all owned resources")
	}
	if err = candidate.cleanup(context.Background()); err != nil {
		t.Fatalf("idempotent cleanup: %v", err)
	}
}

func TestCandidateLauncherValidatesConstructorDependencies(t *testing.T) {
	t.Parallel()
	harness := newCandidateHarness(t)
	host := &pluginv1.BuildIdentity{
		Component: "host", Version: "v1", Commit: "test", Runtime: runtime.Version(),
	}
	for name, construct := range map[string]func() error{
		"process":  func() error { _, err := newCandidateLauncher(nil, harness, bytes.NewReader(nil), host); return err },
		"endpoint": func() error { _, err := newCandidateLauncher(harness, nil, bytes.NewReader(nil), host); return err },
		"entropy":  func() error { _, err := newCandidateLauncher(harness, harness, nil, host); return err },
		"host":     func() error { _, err := newCandidateLauncher(harness, harness, bytes.NewReader(nil), nil); return err },
		"invalid host": func() error {
			_, err := newCandidateLauncher(harness, harness, bytes.NewReader(nil), &pluginv1.BuildIdentity{})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var failure *launchError
			if err := construct(); !errors.As(err, &failure) || failure.phaseName() != launchPhaseValidate {
				t.Fatalf("error = %T %v", err, err)
			}
		})
	}
}

func TestCandidateLauncherReturnsOwnershipAcrossFailurePhases(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		configure func(*candidateHarness)
		entropy   io.Reader
		phase     launchPhase
		process   bool
	}{
		"entropy": {
			entropy: io.LimitReader(bytes.NewReader([]byte{1}), 1), phase: launchPhaseRandom,
		},
		"endpoint with ownership": {
			configure: func(value *candidateHarness) { value.endpointError = errors.New("private endpoint") },
			entropy:   bytes.NewReader(nonzeroEntropy()), phase: launchPhaseEndpoint,
		},
		"endpoint without ownership": {
			configure: func(value *candidateHarness) { value.endpointNil = true },
			entropy:   bytes.NewReader(nonzeroEntropy()), phase: launchPhaseEndpoint,
		},
		"start without ownership": {
			configure: func(value *candidateHarness) { value.nilProcess = true },
			entropy:   bytes.NewReader(nonzeroEntropy()), phase: launchPhaseStart,
		},
		"start with ownership": {
			configure: func(value *candidateHarness) { value.startError = errors.New("private start") },
			entropy:   bytes.NewReader(nonzeroEntropy()), phase: launchPhaseStart, process: true,
		},
		"exit before readiness": {
			configure: func(value *candidateHarness) { value.exitBeforeReady = true },
			entropy:   bytes.NewReader(nonzeroEntropy()), phase: launchPhaseReadiness, process: true,
		},
		"stdout contamination": {
			configure: func(value *candidateHarness) { value.stdoutContamination = true },
			entropy:   bytes.NewReader(nonzeroEntropy()), phase: launchPhaseReadiness, process: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			harness := newCandidateHarness(t)
			if test.configure != nil {
				test.configure(harness)
			}
			launcher := testCandidateLauncher(t, harness, test.entropy)
			candidate, err := launcher.launch(context.Background(),
				testExecutable(t, "spice-agent-go-conformance", nil))
			if candidate == nil {
				t.Fatal("ownership was lost on failure")
			}
			var failure *launchError
			if !errors.As(err, &failure) || failure.phaseName() != test.phase {
				t.Fatalf("failure = %T %v, want phase %q", err, err, test.phase)
			}
			if err = candidate.cleanup(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !harness.endpoint.isClosed() && test.phase != launchPhaseRandom && !harness.endpointNil {
				t.Fatal("owned endpoint was not closed")
			}
			if test.process && !harness.process.wasWaited() {
				t.Fatal("owned process was not contained")
			}
		})
	}
}

func TestCandidateRejectsAuthenticatedManifestAndCapabilityMismatch(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		manifestName string
		approved     []tool.Capability
	}{
		"manifest":   {manifestName: "different"},
		"capability": {manifestName: "fixture.capability"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			harness := newCandidateHarness(t)
			harness.service = func(secret []byte) (pluginv1.PluginServiceServer, error) {
				return newSignedService(t, secret, test.manifestName,
					[]tool.Capability{tool.CapabilityNetworkAccess})
			}
			launcher := testCandidateLauncher(t, harness, bytes.NewReader(nonzeroEntropy()))
			candidate, err := launcher.launch(context.Background(),
				testExecutable(t, "fixture.capability", test.approved))
			var failure *launchError
			if candidate == nil || !errors.As(err, &failure) || failure.phaseName() != launchPhaseManifest {
				t.Fatalf("candidate=%v error=%T %v", candidate, err, err)
			}
			if cleanupErr := candidate.cleanup(context.Background()); cleanupErr != nil {
				t.Fatal(cleanupErr)
			}
		})
	}
}

func TestCandidateRejectsTamperedHandshake(t *testing.T) {
	t.Parallel()
	harness := newCandidateHarness(t)
	harness.service = func(secret []byte) (pluginv1.PluginServiceServer, error) {
		service, err := newSignedService(t, secret, "fixture.tamper", nil)
		if err == nil {
			service.tamperProof = true
		}
		return service, err
	}
	launcher := testCandidateLauncher(t, harness, bytes.NewReader(nonzeroEntropy()))
	candidate, err := launcher.launch(context.Background(),
		testExecutable(t, "fixture.tamper", nil))
	var failure *launchError
	if candidate == nil || !errors.As(err, &failure) || failure.phaseName() != launchPhaseInitialize {
		t.Fatalf("candidate=%v error=%T %v", candidate, err, err)
	}
	if cleanupErr := candidate.cleanup(context.Background()); cleanupErr != nil {
		t.Fatal(cleanupErr)
	}
}

func TestCandidateInitializationCancellationRetainsOwnership(t *testing.T) {
	t.Parallel()
	harness := newCandidateHarness(t)
	entered := make(chan struct{})
	harness.service = func([]byte) (pluginv1.PluginServiceServer, error) {
		return blockingInitializeService{entered: entered}, nil
	}
	launcher := testCandidateLauncher(t, harness, bytes.NewReader(nonzeroEntropy()))
	executable := testExecutable(t, "unused", nil)
	executable.startupTimeout = 10 * time.Second
	operation, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		candidate *candidate
		err       error
	}
	completed := make(chan result, 1)
	go func() {
		owned, launchErr := launcher.launch(operation, executable)
		completed <- result{candidate: owned, err: launchErr}
	}()
	select {
	case <-entered:
		cancel()
	case early := <-completed:
		if early.candidate != nil {
			_ = early.candidate.cleanup(context.Background())
		}
		t.Fatalf("Initialize was not entered: %v", early.err)
	case <-time.After(10 * time.Second):
		cancel()
		timedOut := <-completed
		if timedOut.candidate != nil {
			_ = timedOut.candidate.cleanup(context.Background())
		}
		t.Fatal("Initialize was not entered before the test deadline")
	}
	finished := <-completed
	candidate, err := finished.candidate, finished.err
	var failure *launchError
	if candidate == nil || !errors.As(err, &failure) || failure.phaseName() != launchPhaseInitialize ||
		!errors.Is(err, context.Canceled) {
		t.Fatalf("candidate=%v error=%T %v", candidate, err, err)
	}
	if cleanupErr := candidate.cleanup(context.Background()); cleanupErr != nil {
		t.Fatal(cleanupErr)
	}
	if !harness.endpoint.isClosed() || !harness.process.wasWaited() {
		t.Fatal("canceled initialization lost candidate ownership")
	}
}

func TestCandidateCleanupRetainsEndpointUntilContainment(t *testing.T) {
	t.Parallel()
	harness := newCandidateHarness(t)
	harness.startError = errors.New("private start")
	harness.process.waitError = errors.New("private wait")
	launcher := testCandidateLauncher(t, harness, bytes.NewReader(nonzeroEntropy()))
	candidate, launchErr := launcher.launch(context.Background(),
		testExecutable(t, "spice-agent-go-conformance", nil))
	if launchErr == nil || candidate == nil {
		t.Fatal("expected owned launch failure")
	}
	if err := candidate.cleanup(context.Background()); err == nil {
		t.Fatal("expected containment failure")
	}
	if harness.endpoint.isClosed() {
		t.Fatal("endpoint closed before containment was proved")
	}
	harness.process.waitError = nil
	if err := candidate.cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !harness.endpoint.isClosed() {
		t.Fatal("endpoint remained after containment")
	}
}

func TestCandidateCleanupCachesTerminalFailureAfterContainment(t *testing.T) {
	t.Parallel()
	harness := newCandidateHarness(t)
	harness.startError = errors.New("private start")
	harness.process.forceError = errors.New("private kill")
	launcher := testCandidateLauncher(t, harness, bytes.NewReader(nonzeroEntropy()))
	candidate, launchErr := launcher.launch(context.Background(),
		testExecutable(t, "spice-agent-go-conformance", nil))
	if launchErr == nil || candidate == nil {
		t.Fatal("expected owned launch failure")
	}
	first := candidate.cleanup(context.Background())
	second := candidate.cleanup(context.Background())
	if first == nil || !errors.Is(second, first) || !harness.endpoint.isClosed() {
		t.Fatal("terminal cleanup failure was not cached after containment")
	}
}

func TestCandidateCleanupDestroysSessionAndCapturedOutput(t *testing.T) {
	t.Parallel()
	harness := newCandidateHarness(t)
	launcher := testCandidateLauncher(t, harness, bytes.NewReader(nonzeroEntropy()))
	candidate, err := launcher.launch(
		context.Background(),
		testExecutable(t, "spice-agent-go-conformance", nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = candidate.stderr.Write([]byte("plugin-owned-sensitive-output")); err != nil {
		t.Fatal(err)
	}
	if len(candidate.sessionID()) == 0 || len(candidate.stderr.snapshot().bytes()) == 0 {
		t.Fatal("fixture did not retain launch state before cleanup")
	}
	if err = candidate.cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	stderr := candidate.stderr.snapshot()
	if len(candidate.sessionID()) != 0 || len(candidate.launchIdentity()) != 0 ||
		len(stderr.bytes()) != 0 || stderr.totalBytes() != 0 {
		t.Fatal("candidate cleanup retained authenticated or plugin-controlled state")
	}
}

func TestCandidateLaunchFormattingRedactsOwnedValues(t *testing.T) {
	t.Parallel()
	harness := newCandidateHarness(t)
	harness.endpointError = errors.New("private-address-and-secret")
	launcher := testCandidateLauncher(t, harness, bytes.NewReader(nonzeroEntropy()))
	candidate, err := launcher.launch(context.Background(),
		testExecutable(t, "spice-agent-go-conformance", nil))
	if candidate == nil || err == nil {
		t.Fatal("expected endpoint failure")
	}
	for _, rendered := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprint(candidate)} {
		if strings.Contains(rendered, "private-address") || strings.Contains(rendered, harness.endpoint.address) {
			t.Fatalf("unsafe rendering %q", rendered)
		}
	}
	if cleanupErr := candidate.cleanup(context.Background()); cleanupErr != nil {
		t.Fatal(cleanupErr)
	}
}

func TestCandidateLifecycleMetadataAndFailuresRemainNilSafeAndRedacted(t *testing.T) {
	t.Parallel()
	var absent *candidate
	if absent.catalogSnapshot().Name() != "" || absent.pluginBuild() != nil ||
		absent.selectedProtocol() != nil || absent.negotiatedLimits() != nil ||
		absent.sessionID() != nil || absent.launchIdentity() != nil {
		t.Fatal("nil candidate exposed lifecycle metadata")
	}
	if err := absent.cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := absent.postReadyFailure(); err == nil {
		t.Fatal("nil candidate reported readiness")
	}
	if err := (&candidate{}).cleanup(nil); err == nil { //nolint:staticcheck // Deliberate nil boundary.
		t.Fatal("nil cleanup context succeeded")
	}

	secret := errors.New("private-plugin-path-and-secret")
	failure := launchFailure(launchPhaseInitialize, secret)
	var launch *launchError
	if !errors.As(failure, &launch) || !errors.Is(launch, secret) ||
		launch.phaseName() != launchPhaseInitialize {
		t.Fatal("launch failure did not preserve deliberate inspection")
	}
	invalid := &launchError{phase: "private-phase", cause: secret}
	cleanup := cleanupFailure(secret)
	if launchFailure(launchPhaseValidate, nil) != nil || cleanupFailure(nil) != nil {
		t.Fatal("nil failure cause produced an error")
	}
	for _, value := range []any{absent, failure, invalid, cleanup} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, rendered := range []string{
			fmt.Sprint(value),
			fmt.Sprintf("%#v", value),
			string(encoded),
		} {
			if strings.Contains(rendered, secret.Error()) || strings.Contains(rendered, "private-phase") {
				t.Fatalf("failure formatting exposed private state: %q", rendered)
			}
		}
	}
	var noLaunch *launchError
	var noCleanup *cleanupError
	if noLaunch.Unwrap() != nil || noLaunch.phaseName() != "" || noCleanup.Unwrap() != nil {
		t.Fatal("nil failure receivers exposed a cause")
	}
}

type candidateHarness struct {
	t                   *testing.T
	endpoint            *testLocalEndpoint
	endpointError       error
	endpointNil         bool
	startError          error
	nilProcess          bool
	exitBeforeReady     bool
	stdoutContamination bool
	service             func([]byte) (pluginv1.PluginServiceServer, error)
	launchIdentity      string

	mu      sync.Mutex
	spec    process.Spec
	secret  []byte
	process *testProcess
}

func newCandidateHarness(t *testing.T) *candidateHarness {
	t.Helper()
	listener := bufconn.Listen(pluginv1.InitializeBootstrapMaximumBytes)
	harness := &candidateHarness{t: t}
	harness.endpoint = &testLocalEndpoint{address: "runtime-plugin-test", listener: listener}
	harness.process = &testProcess{done: make(chan struct{})}
	harness.service = func(secret []byte) (pluginv1.PluginServiceServer, error) {
		return pluginfixture.NewService(secret, func() {})
	}
	return harness
}

func (harness *candidateHarness) Open(_ context.Context, launchIdentity string) (LocalEndpoint, error) {
	harness.launchIdentity = launchIdentity
	if harness.endpointNil {
		return nil, harness.endpointError
	}
	return harness.endpoint, harness.endpointError
}

func (harness *candidateHarness) Start(_ context.Context, spec process.Spec) (process.Process, error) {
	harness.mu.Lock()
	harness.spec = spec.Clone()
	harness.mu.Unlock()
	if harness.nilProcess {
		return nil, nil //nolint:nilnil // Deliberately exercise an invalid Launcher result.
	}
	if harness.startError != nil {
		return harness.process, harness.startError
	}
	address, secret, err := pluginv1.DecodeBootstrap(spec.Stdin())
	if err != nil {
		return harness.process, err
	}
	if address != harness.endpoint.address {
		clear(secret)
		return harness.process, errors.New("bootstrap address mismatch")
	}
	harness.secret = slices.Clone(secret)
	service, err := harness.service(secret)
	clear(secret)
	if err != nil {
		return harness.process, err
	}
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(pluginv1.InitializeBootstrapMaximumBytes),
		grpc.MaxSendMsgSize(pluginv1.InitializeBootstrapMaximumBytes),
	)
	pluginv1.RegisterPluginServiceServer(server, service)
	harness.process.stop = server.Stop
	go func() { _ = server.Serve(harness.endpoint.listener) }()
	if harness.exitBeforeReady {
		harness.process.finish()
		return harness.process, nil
	}
	if harness.stdoutContamination {
		_, _ = io.WriteString(spec.Stdout(), pluginv1.ReadinessRecord+"contamination")
		return harness.process, nil
	}
	_, _ = io.WriteString(spec.Stdout(), pluginv1.ReadinessRecord)
	return harness.process, nil
}

func (harness *candidateHarness) launchSpec() process.Spec {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	return harness.spec.Clone()
}

type testLocalEndpoint struct {
	address  string
	listener *bufconn.Listener
	closed   bool
	mu       sync.Mutex
}

func (endpoint *testLocalEndpoint) Address() string { return endpoint.address }
func (endpoint *testLocalEndpoint) Dial(context.Context) (net.Conn, error) {
	return endpoint.listener.Dial()
}

func (endpoint *testLocalEndpoint) Close() error {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	if endpoint.closed {
		return nil
	}
	endpoint.closed = true
	return endpoint.listener.Close()
}

func (endpoint *testLocalEndpoint) isClosed() bool {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	return endpoint.closed
}

type testProcess struct {
	done       chan struct{}
	once       sync.Once
	stop       func()
	killed     bool
	waited     bool
	waitError  error
	forceError error
	mu         sync.Mutex
}

func (owned *testProcess) Done() <-chan struct{} { return owned.done }
func (owned *testProcess) Result() (process.Outcome, error) {
	return process.NewExitedOutcome(1)
}
func (owned *testProcess) RequestStop(context.Context) error { return nil }
func (owned *testProcess) ForceKill(context.Context) error {
	owned.mu.Lock()
	owned.killed = true
	owned.mu.Unlock()
	owned.finish()
	return owned.forceError
}

func (owned *testProcess) Wait(context.Context) error {
	owned.mu.Lock()
	defer owned.mu.Unlock()
	owned.waited = true
	return owned.waitError
}

func (owned *testProcess) finish() {
	owned.once.Do(func() {
		if owned.stop != nil {
			owned.stop()
		}
		close(owned.done)
	})
}

func (owned *testProcess) wasKilled() bool {
	owned.mu.Lock()
	defer owned.mu.Unlock()
	return owned.killed
}

func (owned *testProcess) wasWaited() bool {
	owned.mu.Lock()
	defer owned.mu.Unlock()
	return owned.waited
}

func testCandidateLauncher(t *testing.T, harness *candidateHarness, entropy io.Reader) *candidateLauncher {
	t.Helper()
	launcher, err := newCandidateLauncher(harness, harness, entropy, &pluginv1.BuildIdentity{
		Component: "spice-agent-host", Version: "v1", Commit: "test", Runtime: runtime.Version(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return launcher
}

func testExecutable(t *testing.T, name string, capabilities []tool.Capability) Executable {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "fixture")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	content := []byte("verified runtime plugin executable")
	if err := os.WriteFile(path, content, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	digest, err := ParseSHA256(fmt.Sprintf("%x", sum))
	if err != nil {
		t.Fatal(err)
	}
	executable, err := NewExecutable(ExecutableConfig{
		ID: "fixture", ManifestName: name, ManifestVersion: "v1",
		Path: path, SHA256: digest, WorkingDirectory: root,
		Environment: []string{"VISIBLE=value"}, ApprovedCapabilities: capabilities,
		RequestedLimits: validHostLimits(), StartupTimeout: 3 * time.Second,
		CallTimeout: time.Second, DrainTimeout: time.Second, ShutdownTimeout: time.Second,
		ContainmentTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return executable
}

func nonzeroEntropy() []byte {
	result := make([]byte, pluginv1.LaunchIDBytes+pluginv1.HandshakeChallengeBytes+pluginv1.HandshakeSecretBytes)
	for index := range result {
		result[index] = byte(index%251 + 1)
	}
	return result
}

type signedService struct {
	pluginv1.UnimplementedPluginServiceServer
	secret      []byte
	manifest    *pluginv1.Manifest
	limits      *pluginv1.Limits
	tamperProof bool
}

type blockingInitializeService struct {
	pluginv1.UnimplementedPluginServiceServer
	entered chan<- struct{}
}

func (service blockingInitializeService) Initialize(
	ctx context.Context,
	_ *pluginv1.InitializeRequest,
) (*pluginv1.InitializeResponse, error) {
	close(service.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func newSignedService(
	t *testing.T,
	secret []byte,
	name string,
	capabilities []tool.Capability,
) (*signedService, error) {
	t.Helper()
	definition, err := tool.NewDefinition("fixture.tool", "Fixture tool.", []byte(`{"type":"object"}`),
		tool.EffectMutating, tool.ReplayUnsafe, capabilities...)
	if err != nil {
		return nil, err
	}
	limits := validHostLimits()
	catalog, err := pluginv1.NewCatalog(name, "v1", []tool.Definition{definition}, limits)
	if err != nil {
		return nil, err
	}
	manifest, err := catalog.Manifest()
	return &signedService{secret: slices.Clone(secret), manifest: manifest, limits: limits}, err
}

func (service *signedService) Initialize(
	_ context.Context,
	request *pluginv1.InitializeRequest,
) (*pluginv1.InitializeResponse, error) {
	response := &pluginv1.InitializeResponse{
		Status: commonv1.OKStatus(), Protocol: request.GetProtocol().GetMaximum(),
		Plugin:       &pluginv1.BuildIdentity{Component: "fixture", Version: "v1", Commit: "test", Runtime: runtime.Version()},
		Capabilities: &commonv1.CapabilitySet{Names: []string{pluginv1.CapabilityRuntimeToolsV1}},
		Limits:       service.limits, Manifest: service.manifest,
		LaunchId: slices.Clone(request.GetLaunchId()), SessionId: bytes.Repeat([]byte{1}, pluginv1.SessionIDBytes),
		HandshakeChallenge: slices.Clone(request.GetHandshakeChallenge()),
	}
	signed, err := pluginv1.SignInitializeResponse(request, response, service.secret)
	if err == nil && service.tamperProof {
		signed.HandshakeProof[0]++
	}
	return signed, err
}

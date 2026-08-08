package pluginfixture_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon/localipc"
	"github.com/spice-framework/spice-agent/internal/pluginfixture"
	"github.com/spice-framework/spice-agent/plugin/conformance"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestServePassesConformanceOverRealLocalIPC(t *testing.T) {
	address := localAddress(t)
	secret := bytes.Repeat([]byte{0x57}, pluginv1.HandshakeSecretBytes)
	bootstrap, err := pluginv1.EncodeBootstrap(address, secret)
	if err != nil {
		t.Fatal(err)
	}
	output := &readinessWriter{ready: make(chan struct{})}
	served := make(chan error, 1)
	go func() { served <- pluginfixture.Serve(bytes.NewReader(bootstrap), output) }()
	select {
	case <-time.After(3 * time.Second):
		t.Fatal("fixture readiness timed out")
	case <-output.ready:
	}
	connection, err := grpc.NewClient(
		"passthrough:///spice-agent-plugin-fixture-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return localipc.Dial(ctx, address)
		}),
		grpc.WithNoProxy(),
		grpc.WithDisableRetry(),
	)
	if err != nil {
		t.Fatal(err)
	}
	err = conformance.Run(context.Background(), pluginv1.NewPluginServiceClient(connection), conformance.Config{
		HostBuild: &pluginv1.BuildIdentity{
			Component: "fixture-unit-test", Version: "v1", Commit: "test", Runtime: runtime.Version(),
		},
		Limits:           testLimits(),
		Secret:           secret,
		OperationTimeout: time.Second,
	})
	closeErr := connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	select {
	case <-time.After(3 * time.Second):
		t.Fatal("fixture shutdown timed out")
	case err = <-served:
		if err != nil {
			t.Fatal(err)
		}
	}
	if output.String() != "{\"ready\":true}\n" {
		t.Fatalf("fixture stdout = %q", output.String())
	}
}

func TestServiceHonorsNegotiatedLimitsAndRejectsIncompatibleManifest(t *testing.T) {
	secret := bytes.Repeat([]byte{0x58}, pluginv1.HandshakeSecretBytes)
	client, stop := directServiceClient(t, secret)
	defer stop()

	incompatible := testLimits()
	incompatible.MaxTools = 2
	request := fixtureInitializeRequest(incompatible)
	response, err := client.Initialize(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	err = pluginv1.ValidateInitializeResponseForRequest(request, response, secret)
	var statusErr *commonv1.StatusError
	if !errors.As(err, &statusErr) ||
		statusErr.Status().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("incompatible manifest initialization = %#v, %v", response, err)
	}

	selected := testLimits()
	selected.MaxCallArgumentBytes = 1
	request = fixtureInitializeRequest(selected)
	response, err = client.Initialize(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err = pluginv1.ValidateInitializeResponseForRequest(request, response, secret); err != nil {
		t.Fatal(err)
	}
	if response.GetLimits().GetMaxCallArgumentBytes() != 1 {
		t.Fatalf("selected argument limit = %d", response.GetLimits().GetMaxCallArgumentBytes())
	}
	call := &pluginv1.ExecuteRequest{
		SessionId: response.GetSessionId(), CallId: "too-large", ToolName: conformance.EchoToolName,
		ArgumentsJson: []byte(`{}`),
	}
	if code := executeStatusCode(t, client, call); code != codes.ResourceExhausted {
		t.Fatalf("oversized call status = %s", code)
	}
}

func TestServiceEnforcesConcurrencyAndDrainAdmissionFence(t *testing.T) {
	secret := bytes.Repeat([]byte{0x59}, pluginv1.HandshakeSecretBytes)
	client, stop := directServiceClient(t, secret)
	defer stop()
	limits := testLimits()
	limits.MaxConcurrentCalls = 1
	request := fixtureInitializeRequest(limits)
	response, err := client.Initialize(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err = pluginv1.ValidateInitializeResponseForRequest(request, response, secret); err != nil {
		t.Fatal(err)
	}

	waitContext, cancelWait := context.WithCancel(context.Background())
	wait, err := client.Execute(waitContext, &pluginv1.ExecuteRequest{
		SessionId: response.GetSessionId(), CallId: "active", ToolName: conformance.WaitToolName,
		ArgumentsJson: []byte(`{}`),
	})
	if err != nil {
		cancelWait()
		t.Fatal(err)
	}
	if _, err = wait.Recv(); err != nil {
		cancelWait()
		t.Fatal(err)
	}
	overload := &pluginv1.ExecuteRequest{
		SessionId: response.GetSessionId(), CallId: "overload", ToolName: conformance.EchoToolName,
		ArgumentsJson: []byte(`{"value":"ignored"}`),
	}
	if code := executeStatusCode(t, client, overload); code != codes.ResourceExhausted {
		cancelWait()
		t.Fatalf("overload status = %s", code)
	}

	drainContext, cancelDrain := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, drainErr := client.Drain(drainContext, &pluginv1.DrainRequest{SessionId: response.GetSessionId()})
	cancelDrain()
	if status.Code(drainErr) != codes.DeadlineExceeded {
		cancelWait()
		t.Fatalf("active drain status = %s", status.Code(drainErr))
	}
	if code := executeStatusCode(t, client, overload); code != codes.Unavailable {
		cancelWait()
		t.Fatalf("post-drain admission status = %s", code)
	}
	cancelWait()
	if _, err = wait.Recv(); status.Code(err) != codes.Canceled {
		t.Fatalf("wait cancellation status = %s", status.Code(err))
	}
	operation, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = client.Drain(operation, &pluginv1.DrainRequest{SessionId: response.GetSessionId()}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Shutdown(operation, &pluginv1.ShutdownRequest{SessionId: response.GetSessionId()}); err != nil {
		t.Fatal(err)
	}
}

type readinessWriter struct {
	mu    sync.Mutex
	value bytes.Buffer
	ready chan struct{}
	once  sync.Once
}

func (writer *readinessWriter) Write(value []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	written, err := writer.value.Write(value)
	writer.once.Do(func() { close(writer.ready) })
	return written, err
}

func (writer *readinessWriter) Flush() error { return nil }

func (writer *readinessWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.value.String()
}

func localAddress(t *testing.T) string {
	t.Helper()
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	randomName := hex.EncodeToString(random)
	if runtime.GOOS == "windows" {
		return `\\.\pipe\spice-agent-plugin-` + randomName
	}
	name := "spf-" + randomName
	tempRoot := ""
	if runtime.GOOS == "darwin" {
		tempRoot = "/private/tmp"
	}
	directory, err := os.MkdirTemp(tempRoot, "spf-")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			t.Errorf("remove fixture directory: %v", cleanupErr)
		}
	})
	realDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(realDirectory, name+".sock")
}

func testLimits() *pluginv1.Limits {
	return &pluginv1.Limits{
		MaxMessageBytes:      pluginv1.InitializeBootstrapMaximumBytes,
		MaxTools:             16,
		MaxSchemaBytes:       64 << 10,
		MaxCallArgumentBytes: 64 << 10,
		MaxResultBytes:       pluginv1.InitializeBootstrapMaximumBytes,
		MaxProgressBytes:     tool.MaximumProgressBytes,
		MaxConcurrentCalls:   8,
	}
}

func fixtureInitializeRequest(limits *pluginv1.Limits) *pluginv1.InitializeRequest {
	return &pluginv1.InitializeRequest{
		Protocol: pluginv1.SupportedProtocolRange(),
		Host: &pluginv1.BuildIdentity{
			Component: "fixture-service-test", Version: "v1", Commit: "test", Runtime: runtime.Version(),
		},
		SupportedCapabilities: &commonv1.CapabilitySet{Names: []string{pluginv1.CapabilityRuntimeToolsV1}},
		RequiredCapabilities:  &commonv1.CapabilitySet{Names: []string{pluginv1.CapabilityRuntimeToolsV1}},
		RequestedLimits:       limits,
		LaunchId:              bytes.Repeat([]byte{0x21}, pluginv1.LaunchIDBytes),
		HandshakeChallenge:    bytes.Repeat([]byte{0x22}, pluginv1.HandshakeChallengeBytes),
	}
}

func executeStatusCode(
	t *testing.T,
	client pluginv1.PluginServiceClient,
	request *pluginv1.ExecuteRequest,
) codes.Code {
	t.Helper()
	stream, err := client.Execute(context.Background(), request)
	if err == nil {
		_, err = stream.Recv()
	}
	return status.Code(err)
}

func directServiceClient(
	t *testing.T,
	secret []byte,
) (pluginv1.PluginServiceClient, func()) {
	t.Helper()
	listener := bufconn.Listen(pluginv1.InitializeBootstrapMaximumBytes)
	server := grpc.NewServer()
	service, err := pluginfixture.NewService(secret, func() {})
	if err != nil {
		t.Fatal(err)
	}
	pluginv1.RegisterPluginServiceServer(server, service)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	connection, err := grpc.NewClient(
		"passthrough:///spice-agent-plugin-fixture-service-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithDisableRetry(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return pluginv1.NewPluginServiceClient(connection), func() {
		_ = connection.Close()
		server.Stop()
		_ = listener.Close()
		if serveErr := <-done; serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			t.Errorf("serve direct fixture: %v", serveErr)
		}
	}
}

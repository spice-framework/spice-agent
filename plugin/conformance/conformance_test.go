package conformance

import (
	"bytes"
	"context"
	"errors"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/internal/pluginfixture"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestRunAcceptsIndependentGoService(t *testing.T) {
	t.Parallel()
	secret := bytes.Repeat([]byte{0x42}, pluginv1.HandshakeSecretBytes)
	client, stop := fixtureClient(t, secret)
	defer stop()
	if err := Run(context.Background(), client, Config{
		HostBuild:        conformanceBuild(),
		Limits:           conformanceLimits(),
		Secret:           secret,
		OperationTimeout: time.Second,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsIncompleteConfiguration(t *testing.T) {
	t.Parallel()
	secret := bytes.Repeat([]byte{0x42}, pluginv1.HandshakeSecretBytes)
	client, stop := fixtureClient(t, secret)
	defer stop()
	if err := Run(nil, client, Config{}); err == nil { //nolint:staticcheck // Deliberate public nil-context boundary.
		t.Fatal("nil conformance context succeeded")
	}
	if err := Run(context.Background(), nil, Config{}); err == nil {
		t.Fatal("nil conformance client succeeded")
	}
	for name, config := range map[string]Config{
		"build":  {Limits: conformanceLimits(), Secret: secret},
		"limits": {HostBuild: conformanceBuild(), Secret: secret},
		"secret": {
			HostBuild: conformanceBuild(), Limits: conformanceLimits(), Secret: []byte("short"),
		},
		"timeout": {
			HostBuild: conformanceBuild(), Limits: conformanceLimits(), Secret: secret,
			OperationTimeout: time.Millisecond,
		},
	} {
		if err := Run(context.Background(), client, config); err == nil {
			t.Errorf("invalid %s configuration succeeded", name)
		}
	}
}

func TestRunRejectsWrongSecretCancellationAndInvalidMetadata(t *testing.T) {
	t.Parallel()
	secret := bytes.Repeat([]byte{0x42}, pluginv1.HandshakeSecretBytes)
	client, stop := fixtureClient(t, secret)
	if err := Run(context.Background(), client, Config{
		HostBuild: conformanceBuild(), Limits: conformanceLimits(),
		Secret: bytes.Repeat([]byte{0x99}, pluginv1.HandshakeSecretBytes),
	}); err == nil {
		stop()
		t.Fatal("wrong fixture secret succeeded")
	}
	stop()

	client, stop = fixtureClient(t, secret)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(cancelled, client, Config{
		HostBuild: conformanceBuild(), Limits: conformanceLimits(), Secret: secret,
	}); err == nil {
		stop()
		t.Fatal("cancelled conformance run succeeded")
	}
	stop()

	client, stop = fixtureClient(t, secret)
	defer stop()
	invalidBuild := conformanceBuild()
	invalidBuild.Component = "invalid\nmetadata"
	if err := Run(context.Background(), client, Config{
		HostBuild: invalidBuild, Limits: conformanceLimits(), Secret: secret,
	}); err == nil {
		t.Fatal("invalid conformance build succeeded")
	}
	invalidLimits := conformanceLimits()
	invalidLimits.MaxTools = 0
	if err := Run(context.Background(), client, Config{
		HostBuild: conformanceBuild(), Limits: invalidLimits, Secret: secret,
	}); err == nil {
		t.Fatal("invalid conformance limits succeeded")
	}
}

func TestValidateManifestRejectsMissingAndIncompleteProfiles(t *testing.T) {
	t.Parallel()
	if err := validateManifest(nil, conformanceLimits()); err == nil {
		t.Fatal("nil conformance manifest succeeded")
	}
	definition, err := tool.NewDefinition(
		EchoToolName, "Echo.", []byte(`{"type":"object"}`),
		tool.EffectReadOnly, tool.ReplaySafe,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := pluginv1.NewCatalog("incomplete", "v1", []tool.Definition{definition}, conformanceLimits())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := catalog.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if err = validateManifest(manifest, conformanceLimits()); err == nil {
		t.Fatal("incomplete conformance manifest succeeded")
	}
}

func TestRunBoundsAndValidatesWaitAdmission(t *testing.T) {
	t.Parallel()
	secret := bytes.Repeat([]byte{0x42}, pluginv1.HandshakeSecretBytes)
	profiles := []struct {
		name     string
		decorate func(grpc.ServerStreamingClient[pluginv1.ExecuteResponse]) grpc.ServerStreamingClient[pluginv1.ExecuteResponse]
	}{
		{"timeout", func(stream grpc.ServerStreamingClient[pluginv1.ExecuteResponse]) grpc.ServerStreamingClient[pluginv1.ExecuteResponse] {
			return &stalledWaitStream{ServerStreamingClient: stream}
		}},
		{"correlation", func(stream grpc.ServerStreamingClient[pluginv1.ExecuteResponse]) grpc.ServerStreamingClient[pluginv1.ExecuteResponse] {
			return &corruptedWaitStream{ServerStreamingClient: stream}
		}},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			t.Parallel()
			client, stop := fixtureClient(t, secret)
			defer stop()
			started := time.Now()
			err := Run(context.Background(), &waitStreamClient{
				PluginServiceClient: client,
				decorate:            profile.decorate,
			}, Config{
				HostBuild: conformanceBuild(), Limits: conformanceLimits(), Secret: secret,
				OperationTimeout: minimumOperationTimeout,
			})
			if err == nil {
				t.Fatal("invalid wait admission succeeded")
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("invalid wait admission took %s", elapsed)
			}
		})
	}
}

func TestReceiveWithinCancelsAndJoinsCooperativeReceive(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan struct{})
	_, err := receiveWithin(ctx, minimumOperationTimeout, cancel, func() (*pluginv1.ExecuteResponse, error) {
		defer close(exited)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("receive error = %v", err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("cooperative receive remained active after timeout")
	}
}

type waitStreamClient struct {
	pluginv1.PluginServiceClient
	decorate func(grpc.ServerStreamingClient[pluginv1.ExecuteResponse]) grpc.ServerStreamingClient[pluginv1.ExecuteResponse]
}

func (client *waitStreamClient) Execute(
	ctx context.Context,
	request *pluginv1.ExecuteRequest,
	options ...grpc.CallOption,
) (grpc.ServerStreamingClient[pluginv1.ExecuteResponse], error) {
	stream, err := client.PluginServiceClient.Execute(ctx, request, options...)
	if err != nil || request.GetToolName() != WaitToolName {
		return stream, err
	}
	return client.decorate(stream), nil
}

type stalledWaitStream struct {
	grpc.ServerStreamingClient[pluginv1.ExecuteResponse]
}

func (stream *stalledWaitStream) Recv() (*pluginv1.ExecuteResponse, error) {
	<-stream.Context().Done()
	return nil, stream.Context().Err()
}

type corruptedWaitStream struct {
	grpc.ServerStreamingClient[pluginv1.ExecuteResponse]
}

func (stream *corruptedWaitStream) Recv() (*pluginv1.ExecuteResponse, error) {
	response, err := stream.ServerStreamingClient.Recv()
	if response != nil {
		response.CallId = "wrong-call"
	}
	return response, err
}

func fixtureClient(t *testing.T, secret []byte) (pluginv1.PluginServiceClient, func()) {
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
		"passthrough:///spice-agent-plugin-conformance-test",
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
			t.Errorf("serve fixture: %v", serveErr)
		}
	}
}

func conformanceBuild() *pluginv1.BuildIdentity {
	return &pluginv1.BuildIdentity{
		Component: "conformance-test",
		Version:   "v1",
		Commit:    "test",
		Runtime:   runtime.Version(),
	}
}

func conformanceLimits() *pluginv1.Limits {
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

package localclient

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
	"github.com/spice-framework/spice-agent/daemon/endpoint"
)

func endpointFixture(tb testing.TB, address string, transport endpoint.Transport) endpoint.Metadata {
	tb.Helper()
	token, err := endpoint.GenerateToken()
	if err != nil {
		tb.Fatal(err)
	}
	build, err := client.NewBuild("spice-agentd", "test", "commit", "go1.26.5")
	if err != nil {
		tb.Fatal(err)
	}
	protocol, err := client.NewProtocolVersion(1, 2, 0)
	if err != nil {
		tb.Fatal(err)
	}
	process, err := endpoint.NewProcess(42, time.Unix(1_700_000_000, 0).UTC(), []byte("instance-id-0001"))
	if err != nil {
		tb.Fatal(err)
	}
	metadata, err := endpoint.NewMetadata(transport, address, token, build, protocol, process)
	if err != nil {
		tb.Fatal(err)
	}
	return metadata
}

func connectionFixture(tb testing.TB, metadata endpoint.Metadata) client.Connection {
	tb.Helper()
	limits, err := client.NewLimits(1<<20, 64, 32, 1<<20, 8, 8)
	if err != nil {
		tb.Fatal(err)
	}
	health, err := client.NewHealth(client.HealthReady, nil, 0, limits)
	if err != nil {
		tb.Fatal(err)
	}
	reference, err := client.NewDefinitionRef("fixture", "revision-1")
	if err != nil {
		tb.Fatal(err)
	}
	definition, err := client.NewDefinition(reference, "fixture-model", 1)
	if err != nil {
		tb.Fatal(err)
	}
	catalog, err := client.NewCatalog("catalog-1", []client.Definition{definition}, limits)
	if err != nil {
		tb.Fatal(err)
	}
	connection, err := client.NewConnection(client.ConnectionSpec{
		Protocol: metadata.Protocol(), Server: metadata.Server(), Limits: limits,
		Health: health, ClientID: "client-1", OwnershipEpoch: 1, Catalog: catalog,
	})
	if err != nil {
		tb.Fatal(err)
	}
	return connection
}

func initializeRequestFixture(tb testing.TB, metadata endpoint.Metadata) client.InitializeRequest {
	tb.Helper()
	rangeValue, err := client.NewProtocolRange(metadata.Protocol(), metadata.Protocol())
	if err != nil {
		tb.Fatal(err)
	}
	build, err := client.NewBuild("local-client-test", "test", "commit", "go1.26.5")
	if err != nil {
		tb.Fatal(err)
	}
	limits, err := client.NewLimits(1<<20, 64, 32, 1<<20, 8, 8)
	if err != nil {
		tb.Fatal(err)
	}
	request, err := client.NewInitializeRequest(rangeValue, build, nil, nil, limits)
	if err != nil {
		tb.Fatal(err)
	}
	return request
}

func reconnectRequestFixture(
	tb testing.TB,
	metadata endpoint.Metadata,
	claim client.ReconnectClaim,
) client.InitializeRequest {
	tb.Helper()
	fresh := initializeRequestFixture(tb, metadata)
	request, err := client.NewReconnectRequest(
		fresh.Protocol(), fresh.Client(), fresh.SupportedCapabilities(), fresh.RequiredCapabilities(),
		fresh.RequestedLimits(), claim,
	)
	if err != nil {
		tb.Fatal(err)
	}
	return request
}

type fixtureSession struct {
	client.Session
	connection client.Connection
	close      func() error
}

func (session *fixtureSession) Connection() client.Connection { return session.connection }
func (session *fixtureSession) Close() error {
	if session.close == nil {
		return nil
	}
	return session.close()
}

type fixtureConnector struct {
	initialize func(context.Context, client.InitializeRequest) (client.Session, error)
}

func (connector fixtureConnector) Initialize(
	ctx context.Context,
	request client.InitializeRequest,
) (client.Session, error) {
	return connector.initialize(ctx, request)
}

type fixtureEndpointDiscovery struct {
	metadata endpoint.Metadata
	err      error
}

func (fixture fixtureEndpointDiscovery) Discover(context.Context) (endpoint.Metadata, error) {
	return fixture.metadata, fixture.err
}

type fixtureEndpointStartup struct {
	lease *endpoint.StartupLease
	err   error
}

func (fixture fixtureEndpointStartup) AcquireStartup(context.Context) (*endpoint.StartupLease, error) {
	return fixture.lease, fixture.err
}

type blockingEndpointStartup struct{}

func (blockingEndpointStartup) AcquireStartup(ctx context.Context) (*endpoint.StartupLease, error) {
	<-ctx.Done()
	return nil, context.Cause(ctx)
}

var errFixture = errors.New("fixture failure")

type mutableEndpointDiscovery struct {
	mu       sync.Mutex
	metadata endpoint.Metadata
	err      error
}

type countingEndpointDiscovery struct {
	metadata endpoint.Metadata
	calls    atomic.Int32
}

func (fixture *countingEndpointDiscovery) Discover(context.Context) (endpoint.Metadata, error) {
	fixture.calls.Add(1)
	return fixture.metadata, nil
}

func (fixture *mutableEndpointDiscovery) Discover(context.Context) (endpoint.Metadata, error) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.metadata, fixture.err
}

func (fixture *mutableEndpointDiscovery) set(metadata endpoint.Metadata, err error) {
	fixture.mu.Lock()
	fixture.metadata = metadata
	fixture.err = err
	fixture.mu.Unlock()
}

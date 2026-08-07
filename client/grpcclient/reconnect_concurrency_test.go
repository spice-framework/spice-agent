package grpcclient

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon/endpoint"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestReconnectSerializesFenceRPCAndInstallForOneClient(t *testing.T) {
	t.Parallel()
	connection1 := reconnectConnection(t, "client-serial", 1)
	connection2 := reconnectConnection(t, "client-serial", 2)
	request1 := reconnectRequest(t, connection1, 1)
	request2 := reconnectRequest(t, connection2, 2)
	response2 := reconnectResponse(t, request1, "client-serial", 2)
	response3 := reconnectResponse(t, request2, "client-serial", 3)
	entered1 := make(chan struct{})
	entered2 := make(chan struct{})
	release1 := make(chan struct{})
	service := &unaryEngineClient{initialize: func(_ context.Context, request *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
		switch request.GetReconnectClaim().GetExpectedOwnershipEpoch() {
		case 1:
			close(entered1)
			<-release1
			return proto.CloneOf(response2), nil
		case 2:
			close(entered2)
			return proto.CloneOf(response3), nil
		default:
			return nil, errors.New("unexpected reconnect epoch")
		}
	}}
	connector := reconnectConnector(t, service)
	old := unaryTestSession(t, connection1, service)
	old.owner = connector
	connector.sessions[connection1.ClientID()] = old
	lifetime, _ := newStreamLifetime(old)
	old.streams[lifetime] = struct{}{}

	first := make(chan initializeResult, 1)
	go initializeAsync(connector, request1, first)
	<-lifetime.done
	assertNotSignaled(t, entered1, "first reconnect reached RPC before its old stream joined")

	secondStarted := make(chan struct{})
	second := make(chan initializeResult, 1)
	go func() {
		close(secondStarted)
		initializeAsync(connector, request2, second)
	}()
	<-secondStarted
	waitForReconnectUsers(t, connector, connection1.ClientID(), 2)
	assertNotSignaled(t, entered1, "first reconnect bypassed its stream fence")
	assertNotSignaled(t, entered2, "N+1 reconnect bypassed the N reconnect gate")

	lifetime.finishRPC()
	<-entered1
	assertNotSignaled(t, entered2, "N+1 reconnect overlapped the N reconnect RPC")
	close(release1)
	firstResult := <-first
	if firstResult.err != nil || firstResult.session.Connection().OwnershipEpoch() != 2 {
		t.Fatalf("first reconnect = %#v, %v", firstResult.session, firstResult.err)
	}
	<-entered2
	secondResult := <-second
	if secondResult.err != nil || secondResult.session.Connection().OwnershipEpoch() != 3 {
		t.Fatalf("second reconnect = %#v, %v", secondResult.session, secondResult.err)
	}
	connector.mu.Lock()
	installed := connector.sessions[connection1.ClientID()]
	connector.mu.Unlock()
	if installed == nil || installed != secondResult.session || installed.connection.OwnershipEpoch() != 3 {
		t.Fatalf("installed reconnect = %#v, want epoch 3", installed)
	}
}

func TestCancelledReconnectWaiterDoesNotAffectOwner(t *testing.T) {
	t.Parallel()
	connection := reconnectConnection(t, "client-cancel", 1)
	request := reconnectRequest(t, connection, 1)
	response := reconnectResponse(t, request, connection.ClientID(), 2)
	ownerEntered := make(chan struct{})
	ownerRelease := make(chan struct{})
	var calls atomic.Int32
	service := &unaryEngineClient{initialize: func(_ context.Context, _ *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
		if calls.Add(1) != 1 {
			return nil, errors.New("cancelled waiter reached the RPC")
		}
		close(ownerEntered)
		<-ownerRelease
		return proto.CloneOf(response), nil
	}}
	connector := reconnectConnector(t, service)
	old := unaryTestSession(t, connection, service)
	old.owner = connector
	connector.sessions[connection.ClientID()] = old

	owner := make(chan initializeResult, 1)
	go initializeAsync(connector, request, owner)
	<-ownerEntered
	waiterContext, cancelWaiter := context.WithCancel(t.Context())
	waiter := make(chan initializeResult, 1)
	go func() {
		session, err := connector.Initialize(waiterContext, request)
		waiter <- initializeResult{session: session, err: err}
	}()
	waitForReconnectUsers(t, connector, connection.ClientID(), 2)
	protected := unaryTestSession(t, connection, service)
	protected.owner = connector
	connector.mu.Lock()
	if connector.sessions[connection.ClientID()] != nil {
		connector.mu.Unlock()
		t.Fatal("owner reconnect did not fence its exact prior session")
	}
	connector.sessions[connection.ClientID()] = protected
	beforeOverload := connector.sessions[connection.ClientID()]
	connector.mu.Unlock()
	overloadedSession, overloadErr := connector.Initialize(t.Context(), request)
	var overload *client.OverloadError
	if overloadedSession != nil || !errors.As(overloadErr, &overload) {
		t.Fatalf("excess reconnect = %#v, %v, want typed overload", overloadedSession, overloadErr)
	}
	if overload.Resource() != "client-reconnect-waiters" || overload.Limit() != 1 ||
		overload.Observed() != 2 || !overload.Retryable() {
		t.Fatalf(
			"excess reconnect overload = resource %q, limit %d, observed %d, retryable %t",
			overload.Resource(), overload.Limit(), overload.Observed(), overload.Retryable(),
		)
	}
	connector.mu.Lock()
	afterOverload := connector.sessions[connection.ClientID()]
	gate := connector.reconnects[connection.ClientID()]
	gateUsers := 0
	if gate != nil {
		gateUsers = gate.users
	}
	connector.mu.Unlock()
	if afterOverload != beforeOverload || gateUsers != 2 || calls.Load() != 1 {
		t.Fatalf(
			"excess reconnect mutated state: session before %#v, after %#v, gate users %d, RPC calls %d",
			beforeOverload, afterOverload, gateUsers, calls.Load(),
		)
	}
	protected.mu.Lock()
	protectedClosed := protected.closed
	protected.mu.Unlock()
	if protectedClosed {
		t.Fatal("excess reconnect fenced the protected local session")
	}
	if err := protected.Close(); err != nil {
		t.Fatal(err)
	}
	cancelWaiter()
	waiterResult := <-waiter
	if waiterResult.session != nil || !errors.Is(waiterResult.err, context.Canceled) {
		t.Fatalf("cancelled waiter = %#v, %v", waiterResult.session, waiterResult.err)
	}
	if calls.Load() != 1 {
		t.Fatalf("RPC calls after waiter cancellation = %d, want 1", calls.Load())
	}
	close(ownerRelease)
	ownerResult := <-owner
	if ownerResult.err != nil || ownerResult.session.Connection().OwnershipEpoch() != 2 {
		t.Fatalf("owner reconnect = %#v, %v", ownerResult.session, ownerResult.err)
	}
	waitForReconnectUsers(t, connector, connection.ClientID(), 0)
}

func TestReconnectsForDistinctClientsRemainConcurrent(t *testing.T) {
	t.Parallel()
	connections := []client.Connection{
		reconnectConnection(t, "client-a", 1),
		reconnectConnection(t, "client-b", 1),
	}
	requests := []client.InitializeRequest{
		reconnectRequest(t, connections[0], 1),
		reconnectRequest(t, connections[1], 1),
	}
	responses := map[string]*enginev1.InitializeResponse{
		"client-a": reconnectResponse(t, requests[0], "client-a", 2),
		"client-b": reconnectResponse(t, requests[1], "client-b", 2),
	}
	entered := make(chan string, 2)
	release := make(chan struct{})
	service := &unaryEngineClient{initialize: func(_ context.Context, request *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
		clientID := request.GetReconnectClaim().GetClientId()
		entered <- clientID
		<-release
		return proto.CloneOf(responses[clientID]), nil
	}}
	connector := reconnectConnector(t, service)
	for index := range connections {
		old := unaryTestSession(t, connections[index], service)
		old.owner = connector
		connector.sessions[connections[index].ClientID()] = old
	}
	results := make(chan initializeResult, 2)
	for _, request := range requests {
		go initializeAsync(connector, request, results)
	}
	seen := map[string]bool{<-entered: true, <-entered: true}
	if !seen["client-a"] || !seen["client-b"] {
		t.Fatalf("concurrent reconnects entered for %v", seen)
	}
	close(release)
	for range connections {
		result := <-results
		if result.err != nil || result.session.Connection().OwnershipEpoch() != 2 {
			t.Fatalf("distinct reconnect = %#v, %v", result.session, result.err)
		}
	}
}

func TestReconnectResponseCannotRegressInstalledEpoch(t *testing.T) {
	t.Parallel()
	installedConnection := reconnectConnection(t, "client-regression", 3)
	requestConnection := reconnectConnection(t, "client-regression", 1)
	request := reconnectRequest(t, requestConnection, 1)
	response := reconnectResponse(t, request, requestConnection.ClientID(), 2)
	service := &unaryEngineClient{initialize: func(context.Context, *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
		return proto.CloneOf(response), nil
	}}
	connector := reconnectConnector(t, service)
	installed := unaryTestSession(t, installedConnection, service)
	installed.owner = connector
	connector.sessions[installedConnection.ClientID()] = installed

	result, err := connector.Initialize(t.Context(), request)
	if result != nil || errorCode(err) != client.ErrorInternal {
		t.Fatalf("regressing reconnect = %#v, %v", result, err)
	}
	connector.mu.Lock()
	current := connector.sessions[installedConnection.ClientID()]
	connector.mu.Unlock()
	if current == nil || current != installed || current.connection.OwnershipEpoch() != 3 {
		t.Fatalf("installed epoch regressed to %#v", current)
	}
	installed.mu.Lock()
	closed := installed.closed
	installed.mu.Unlock()
	if closed {
		t.Fatal("stale reconnect closed the newer installed session")
	}
}

func TestFutureReconnectClaimDoesNotFenceLocalPrior(t *testing.T) {
	t.Parallel()
	connection := reconnectConnection(t, "client-future", 1)
	request := reconnectRequest(t, connection, 2)
	service := &unaryEngineClient{initialize: func(context.Context, *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
		return nil, status.Error(codes.Unavailable, "future claim outcome unknown")
	}}
	connector := reconnectConnector(t, service)
	prior := unaryTestSession(t, connection, service)
	prior.owner = connector
	connector.sessions[connection.ClientID()] = prior
	lifetime, _ := newStreamLifetime(prior)
	prior.streams[lifetime] = struct{}{}

	result, err := connector.Initialize(t.Context(), request)
	if result != nil || errorCode(err) != client.ErrorUnavailable {
		t.Fatalf("future reconnect = %#v, %v", result, err)
	}
	select {
	case <-lifetime.done:
		t.Fatal("future reconnect claim fenced the current local stream")
	default:
	}
	prior.mu.Lock()
	closed := prior.closed
	prior.mu.Unlock()
	if closed {
		t.Fatal("future reconnect claim closed the current local session")
	}
	connector.mu.Lock()
	current := connector.sessions[connection.ClientID()]
	connector.mu.Unlock()
	if current != prior {
		t.Fatalf("future reconnect replaced current session with %#v", current)
	}
	lifetime.close()
	lifetime.finishRPC()
}

type initializeResult struct {
	session client.Session
	err     error
}

func initializeAsync(connector *Connector, request client.InitializeRequest, output chan<- initializeResult) {
	session, err := connector.Initialize(context.Background(), request)
	output <- initializeResult{session: session, err: err}
}

func reconnectConnector(t *testing.T, service enginev1.EngineServiceClient) *Connector {
	t.Helper()
	token, err := endpoint.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	return &Connector{
		service: service, token: token, sessions: make(map[string]*session),
		reconnects: make(map[string]*reconnectGate),
	}
}

func reconnectConnection(t *testing.T, clientID string, epoch uint64) client.Connection {
	t.Helper()
	base := unaryTestConnection(t)
	connection, err := client.NewConnection(client.ConnectionSpec{
		Protocol: base.Protocol(), Server: base.Server(), Capabilities: base.Capabilities(),
		Limits: base.Limits(), Health: base.Health(), ClientID: clientID,
		OwnershipEpoch: epoch, Catalog: base.Catalog(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func reconnectRequest(t *testing.T, connection client.Connection, expected uint64) client.InitializeRequest {
	t.Helper()
	claim, err := client.NewReconnectClaim(connection.ClientID(), expected)
	if err != nil {
		t.Fatal(err)
	}
	return mustInitializeRequest(t, connection, &claim)
}

func reconnectResponse(
	t *testing.T,
	request client.InitializeRequest,
	clientID string,
	epoch uint64,
) *enginev1.InitializeResponse {
	t.Helper()
	wireRequest, err := initializeRequestToWire(request)
	if err != nil {
		t.Fatal(err)
	}
	build, err := buildToWire(request.Client())
	if err != nil {
		t.Fatal(err)
	}
	limits, err := limitsToWire(request.RequestedLimits())
	if err != nil {
		t.Fatal(err)
	}
	response := &enginev1.InitializeResponse{
		Status: commonv1.OKStatus(), Protocol: proto.CloneOf(wireRequest.GetProtocol().GetMaximum()),
		Server: build, Capabilities: &commonv1.CapabilitySet{Names: request.RequiredCapabilities()},
		Limits: limits, Health: &commonv1.Health{State: commonv1.HealthState_HEALTH_STATE_READY, Limits: proto.CloneOf(limits)},
		ClientId: clientID, OwnershipEpoch: epoch,
		Definitions: &enginev1.DefinitionSet{Revision: "catalog", Definitions: []*enginev1.Definition{{
			Id: "definition", Revision: "revision", Model: "model", MaxTurns: 1,
		}}},
	}
	if err = enginev1.ValidateInitializeResponseForRequest(wireRequest, response); err != nil {
		t.Fatalf("reconnect response fixture: %v", err)
	}
	return response
}

func waitForReconnectUsers(t *testing.T, connector *Connector, clientID string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		connector.mu.Lock()
		gate := connector.reconnects[clientID]
		got := 0
		if gate != nil {
			got = gate.users
		}
		connector.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("reconnect gate users = %d, want %d", got, want)
		}
		runtime.Gosched()
	}
}

func assertNotSignaled(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
		t.Fatal(message)
	default:
	}
}

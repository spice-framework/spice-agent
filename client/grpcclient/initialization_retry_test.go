package grpcclient

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon/endpoint"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

const aggressiveRetryServiceConfig = `{
  "methodConfig": [{
    "name": [{"service": "spice.agent.engine.v1.EngineService"}],
    "retryPolicy": {
      "maxAttempts": 4,
      "initialBackoff": "0.001s",
      "maxBackoff": "0.001s",
      "backoffMultiplier": 1,
      "retryableStatusCodes": ["UNAVAILABLE"]
    }
  }]
}`

func TestInitializationAttemptRetriesFreshAndReconnectWithExactCallerIntent(t *testing.T) {
	connection := unaryTestConnection(t)
	for _, reconnect := range []bool{false, true} {
		name := "fresh"
		if reconnect {
			name = "reconnect"
		}
		t.Run(name, func(t *testing.T) {
			request := retryInitializeRequest(t, connection, reconnect)
			wireRequest, err := initializeRequestToWire(request)
			if err != nil {
				t.Fatal(err)
			}
			epoch := uint64(1)
			if reconnect {
				epoch = connection.OwnershipEpoch() + 1
			}
			response := reconnectResponse(t, request, connection.ClientID(), epoch)
			var requests []*enginev1.InitializeRequest
			service := &unaryEngineClient{initialize: func(
				_ context.Context,
				observed *enginev1.InitializeRequest,
			) (*enginev1.InitializeResponse, error) {
				requests = append(requests, proto.CloneOf(observed))
				if len(requests) == 1 {
					observed.Client.Component = "mutated-by-first-transport-attempt"
					return nil, status.Error(codes.Unavailable, "response lost")
				}
				return proto.CloneOf(response), nil
			}}
			connector := testConnector(t, service)
			session, err := connector.Initialize(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			if len(requests) != 2 || !proto.Equal(requests[0], requests[1]) ||
				!proto.Equal(requests[0], wireRequest) || requests[0] == requests[1] {
				t.Fatalf("RPC requests were not two exact independent clones: %#v", requests)
			}
			attempt, ok := request.AttemptID()
			attemptBytes := attempt.Bytes()
			if !ok || string(requests[0].GetInitializationAttemptId()) != string(attemptBytes[:]) {
				t.Fatalf("caller attempt identity was not preserved: %v, %t", attempt, ok)
			}
		})
	}
}

func TestInitializationAttemptRetryIsCappedAndCallerCanRetainIdentity(t *testing.T) {
	connection := unaryTestConnection(t)
	request := retryInitializeRequest(t, connection, false)
	want, ok := request.AttemptID()
	if !ok {
		t.Fatal("attempt request has no caller-retainable identity")
	}
	count := 0
	service := &unaryEngineClient{initialize: func(context.Context, *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
		count++
		return nil, status.Error(codes.Unavailable, "response lost")
	}}
	connector := testConnector(t, service)
	_, err := connector.Initialize(t.Context(), request)
	replay, typed := errors.AsType[*client.InitializationReplayError](err)
	if count != 2 || !typed || replay.AttemptID() != want || !replay.Retryable() {
		t.Fatalf("Initialize calls = %d, error = %T %v", count, err, err)
	}
	if got, present := request.AttemptID(); !present || got != want {
		t.Fatalf("request attempt changed after retry: %v, %t", got, present)
	}
}

func TestInitializationAttemptCancellationAndDeadlinePreserveExactReplayAfterCommit(t *testing.T) {
	connection := unaryTestConnection(t)
	for _, reconnect := range []bool{false, true} {
		for _, failure := range []struct {
			name  string
			code  codes.Code
			cause error
		}{
			{name: "cancellation", code: codes.Canceled, cause: context.Canceled},
			{name: "deadline", code: codes.DeadlineExceeded, cause: context.DeadlineExceeded},
		} {
			name := "fresh/" + failure.name
			if reconnect {
				name = "reconnect/" + failure.name
			}
			t.Run(name, func(t *testing.T) {
				request := retryInitializeRequest(t, connection, reconnect)
				want, ok := request.AttemptID()
				if !ok {
					t.Fatal("attempt request omitted replay identity")
				}
				ctx, cancel := context.WithCancelCause(t.Context())
				t.Cleanup(func() { cancel(context.Canceled) })
				calls := 0
				committed := false
				service := &unaryEngineClient{initialize: func(
					context.Context,
					*enginev1.InitializeRequest,
				) (*enginev1.InitializeResponse, error) {
					calls++
					committed = true
					cancel(failure.cause)
					return nil, status.Error(failure.code, "committed response was lost")
				}}
				connector := testConnector(t, service)
				_, err := connector.Initialize(ctx, request)
				replay, typed := errors.AsType[*client.InitializationReplayError](err)
				if calls != 1 || !committed || !errors.Is(err, failure.cause) || !typed ||
					replay.AttemptID() != want || !replay.Retryable() ||
					!strings.Contains(replay.Facts().Message(), "same immutable request and attempt ID") {
					t.Fatalf(
						"Initialize calls = %d, committed = %t, error = %T %v, replay = %#v",
						calls, committed, err, err, replay,
					)
				}
			})
		}
	}
}

func TestInitializationAttemptRejectsSuccessEchoMismatch(t *testing.T) {
	connection := unaryTestConnection(t)
	request := retryInitializeRequest(t, connection, false)
	response := reconnectResponse(t, request, connection.ClientID(), 1)
	response.InitializationAttemptId[0] ^= 0xff
	service := &unaryEngineClient{initialize: func(context.Context, *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
		return response, nil
	}}
	connector := testConnector(t, service)
	if _, err := connector.Initialize(t.Context(), request); errorCode(err) != client.ErrorInternal {
		t.Fatalf("echo mismatch error = %T %v", err, err)
	}
}

func TestInitializationAttemptDoesNotAutoRetryAmbiguousOrDefinitiveFailures(t *testing.T) {
	connection := unaryTestConnection(t)
	for name, code := range map[string]codes.Code{
		"cancelled":      codes.Canceled,
		"deadline":       codes.DeadlineExceeded,
		"authentication": codes.PermissionDenied,
		"invalid":        codes.InvalidArgument,
		"internal":       codes.Internal,
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			service := &unaryEngineClient{initialize: func(context.Context, *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
				calls++
				return nil, status.Error(code, "definitive failure")
			}}
			connector := testConnector(t, service)
			_, _ = connector.Initialize(t.Context(), retryInitializeRequest(t, connection, false))
			if calls != 1 {
				t.Fatalf("Initialize calls = %d, want 1", calls)
			}
		})
	}

	t.Run("caller context cancelled after transient failure", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		service := &unaryEngineClient{initialize: func(context.Context, *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
			calls++
			cancel()
			return nil, status.Error(codes.Unavailable, "response lost")
		}}
		connector := testConnector(t, service)
		_, err := connector.Initialize(ctx, retryInitializeRequest(t, connection, false))
		if calls != 1 || !errors.Is(err, context.Canceled) {
			t.Fatalf("Initialize calls = %d, error = %T %v", calls, err, err)
		}
	})
}

func TestInitializationAttemptServerCancellationAndDeadlinePreserveReplayWithLiveCaller(t *testing.T) {
	connection := unaryTestConnection(t)
	for _, reconnect := range []bool{false, true} {
		for _, failure := range []struct {
			name  string
			code  codes.Code
			cause error
		}{
			{name: "cancellation", code: codes.Canceled, cause: context.Canceled},
			{name: "deadline", code: codes.DeadlineExceeded, cause: context.DeadlineExceeded},
		} {
			name := "fresh/" + failure.name
			if reconnect {
				name = "reconnect/" + failure.name
			}
			t.Run(name, func(t *testing.T) {
				request := retryInitializeRequest(t, connection, reconnect)
				want, ok := request.AttemptID()
				if !ok {
					t.Fatal("attempt request omitted replay identity")
				}
				calls := 0
				service := &unaryEngineClient{initialize: func(
					context.Context,
					*enginev1.InitializeRequest,
				) (*enginev1.InitializeResponse, error) {
					calls++
					return nil, status.Error(failure.code, "committed response was lost")
				}}
				connector := testConnector(t, service)
				_, err := connector.Initialize(t.Context(), request)
				replay, typed := errors.AsType[*client.InitializationReplayError](err)
				if calls != 1 || !errors.Is(err, failure.cause) || !typed ||
					replay.AttemptID() != want || !replay.Retryable() {
					t.Fatalf("Initialize calls = %d, error = %T %v, replay = %#v", calls, err, err, replay)
				}
			})
		}
	}
}

func TestInitializationAttemptApplicationFailureRemainsTypedAndIsNotRetried(t *testing.T) {
	connection := unaryTestConnection(t)
	calls := 0
	service := &unaryEngineClient{initialize: func(context.Context, *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
		calls++
		return &enginev1.InitializeResponse{Status: &commonv1.Status{
			Code: commonv1.ErrorCode_ERROR_CODE_CONFLICT, Message: "client identity conflict",
		}}, nil
	}}
	connector := testConnector(t, service)
	_, err := connector.Initialize(t.Context(), retryInitializeRequest(t, connection, false))
	statusErr, typed := errors.AsType[*client.StatusError](err)
	if calls != 1 || !typed || statusErr.Code() != client.ErrorConflict {
		t.Fatalf("Initialize calls = %d, error = %T %v", calls, err, err)
	}
}

func retryInitializeRequest(t *testing.T, connection client.Connection, reconnect bool) client.InitializeRequest {
	t.Helper()
	protocol, err := client.NewProtocolRange(connection.Protocol(), connection.Protocol())
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := client.ParseInitializationAttemptID("00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	if !reconnect {
		request, requestErr := client.NewInitializeRequestWithAttempt(
			protocol, connection.Server(), connection.Capabilities(), connection.Capabilities(), connection.Limits(), attempt,
		)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return request
	}
	claim, err := client.NewReconnectClaim(connection.ClientID(), connection.OwnershipEpoch())
	if err != nil {
		t.Fatal(err)
	}
	request, err := client.NewReconnectRequestWithAttempt(
		protocol, connection.Server(), connection.Capabilities(), connection.Capabilities(), connection.Limits(), claim, attempt,
	)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func legacyInitializeRequest(
	t *testing.T,
	connection client.Connection,
	claim *client.ReconnectClaim,
) client.InitializeRequest {
	t.Helper()
	minimum, err := client.NewProtocolVersion(1, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	maximum, err := client.NewProtocolVersion(1, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	protocol, err := client.NewProtocolRange(minimum, maximum)
	if err != nil {
		t.Fatal(err)
	}
	if claim == nil {
		request, requestErr := client.NewLegacyInitializeRequest(
			protocol, connection.Server(), connection.Capabilities(), connection.Capabilities(), connection.Limits(),
		)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return request
	}
	request, err := client.NewLegacyReconnectRequest(
		protocol, connection.Server(), connection.Capabilities(), connection.Capabilities(), connection.Limits(), *claim,
	)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func testConnector(t *testing.T, service enginev1.EngineServiceClient) *Connector {
	t.Helper()
	token, err := endpoint.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	return &Connector{service: service, token: token, sessions: make(map[string]*session)}
}

func TestRetryClassifierIsTransientResponseLossOnly(t *testing.T) {
	t.Parallel()
	for code, want := range map[codes.Code]bool{
		codes.Unavailable:       true,
		codes.Canceled:          false,
		codes.DeadlineExceeded:  false,
		codes.Unauthenticated:   false,
		codes.PermissionDenied:  false,
		codes.InvalidArgument:   false,
		codes.ResourceExhausted: false,
		codes.Aborted:           false,
		codes.Internal:          false,
		codes.Unknown:           false,
	} {
		if got := retryableInitializeTransport(t.Context(), status.Error(code, "failure")); got != want {
			t.Fatalf("retryableInitializeTransport(%s) = %t, want %t", code, got, want)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if retryableInitializeTransport(cancelled, status.Error(codes.Unavailable, "failure")) {
		t.Fatal("cancelled context allowed initialization retry")
	}
}

func TestAdapterCallOptionsDefeatCallerConnectionRetryPolicy(t *testing.T) {
	t.Parallel()
	server := new(retryPolicyEngineServer)
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	enginev1.RegisterEngineServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})
	connection, err := grpc.NewClient(
		"passthrough:///spice-agent-retry-policy-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithDefaultServiceConfig(aggressiveRetryServiceConfig),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	token, err := endpoint.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	connector, err := New(Config{Connection: connection, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	fixture := unaryTestConnection(t)

	if _, err = connector.Initialize(t.Context(), legacyInitializeRequest(t, fixture, nil)); err == nil {
		t.Fatal("legacy unavailable initialization succeeded")
	}
	if got := server.legacyInitializations.Load(); got != 1 {
		t.Fatalf("legacy handler entries = %d, want 1", got)
	}

	if _, err = connector.Initialize(t.Context(), retryInitializeRequest(t, fixture, false)); err == nil {
		t.Fatal("protocol-1.3 unavailable initialization succeeded")
	}
	if got := server.attemptInitializations.Load(); got != 2 {
		t.Fatalf("protocol-1.3 handler entries = %d, want 2", got)
	}

	api := enginev1.NewEngineServiceClient(connection)
	_, _ = api.StartRun(t.Context(), &enginev1.StartRunRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "operation",
	}, messageCallOptions(1<<20, 1<<20)...)
	if got := server.mutations.Load(); got != 1 {
		t.Fatalf("mutation handler entries = %d, want 1", got)
	}
}

type retryPolicyEngineServer struct {
	enginev1.UnimplementedEngineServiceServer

	legacyInitializations  atomic.Int32
	attemptInitializations atomic.Int32
	mutations              atomic.Int32
}

func (server *retryPolicyEngineServer) Initialize(
	_ context.Context,
	request *enginev1.InitializeRequest,
) (*enginev1.InitializeResponse, error) {
	if len(request.GetInitializationAttemptId()) == 0 {
		server.legacyInitializations.Add(1)
	} else {
		server.attemptInitializations.Add(1)
	}
	return nil, status.Error(codes.Unavailable, "retry-policy fixture")
}

func (server *retryPolicyEngineServer) StartRun(
	context.Context,
	*enginev1.StartRunRequest,
) (*enginev1.StartRunResponse, error) {
	server.mutations.Add(1)
	return nil, status.Error(codes.Unavailable, "retry-policy fixture")
}

package grpcclient

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon/endpoint"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestSessionUnaryOperationsTranslateValidResponses(t *testing.T) {
	t.Parallel()
	connection := unaryTestConnection(t)
	envelope := unaryTestSnapshotEnvelope(t, "run-snapshot")
	service := &unaryEngineClient{
		start: func(_ context.Context, request *enginev1.StartRunRequest) (*enginev1.StartRunResponse, error) {
			if request.GetClientId() != connection.ClientID() || request.GetClientOperationId() != "operation" {
				t.Fatalf("unexpected start request: %#v", request)
			}
			return &enginev1.StartRunResponse{
				Status: commonv1.OKStatus(), RunId: "run-started", InitialSequence: 1,
				PlanId: "plan", DuplicateOperation: false,
			}, nil
		},
		cancel: func(_ context.Context, request *enginev1.CancelRunRequest) (*enginev1.CancelRunResponse, error) {
			if request.GetReason() != "test-cancel" {
				t.Fatalf("cancel reason = %q", request.GetReason())
			}
			return &enginev1.CancelRunResponse{
				Status: commonv1.OKStatus(), CancellationRequested: true,
			}, nil
		},
		respond: func(_ context.Context, request *enginev1.RespondInteractionRequest) (*enginev1.RespondInteractionResponse, error) {
			if string(request.GetValueJson()) != `"accepted"` {
				t.Fatalf("response value = %s", request.GetValueJson())
			}
			return &enginev1.RespondInteractionResponse{Status: commonv1.OKStatus(), Accepted: true}, nil
		},
		suspend: func(context.Context, *enginev1.SuspendRunRequest) (*enginev1.SuspendRunResponse, error) {
			return &enginev1.SuspendRunResponse{
				Status: commonv1.OKStatus(), Suspended: true, BoundarySequence: 4,
			}, nil
		},
		resume: func(context.Context, *enginev1.ResumeRunRequest) (*enginev1.ResumeRunResponse, error) {
			return &enginev1.ResumeRunResponse{
				Status: commonv1.OKStatus(), Resumed: true, NextSequence: 5,
			}, nil
		},
		export: func(context.Context, *enginev1.ExportSnapshotRequest) (*enginev1.ExportSnapshotResponse, error) {
			return &enginev1.ExportSnapshotResponse{Status: commonv1.OKStatus(), Snapshot: proto.CloneOf(envelope)}, nil
		},
		importSnapshot: func(_ context.Context, request *enginev1.ImportSnapshotRequest) (*enginev1.ImportSnapshotResponse, error) {
			if !proto.Equal(request.GetSnapshot(), envelope) {
				t.Fatal("import snapshot changed in transit")
			}
			return &enginev1.ImportSnapshotResponse{
				Status: commonv1.OKStatus(), RunId: "run-snapshot", NextSequence: 5,
			}, nil
		},
		health: func(context.Context, *enginev1.HealthRequest) (*enginev1.HealthResponse, error) {
			return unaryHealthResponse(t, connection), nil
		},
	}
	session := unaryTestSession(t, connection, service)
	operation := mustOperation(t, "operation")
	run := mustRun(t, "run-snapshot")

	start, err := session.Start(t.Context(), mustStartRequest(t, operation))
	if err != nil || start.Run().ID() != "run-started" || start.InitialSequence() != 1 {
		t.Fatalf("Start = %#v, %v", start, err)
	}
	cancelled, err := session.Cancel(t.Context(), mustCancelRequest(t, run, operation))
	if err != nil || !cancelled.Requested() {
		t.Fatalf("Cancel = %#v, %v", cancelled, err)
	}
	responded, err := session.Respond(t.Context(), mustRespondRequest(t, run, operation))
	if err != nil || !responded.Accepted() {
		t.Fatalf("Respond = %#v, %v", responded, err)
	}
	mutation := mustRunMutation(t, run, operation)
	suspended, err := session.Suspend(t.Context(), mutation)
	if err != nil || suspended.BoundarySequence() != 4 {
		t.Fatalf("Suspend = %#v, %v", suspended, err)
	}
	resumed, err := session.Resume(t.Context(), mutation)
	if err != nil || resumed.NextSequence() != 5 {
		t.Fatalf("Resume = %#v, %v", resumed, err)
	}
	snapshot, err := session.Export(t.Context(), run)
	if err != nil || snapshot.SizeBytes() == 0 {
		t.Fatalf("Export = %#v, %v", snapshot, err)
	}
	imported, err := session.Import(t.Context(), mustImportRequest(t, operation, snapshot))
	if err != nil || imported.Run() != run || imported.NextSequence() != 5 {
		t.Fatalf("Import = %#v, %v", imported, err)
	}
	health, err := session.Health(t.Context())
	if err != nil || health.State() != client.HealthReady {
		t.Fatalf("Health = %#v, %v", health, err)
	}
}

func TestSessionUnaryOperationsRejectInvalidInputs(t *testing.T) {
	t.Parallel()
	session := unaryTestSession(t, unaryTestConnection(t), &unaryEngineClient{})
	assertCode := func(err error) {
		t.Helper()
		statusErr, ok := errors.AsType[*client.StatusError](err)
		if !ok || statusErr.Code() != client.ErrorInvalidArgument {
			t.Fatalf("error = %T %v, want invalid argument", err, err)
		}
	}
	_, err := session.Start(t.Context(), client.StartRequest{})
	assertCode(err)
	_, err = session.Cancel(t.Context(), client.CancelRequest{})
	assertCode(err)
	_, err = session.Respond(t.Context(), client.RespondRequest{})
	assertCode(err)
	_, err = session.Suspend(t.Context(), client.RunMutation{})
	assertCode(err)
	_, err = session.Resume(t.Context(), client.RunMutation{})
	assertCode(err)
	_, err = session.Export(t.Context(), client.RunRef{})
	assertCode(err)
	_, err = session.Import(t.Context(), client.ImportRequest{})
	assertCode(err)
	_, err = session.Health(nil) //nolint:staticcheck // Boundary verifies that the public adapter rejects nil contexts.
	assertCode(err)

	cancelled, cancel := context.WithCancelCause(t.Context())
	cancel(errors.New("caller stopped"))
	if _, err = session.Health(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Health error = %v", err)
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = session.Health(t.Context()); !errors.Is(err, client.ErrClosed) {
		t.Fatalf("closed Health error = %v", err)
	}
}

func TestSessionMutationTransportOutcomesAreExplicit(t *testing.T) {
	t.Parallel()
	connection := unaryTestConnection(t)
	operation := mustOperation(t, "transport-operation")
	run := mustRun(t, "run-transport")
	tests := []struct {
		name      string
		code      codes.Code
		want      client.ErrorCode
		uncertain bool
	}{
		{name: "authentication", code: codes.Unauthenticated, want: client.ErrorUnauthenticated},
		{name: "known rejection", code: codes.InvalidArgument, want: client.ErrorInvalidArgument},
		{name: "lost response", code: codes.Unavailable, uncertain: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &unaryEngineClient{cancel: func(context.Context, *enginev1.CancelRunRequest) (*enginev1.CancelRunResponse, error) {
				return nil, status.Error(test.code, "unsafe transport detail")
			}}
			session := unaryTestSession(t, connection, service)
			_, err := session.Cancel(t.Context(), mustCancelRequest(t, run, operation))
			if test.uncertain {
				uncertain, ok := errors.AsType[*client.UncertainOperationError](err)
				if !ok {
					t.Fatalf("error = %T %v, want correlated uncertain mutation", err, err)
				}
				correlated, hasOperation := uncertain.Operation()
				if !hasOperation || correlated != operation || uncertain.Kind() != "cancel" {
					t.Fatalf("error = %T %v, want correlated uncertain mutation", err, err)
				}
				return
			}
			statusErr, ok := errors.AsType[*client.StatusError](err)
			if !ok || statusErr.Code() != test.want {
				t.Fatalf("error = %T %v, want %s", err, err, test.want)
			}
		})
	}
}

func TestSessionRejectsMalformedUnaryResponse(t *testing.T) {
	t.Parallel()
	service := &unaryEngineClient{resume: func(context.Context, *enginev1.ResumeRunRequest) (*enginev1.ResumeRunResponse, error) {
		return &enginev1.ResumeRunResponse{Status: commonv1.OKStatus(), Resumed: true}, nil
	}}
	session := unaryTestSession(t, unaryTestConnection(t), service)
	_, err := session.Resume(t.Context(), mustRunMutation(t, mustRun(t, "run"), mustOperation(t, "operation")))
	statusErr, ok := errors.AsType[*client.StatusError](err)
	if !ok || statusErr.Code() != client.ErrorInternal {
		t.Fatalf("malformed response error = %T %v", err, err)
	}
}

func TestSessionRejectsMalformedResponsesForEveryUnaryContract(t *testing.T) {
	t.Parallel()
	connection := unaryTestConnection(t)
	service := &unaryEngineClient{
		start: func(context.Context, *enginev1.StartRunRequest) (*enginev1.StartRunResponse, error) {
			return &enginev1.StartRunResponse{Status: commonv1.OKStatus()}, nil
		},
		cancel: func(context.Context, *enginev1.CancelRunRequest) (*enginev1.CancelRunResponse, error) {
			return &enginev1.CancelRunResponse{Status: commonv1.OKStatus()}, nil
		},
		respond: func(context.Context, *enginev1.RespondInteractionRequest) (*enginev1.RespondInteractionResponse, error) {
			return &enginev1.RespondInteractionResponse{Status: commonv1.OKStatus()}, nil
		},
		suspend: func(context.Context, *enginev1.SuspendRunRequest) (*enginev1.SuspendRunResponse, error) {
			return &enginev1.SuspendRunResponse{Status: commonv1.OKStatus()}, nil
		},
		export: func(context.Context, *enginev1.ExportSnapshotRequest) (*enginev1.ExportSnapshotResponse, error) {
			return &enginev1.ExportSnapshotResponse{Status: commonv1.OKStatus()}, nil
		},
		importSnapshot: func(context.Context, *enginev1.ImportSnapshotRequest) (*enginev1.ImportSnapshotResponse, error) {
			return &enginev1.ImportSnapshotResponse{Status: commonv1.OKStatus()}, nil
		},
		health: func(context.Context, *enginev1.HealthRequest) (*enginev1.HealthResponse, error) {
			return &enginev1.HealthResponse{Status: commonv1.OKStatus()}, nil
		},
	}
	session := unaryTestSession(t, connection, service)
	operation := mustOperation(t, "operation")
	run := mustRun(t, "run")
	snapshot, err := snapshotFromWire(unaryTestSnapshotEnvelope(t, "run"))
	if err != nil {
		t.Fatal(err)
	}
	assertInternal := func(err error) {
		t.Helper()
		if errorCode(err) != client.ErrorInternal {
			t.Fatalf("malformed response error = %T %v", err, err)
		}
	}
	_, err = session.Start(t.Context(), mustStartRequest(t, operation))
	assertInternal(err)
	_, err = session.Cancel(t.Context(), mustCancelRequest(t, run, operation))
	assertInternal(err)
	_, err = session.Respond(t.Context(), mustRespondRequest(t, run, operation))
	assertInternal(err)
	_, err = session.Suspend(t.Context(), mustRunMutation(t, run, operation))
	assertInternal(err)
	_, err = session.Export(t.Context(), run)
	assertInternal(err)
	_, err = session.Import(t.Context(), mustImportRequest(t, operation, snapshot))
	assertInternal(err)
	_, err = session.Health(t.Context())
	assertInternal(err)
}

func TestInitializeTransportLossIsFailClosedForFreshAndReconnect(t *testing.T) {
	t.Parallel()
	connection := unaryTestConnection(t)
	token, err := endpoint.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	service := &unaryEngineClient{initialize: func(context.Context, *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
		return nil, status.Error(codes.Unavailable, "response was lost after commit")
	}}
	connector := &Connector{service: service, token: token, sessions: make(map[string]*session)}
	fresh := mustInitializeRequest(t, connection, nil)
	_, err = connector.Initialize(t.Context(), fresh)
	assertNonretryableUnavailable(t, err, "fresh")

	claim, err := client.NewReconnectClaim(connection.ClientID(), connection.OwnershipEpoch())
	if err != nil {
		t.Fatal(err)
	}
	reconnect := mustInitializeRequest(t, connection, &claim)
	_, err = connector.Initialize(t.Context(), reconnect)
	assertNonretryableUnavailable(t, err, "reconnect")
}

func TestInitializeDefinitiveTransportRejectionsRemainTyped(t *testing.T) {
	t.Parallel()
	connection := unaryTestConnection(t)
	for name, code := range map[string]codes.Code{
		"authentication":  codes.PermissionDenied,
		"invalid request": codes.InvalidArgument,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			token, err := endpoint.GenerateToken()
			if err != nil {
				t.Fatal(err)
			}
			service := &unaryEngineClient{initialize: func(context.Context, *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
				return nil, status.Error(code, "definitive rejection")
			}}
			connector := &Connector{service: service, token: token, sessions: make(map[string]*session)}
			claim, claimErr := client.NewReconnectClaim(connection.ClientID(), connection.OwnershipEpoch())
			if claimErr != nil {
				t.Fatal(claimErr)
			}
			for requestKind, claimValue := range map[string]*client.ReconnectClaim{
				"fresh": nil, "reconnect": &claim,
			} {
				_, err = connector.Initialize(t.Context(), mustInitializeRequest(t, connection, claimValue))
				statusErr, ok := errors.AsType[*client.StatusError](err)
				if !ok || statusErr.Retryable() {
					t.Fatalf("%s error = %T %v, want non-retryable status", requestKind, err, err)
				}
				if code == codes.PermissionDenied && statusErr.Code() != client.ErrorUnauthenticated {
					t.Fatalf("%s code = %s, want unauthenticated", requestKind, statusErr.Code())
				}
				if code == codes.InvalidArgument && statusErr.Code() != client.ErrorInvalidArgument {
					t.Fatalf("%s code = %s, want invalid argument", requestKind, statusErr.Code())
				}
			}
		})
	}
}

func TestConnectorConstructionAndInitializeBoundaries(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{}); err == nil {
		t.Fatal("nil connection was accepted")
	}
	if _, err := New(Config{Connection: new(grpc.ClientConn)}); err == nil {
		t.Fatal("invalid endpoint token was accepted")
	}
	connection := unaryTestConnection(t)
	request := mustInitializeRequest(t, connection, nil)
	var nilConnector *Connector
	if _, err := nilConnector.Initialize(t.Context(), request); errorCode(err) != client.ErrorUnavailable {
		t.Fatalf("nil connector = %v", err)
	}
	token, err := endpoint.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	service := &unaryEngineClient{initialize: func(context.Context, *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
		return &enginev1.InitializeResponse{Status: commonv1.OKStatus()}, nil
	}}
	connector := &Connector{service: service, token: token, sessions: make(map[string]*session)}
	if _, err = connector.Initialize(nil, request); //nolint:staticcheck // Boundary verifies nil-context rejection.
	errorCode(err) != client.ErrorInvalidArgument {
		t.Fatalf("nil initialize context = %v", err)
	}
	if _, err = connector.Initialize(t.Context(), client.InitializeRequest{}); errorCode(err) != client.ErrorInvalidArgument {
		t.Fatalf("invalid initialize request = %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = connector.Initialize(cancelled, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled initialize = %v", err)
	}
	if _, err = connector.Initialize(t.Context(), request); errorCode(err) != client.ErrorInternal {
		t.Fatalf("malformed initialize response = %v", err)
	}
}

type unaryEngineClient struct {
	initialize     func(context.Context, *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error)
	start          func(context.Context, *enginev1.StartRunRequest) (*enginev1.StartRunResponse, error)
	cancel         func(context.Context, *enginev1.CancelRunRequest) (*enginev1.CancelRunResponse, error)
	respond        func(context.Context, *enginev1.RespondInteractionRequest) (*enginev1.RespondInteractionResponse, error)
	suspend        func(context.Context, *enginev1.SuspendRunRequest) (*enginev1.SuspendRunResponse, error)
	resume         func(context.Context, *enginev1.ResumeRunRequest) (*enginev1.ResumeRunResponse, error)
	export         func(context.Context, *enginev1.ExportSnapshotRequest) (*enginev1.ExportSnapshotResponse, error)
	importSnapshot func(context.Context, *enginev1.ImportSnapshotRequest) (*enginev1.ImportSnapshotResponse, error)
	health         func(context.Context, *enginev1.HealthRequest) (*enginev1.HealthResponse, error)
}

func (service *unaryEngineClient) Initialize(ctx context.Context, request *enginev1.InitializeRequest, _ ...grpc.CallOption) (*enginev1.InitializeResponse, error) {
	if service.initialize == nil {
		return nil, status.Error(codes.Unimplemented, "initialize")
	}
	return service.initialize(ctx, request)
}

func (service *unaryEngineClient) Health(ctx context.Context, request *enginev1.HealthRequest, _ ...grpc.CallOption) (*enginev1.HealthResponse, error) {
	if service.health == nil {
		return nil, status.Error(codes.Unimplemented, "health")
	}
	return service.health(ctx, request)
}

func (service *unaryEngineClient) StartRun(ctx context.Context, request *enginev1.StartRunRequest, _ ...grpc.CallOption) (*enginev1.StartRunResponse, error) {
	if service.start == nil {
		return nil, status.Error(codes.Unimplemented, "start")
	}
	return service.start(ctx, request)
}

func (*unaryEngineClient) StreamEvents(context.Context, *enginev1.StreamEventsRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[enginev1.StreamEventsResponse], error) {
	return nil, status.Error(codes.Unimplemented, "events")
}

func (*unaryEngineClient) StreamInteractions(context.Context, *enginev1.StreamInteractionsRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[enginev1.StreamInteractionsResponse], error) {
	return nil, status.Error(codes.Unimplemented, "interactions")
}

func (service *unaryEngineClient) CancelRun(ctx context.Context, request *enginev1.CancelRunRequest, _ ...grpc.CallOption) (*enginev1.CancelRunResponse, error) {
	if service.cancel == nil {
		return nil, status.Error(codes.Unimplemented, "cancel")
	}
	return service.cancel(ctx, request)
}

func (service *unaryEngineClient) RespondInteraction(ctx context.Context, request *enginev1.RespondInteractionRequest, _ ...grpc.CallOption) (*enginev1.RespondInteractionResponse, error) {
	if service.respond == nil {
		return nil, status.Error(codes.Unimplemented, "respond")
	}
	return service.respond(ctx, request)
}

func (service *unaryEngineClient) SuspendRun(ctx context.Context, request *enginev1.SuspendRunRequest, _ ...grpc.CallOption) (*enginev1.SuspendRunResponse, error) {
	if service.suspend == nil {
		return nil, status.Error(codes.Unimplemented, "suspend")
	}
	return service.suspend(ctx, request)
}

func (service *unaryEngineClient) ResumeRun(ctx context.Context, request *enginev1.ResumeRunRequest, _ ...grpc.CallOption) (*enginev1.ResumeRunResponse, error) {
	if service.resume == nil {
		return nil, status.Error(codes.Unimplemented, "resume")
	}
	return service.resume(ctx, request)
}

func (service *unaryEngineClient) ExportSnapshot(ctx context.Context, request *enginev1.ExportSnapshotRequest, _ ...grpc.CallOption) (*enginev1.ExportSnapshotResponse, error) {
	if service.export == nil {
		return nil, status.Error(codes.Unimplemented, "export")
	}
	return service.export(ctx, request)
}

func (service *unaryEngineClient) ImportSnapshot(ctx context.Context, request *enginev1.ImportSnapshotRequest, _ ...grpc.CallOption) (*enginev1.ImportSnapshotResponse, error) {
	if service.importSnapshot == nil {
		return nil, status.Error(codes.Unimplemented, "import")
	}
	return service.importSnapshot(ctx, request)
}

func unaryTestSession(t *testing.T, connection client.Connection, service enginev1.EngineServiceClient) *session {
	t.Helper()
	token, err := endpoint.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	return &session{service: service, token: token, connection: connection, streams: make(map[*streamLifetime]struct{})}
}

func unaryTestConnection(t *testing.T) client.Connection {
	t.Helper()
	version, err := client.NewProtocolVersion(commonv1.ProtocolMajor, commonv1.ProtocolMinor, commonv1.ProtocolPatch)
	if err != nil {
		t.Fatal(err)
	}
	build, err := client.NewBuild("spice-agentd", "test", "commit", "go1.26.5")
	if err != nil {
		t.Fatal(err)
	}
	limits, err := client.NewLimits(2<<20, 64, 32, 2<<20, 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	health, err := client.NewHealth(client.HealthReady, nil, 0, limits)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := client.NewDefinitionRef("definition", "revision")
	if err != nil {
		t.Fatal(err)
	}
	definition, err := client.NewDefinition(reference, "model", 4)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := client.NewCatalog("catalog", []client.Definition{definition}, limits)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := client.NewConnection(client.ConnectionSpec{
		Protocol: version, Server: build, Capabilities: []string{"events", "interactions", "snapshots"},
		Limits: limits, Health: health, ClientID: "client", OwnershipEpoch: 1, Catalog: catalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func unaryHealthResponse(t *testing.T, connection client.Connection) *enginev1.HealthResponse {
	t.Helper()
	build, err := buildToWire(connection.Server())
	if err != nil {
		t.Fatal(err)
	}
	protocol, err := protocolVersionToWire(connection.Protocol())
	if err != nil {
		t.Fatal(err)
	}
	limits, err := limitsToWire(connection.Limits())
	if err != nil {
		t.Fatal(err)
	}
	return &enginev1.HealthResponse{
		Status: commonv1.OKStatus(), Server: build, Protocol: protocol,
		Health: &commonv1.Health{State: commonv1.HealthState_HEALTH_STATE_READY, Limits: limits},
	}
}

func unaryTestSnapshotEnvelope(t *testing.T, runID string) *enginev1.SnapshotEnvelope {
	t.Helper()
	payload := []byte(`{"run_id":"` + runID + `"}`)
	digest := sha256.Sum256(payload)
	value := &enginev1.SnapshotEnvelope{
		Format: enginev1.SnapshotFormat, RunId: runID, LastSequence: 4,
		Lifecycle: enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED,
		Payload:   payload, Sha256: digest[:],
		Authority: &enginev1.SnapshotAuthority{ScopeId: make([]byte, 32), Generation: 1, HmacSha256: make([]byte, 32)},
	}
	if err := enginev1.ValidateSnapshotEnvelope(value); err != nil {
		t.Fatalf("snapshot fixture: %v", err)
	}
	return value
}

func mustOperation(t *testing.T, value string) client.OperationID {
	t.Helper()
	result, err := client.NewOperationID(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustRun(t *testing.T, value string) client.RunRef {
	t.Helper()
	result, err := client.NewRunRef(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustStartRequest(t *testing.T, operation client.OperationID) client.StartRequest {
	t.Helper()
	definition, err := client.NewDefinitionRef("definition", "revision")
	if err != nil {
		t.Fatal(err)
	}
	input, err := client.NewInput("message", "hello")
	if err != nil {
		t.Fatal(err)
	}
	request, err := client.NewStartRequest(operation, definition, input)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func mustCancelRequest(t *testing.T, run client.RunRef, operation client.OperationID) client.CancelRequest {
	t.Helper()
	request, err := client.NewCancelRequest(run, operation, "test-cancel")
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func mustRespondRequest(t *testing.T, run client.RunRef, operation client.OperationID) client.RespondRequest {
	t.Helper()
	value, err := client.NewStructuredText("accepted")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.NewInteractionResponse("interaction", value)
	if err != nil {
		t.Fatal(err)
	}
	request, err := client.NewRespondRequest(run, operation, response)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func mustRunMutation(t *testing.T, run client.RunRef, operation client.OperationID) client.RunMutation {
	t.Helper()
	request, err := client.NewRunMutation(run, operation)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func mustImportRequest(t *testing.T, operation client.OperationID, snapshot client.Snapshot) client.ImportRequest {
	t.Helper()
	request, err := client.NewImportRequest(operation, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func mustInitializeRequest(
	t *testing.T,
	connection client.Connection,
	claim *client.ReconnectClaim,
) client.InitializeRequest {
	t.Helper()
	protocol, err := client.NewProtocolRange(connection.Protocol(), connection.Protocol())
	if err != nil {
		t.Fatal(err)
	}
	if claim == nil {
		request, requestErr := client.NewInitializeRequest(
			protocol, connection.Server(), connection.Capabilities(), connection.Capabilities(), connection.Limits(),
		)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return request
	}
	request, err := client.NewReconnectRequest(
		protocol, connection.Server(), connection.Capabilities(), connection.Capabilities(), connection.Limits(), *claim,
	)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func assertNonretryableUnavailable(t *testing.T, err error, operation string) {
	t.Helper()
	statusErr, ok := errors.AsType[*client.StatusError](err)
	if !ok || statusErr.Code() != client.ErrorUnavailable || statusErr.Retryable() {
		t.Fatalf("%s error = %T %v, want non-retryable unavailable", operation, err, err)
	}
}

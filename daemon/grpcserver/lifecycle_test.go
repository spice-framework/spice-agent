package grpcserver

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestLifecycleUnaryRPCsRoundTripOverAuthenticatedGRPC(t *testing.T) {
	t.Parallel()
	envelope, snapshot := lifecycleSnapshotFixture(t, "run-lifecycle")
	host := lifecycleGRPCHostFixture(t)
	host.snapshot = snapshot
	api, ctx, initialized := initializeLifecycleGRPC(t, host, []string{
		"events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots",
	})
	clientID, epoch := initialized.GetClientId(), initialized.GetOwnershipEpoch()

	start, err := api.StartRun(ctx, &enginev1.StartRunRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-start",
		Definition: &enginev1.AgentDefinitionRef{Id: "default", Revision: "rev-1"},
		Input: &enginev1.Message{
			Id: "message-1", Role: enginev1.MessageRole_MESSAGE_ROLE_USER,
			Parts: []*enginev1.ContentPart{{Value: &enginev1.ContentPart_Text{Text: "inspect the repository"}}},
		},
	})
	if err != nil || enginev1.ValidateStartRunResponse(start, initialized.GetLimits()) != nil ||
		start.GetRunId() != "run-lifecycle" || host.startInput != "inspect the repository" {
		t.Fatalf("start = %#v, %v, input %q", start, err, host.startInput)
	}

	cancel, err := api.CancelRun(ctx, &enginev1.CancelRunRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-cancel",
		RunId: start.GetRunId(), Reason: "caller requested",
	})
	if err != nil || enginev1.ValidateCancelRunResponse(cancel, initialized.GetLimits()) != nil ||
		!cancel.GetCancellationRequested() {
		t.Fatalf("cancel = %#v, %v", cancel, err)
	}

	respond, err := api.RespondInteraction(ctx, &enginev1.RespondInteractionRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-respond",
		RunId: start.GetRunId(), InteractionId: "approval-1", ValueJson: []byte("true"),
	})
	if err != nil || enginev1.ValidateRespondInteractionResponse(respond, initialized.GetLimits()) != nil ||
		!respond.GetAccepted() {
		t.Fatalf("respond = %#v, %v", respond, err)
	}

	suspended, err := api.SuspendRun(ctx, &enginev1.SuspendRunRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-suspend", RunId: start.GetRunId(),
	})
	if err != nil || enginev1.ValidateSuspendRunResponse(suspended, initialized.GetLimits()) != nil ||
		suspended.GetBoundarySequence() != 8 {
		t.Fatalf("suspend = %#v, %v", suspended, err)
	}

	resumed, err := api.ResumeRun(ctx, &enginev1.ResumeRunRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-resume", RunId: start.GetRunId(),
	})
	if err != nil || enginev1.ValidateResumeRunResponse(resumed, initialized.GetLimits()) != nil ||
		resumed.GetNextSequence() != 9 {
		t.Fatalf("resume = %#v, %v", resumed, err)
	}

	exported, err := api.ExportSnapshot(ctx, &enginev1.ExportSnapshotRequest{
		ClientId: clientID, OwnershipEpoch: epoch, RunId: start.GetRunId(),
	})
	if err != nil || enginev1.ValidateExportSnapshotResponse(exported, initialized.GetLimits()) != nil ||
		!proto.Equal(exported.GetSnapshot(), envelope) {
		t.Fatalf("export = %#v, %v", exported, err)
	}

	imported, err := api.ImportSnapshot(ctx, &enginev1.ImportSnapshotRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-import", Snapshot: envelope,
	})
	if err != nil || enginev1.ValidateImportSnapshotResponse(imported, initialized.GetLimits()) != nil ||
		imported.GetRunId() != "run-lifecycle" || imported.GetNextSequence() != 9 {
		t.Fatalf("import = %#v, %v", imported, err)
	}
	if got := host.calls.Load(); got != 7 {
		t.Fatalf("host lifecycle calls = %d", got)
	}
}

func TestLifecycleUnaryRPCsRejectUnsupportedInputAndCapabilitiesBeforeHost(t *testing.T) {
	t.Parallel()
	host := lifecycleGRPCHostFixture(t)
	api, ctx, initialized := initializeLifecycleGRPC(t, host, []string{"events"})
	clientID, epoch := initialized.GetClientId(), initialized.GetOwnershipEpoch()

	start, err := api.StartRun(ctx, &enginev1.StartRunRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-rich",
		Definition: &enginev1.AgentDefinitionRef{Id: "default", Revision: "rev-1"},
		Input: &enginev1.Message{
			Id: "message-rich", Role: enginev1.MessageRole_MESSAGE_ROLE_USER,
			Parts: []*enginev1.ContentPart{
				{Value: &enginev1.ContentPart_Text{Text: "first"}},
				{Value: &enginev1.ContentPart_Text{Text: "second"}},
			},
		},
	})
	if err != nil || start.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("unsupported rich start = %#v, %v", start, err)
	}

	exported, err := api.ExportSnapshot(ctx, &enginev1.ExportSnapshotRequest{
		ClientId: clientID, OwnershipEpoch: epoch, RunId: "run-lifecycle",
	})
	if err != nil || exported.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_MISSING_CAPABILITY ||
		exported.GetStatus().GetCapabilityMismatch() == nil {
		t.Fatalf("unnegotiated export = %#v, %v", exported, err)
	}
	if invalid, callErr := api.CancelRun(ctx, &enginev1.CancelRunRequest{}); callErr != nil ||
		invalid.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("invalid cancel = %#v, %v", invalid, callErr)
	}
	if invalid, callErr := api.RespondInteraction(ctx, &enginev1.RespondInteractionRequest{}); callErr != nil ||
		invalid.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("invalid response = %#v, %v", invalid, callErr)
	}
	if invalid, callErr := api.SuspendRun(ctx, &enginev1.SuspendRunRequest{}); callErr != nil ||
		invalid.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("invalid suspend = %#v, %v", invalid, callErr)
	}
	if invalid, callErr := api.ResumeRun(ctx, &enginev1.ResumeRunRequest{}); callErr != nil ||
		invalid.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("invalid resume = %#v, %v", invalid, callErr)
	}
	if invalid, callErr := api.ImportSnapshot(ctx, &enginev1.ImportSnapshotRequest{}); callErr != nil ||
		invalid.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("invalid import = %#v, %v", invalid, callErr)
	}
	if got := host.calls.Load(); got != 0 {
		t.Fatalf("rejected requests reached host %d times", got)
	}
}

func TestLifecycleUnaryRPCsFailClosedForStaleMalformedAndHostFailures(t *testing.T) {
	t.Parallel()
	host := lifecycleGRPCHostFixture(t)
	host.startErr = hostCapacityStatusFixture{resource: "active runs", limit: 2, observed: 3}
	api, ctx, initialized := initializeLifecycleGRPC(t, host, []string{
		"events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots",
	})
	request := &enginev1.StartRunRequest{
		ClientId: initialized.GetClientId(), OwnershipEpoch: initialized.GetOwnershipEpoch(),
		ClientOperationId: "op-capacity", Definition: &enginev1.AgentDefinitionRef{Id: "default", Revision: "rev-1"},
		Input: &enginev1.Message{
			Id: "message-capacity", Role: enginev1.MessageRole_MESSAGE_ROLE_USER,
			Parts: []*enginev1.ContentPart{{Value: &enginev1.ContentPart_Text{Text: "start"}}},
		},
	}
	overload, err := api.StartRun(ctx, request)
	if err != nil || overload.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED ||
		overload.GetStatus().GetOverload().GetObserved() != 3 {
		t.Fatalf("typed host overload = %#v, %v", overload, err)
	}

	request.OwnershipEpoch++
	stale, err := api.StartRun(ctx, request)
	if err != nil || stale.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_STALE_CLIENT ||
		stale.GetStatus().GetStaleClient().GetExpectedEpoch() != initialized.GetOwnershipEpoch() {
		t.Fatalf("stale lifecycle request = %#v, %v", stale, err)
	}

	envelope, _ := lifecycleSnapshotFixture(t, "run-malformed")
	unknown := protowire.AppendTag(nil, 100, protowire.VarintType)
	envelope.ProtoReflect().SetUnknown(protowire.AppendVarint(unknown, 1))
	malformed, err := api.ImportSnapshot(ctx, &enginev1.ImportSnapshotRequest{
		ClientId: initialized.GetClientId(), OwnershipEpoch: initialized.GetOwnershipEpoch(),
		ClientOperationId: "op-malformed", Snapshot: envelope,
	})
	if err != nil || malformed.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("unsigned envelope extension = %#v, %v", malformed, err)
	}
	if got := host.calls.Load(); got != 1 {
		t.Fatalf("rejected lifecycle requests reached host, calls %d", got)
	}
}

func TestLifecycleUnaryCancellationRemainsTransportOnly(t *testing.T) {
	t.Parallel()
	host := lifecycleGRPCHostFixture(t)
	host.startErr = context.Canceled
	api, ctx, initialized := initializeLifecycleGRPC(t, host, []string{"events"})
	response, err := api.StartRun(ctx, &enginev1.StartRunRequest{
		ClientId: initialized.GetClientId(), OwnershipEpoch: initialized.GetOwnershipEpoch(),
		ClientOperationId: "op-cancelled-start",
		Definition:        &enginev1.AgentDefinitionRef{Id: "default", Revision: "rev-1"},
		Input: &enginev1.Message{
			Id: "message-cancelled", Role: enginev1.MessageRole_MESSAGE_ROLE_USER,
			Parts: []*enginev1.ContentPart{{Value: &enginev1.ContentPart_Text{Text: "start"}}},
		},
	})
	if response != nil || status.Code(err) != codes.Canceled {
		t.Fatalf("canceled start = %#v, %v", response, err)
	}
}

func TestLifecycleUnaryApplicationFailuresRemainResponseStatuses(t *testing.T) {
	t.Parallel()
	envelope, _ := lifecycleSnapshotFixture(t, "run-failure")
	host := lifecycleGRPCHostFixture(t)
	host.cancelErr = daemon.ErrRunHostState
	host.respondErr = daemon.ErrRunHostState
	host.suspendErr = daemon.ErrRunHostState
	host.resumeErr = daemon.ErrRunHostState
	host.exportErr = daemon.ErrRunHostState
	host.importErr = daemon.ErrRunHostState
	api, ctx, initialized := initializeLifecycleGRPC(t, host, []string{
		"events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots",
	})
	clientID, epoch := initialized.GetClientId(), initialized.GetOwnershipEpoch()

	cancel, err := api.CancelRun(ctx, &enginev1.CancelRunRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-failed-cancel", RunId: "run-failure",
	})
	assertLifecycleConflict(t, cancel.GetStatus(), err)
	respond, err := api.RespondInteraction(ctx, &enginev1.RespondInteractionRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-failed-respond",
		RunId: "run-failure", InteractionId: "interaction-failure", ValueJson: []byte("null"),
	})
	assertLifecycleConflict(t, respond.GetStatus(), err)
	suspended, err := api.SuspendRun(ctx, &enginev1.SuspendRunRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-failed-suspend", RunId: "run-failure",
	})
	assertLifecycleConflict(t, suspended.GetStatus(), err)
	resumed, err := api.ResumeRun(ctx, &enginev1.ResumeRunRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-failed-resume", RunId: "run-failure",
	})
	assertLifecycleConflict(t, resumed.GetStatus(), err)
	exported, err := api.ExportSnapshot(ctx, &enginev1.ExportSnapshotRequest{
		ClientId: clientID, OwnershipEpoch: epoch, RunId: "run-failure",
	})
	assertLifecycleConflict(t, exported.GetStatus(), err)
	imported, err := api.ImportSnapshot(ctx, &enginev1.ImportSnapshotRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-failed-import", Snapshot: envelope,
	})
	assertLifecycleConflict(t, imported.GetStatus(), err)
	if got := host.calls.Load(); got != 6 {
		t.Fatalf("failed lifecycle host calls = %d", got)
	}
}

func TestLifecycleUnaryInvalidHostResultsFailClosed(t *testing.T) {
	t.Parallel()
	envelope, snapshot := lifecycleSnapshotFixture(t, "run-invalid-result")
	host := lifecycleGRPCHostFixture(t)
	host.snapshot = snapshot
	host.invalidResults = true
	api, ctx, initialized := initializeLifecycleGRPC(t, host, []string{
		"events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots",
	})
	clientID, epoch := initialized.GetClientId(), initialized.GetOwnershipEpoch()

	started, err := api.StartRun(ctx, &enginev1.StartRunRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-invalid-start",
		Definition: &enginev1.AgentDefinitionRef{Id: "default", Revision: "rev-1"},
		Input: &enginev1.Message{
			Id: "message-invalid", Role: enginev1.MessageRole_MESSAGE_ROLE_USER,
			Parts: []*enginev1.ContentPart{{Value: &enginev1.ContentPart_Text{Text: "start"}}},
		},
	})
	assertLifecycleInternal(t, started.GetStatus(), err)
	canceled, err := api.CancelRun(ctx, &enginev1.CancelRunRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-invalid-cancel", RunId: "run-invalid-result",
	})
	assertLifecycleInternal(t, canceled.GetStatus(), err)
	responded, err := api.RespondInteraction(ctx, &enginev1.RespondInteractionRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-invalid-respond",
		RunId: "run-invalid-result", InteractionId: "interaction-invalid", ValueJson: []byte("null"),
	})
	assertLifecycleInternal(t, responded.GetStatus(), err)
	suspended, err := api.SuspendRun(ctx, &enginev1.SuspendRunRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-invalid-suspend", RunId: "run-invalid-result",
	})
	assertLifecycleInternal(t, suspended.GetStatus(), err)
	resumed, err := api.ResumeRun(ctx, &enginev1.ResumeRunRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-invalid-resume", RunId: "run-invalid-result",
	})
	assertLifecycleInternal(t, resumed.GetStatus(), err)
	exported, err := api.ExportSnapshot(ctx, &enginev1.ExportSnapshotRequest{
		ClientId: clientID, OwnershipEpoch: epoch, RunId: "run-invalid-result",
	})
	assertLifecycleInternal(t, exported.GetStatus(), err)
	imported, err := api.ImportSnapshot(ctx, &enginev1.ImportSnapshotRequest{
		ClientId: clientID, OwnershipEpoch: epoch, ClientOperationId: "op-invalid-import", Snapshot: envelope,
	})
	assertLifecycleInternal(t, imported.GetStatus(), err)
	if got := host.calls.Load(); got != 7 {
		t.Fatalf("invalid-result lifecycle host calls = %d", got)
	}
}

type lifecycleGRPCHost struct {
	*grpcFixtureHost
	calls          atomic.Int32
	startErr       error
	cancelErr      error
	respondErr     error
	suspendErr     error
	resumeErr      error
	exportErr      error
	importErr      error
	startInput     string
	snapshot       client.Snapshot
	invalidResults bool
}

func lifecycleGRPCHostFixture(t *testing.T) *lifecycleGRPCHost {
	t.Helper()
	_, _, health, definitions := wireFixtureValues(t)
	description, err := daemon.NewRunHostDescription(definitions, health)
	if err != nil {
		t.Fatal(err)
	}
	return &lifecycleGRPCHost{grpcFixtureHost: &grpcFixtureHost{description: description, health: health}}
}

func (host *lifecycleGRPCHost) Start(
	ctx context.Context,
	_ daemon.Session,
	request client.StartRequest,
) (client.StartResult, error) {
	if err := ctx.Err(); err != nil {
		return client.StartResult{}, err
	}
	host.calls.Add(1)
	host.startInput = request.Input().Text()
	if host.startErr != nil {
		return client.StartResult{}, host.startErr
	}
	if host.invalidResults {
		return client.StartResult{}, nil
	}
	run, _ := client.NewRunRef("run-lifecycle")
	return client.NewStartResult(run, 1, "plan-lifecycle", false)
}

func (host *lifecycleGRPCHost) Cancel(
	context.Context, daemon.Session, client.CancelRequest,
) (client.CancelResult, error) {
	host.calls.Add(1)
	if host.cancelErr != nil {
		return client.CancelResult{}, host.cancelErr
	}
	if host.invalidResults {
		return client.CancelResult{}, nil
	}
	return client.NewCancelResult(true, false, 0)
}

func (host *lifecycleGRPCHost) Respond(
	_ context.Context,
	_ daemon.Session,
	request client.RespondRequest,
) (client.RespondResult, error) {
	host.calls.Add(1)
	if host.respondErr != nil {
		return client.RespondResult{}, host.respondErr
	}
	if host.invalidResults {
		return client.RespondResult{}, nil
	}
	if value, ok := request.Response().Value().Bool(); !ok || !value {
		return client.RespondResult{}, errors.New("fixture received wrong structured response")
	}
	return client.NewRespondResult(true, false)
}

func (host *lifecycleGRPCHost) Suspend(
	context.Context, daemon.Session, client.RunMutation,
) (client.SuspendResult, error) {
	host.calls.Add(1)
	if host.suspendErr != nil {
		return client.SuspendResult{}, host.suspendErr
	}
	if host.invalidResults {
		return client.SuspendResult{}, nil
	}
	return client.NewSuspendResult(true, 8, false)
}

func (host *lifecycleGRPCHost) Resume(
	context.Context, daemon.Session, client.RunMutation,
) (client.ResumeResult, error) {
	host.calls.Add(1)
	if host.resumeErr != nil {
		return client.ResumeResult{}, host.resumeErr
	}
	if host.invalidResults {
		return client.ResumeResult{}, nil
	}
	return client.NewResumeResult(true, 9, false)
}

func (host *lifecycleGRPCHost) Export(
	context.Context, daemon.Session, client.RunRef,
) (client.Snapshot, error) {
	host.calls.Add(1)
	if host.exportErr != nil {
		return client.Snapshot{}, host.exportErr
	}
	if host.invalidResults {
		return client.Snapshot{}, nil
	}
	return host.snapshot, nil
}

func (host *lifecycleGRPCHost) Import(
	_ context.Context,
	_ daemon.Session,
	request client.ImportRequest,
) (client.ImportResult, error) {
	host.calls.Add(1)
	if host.importErr != nil {
		return client.ImportResult{}, host.importErr
	}
	if host.invalidResults {
		return client.ImportResult{}, nil
	}
	encoded, err := request.Snapshot().MarshalBinary()
	if err != nil || !bytes.Contains(encoded, []byte("run-lifecycle")) {
		return client.ImportResult{}, errors.New("fixture received wrong snapshot")
	}
	run, _ := client.NewRunRef("run-lifecycle")
	return client.NewImportResult(run, 9, false)
}

func initializeLifecycleGRPC(
	t *testing.T,
	host runHostBoundary,
	capabilities []string,
) (enginev1.EngineServiceClient, context.Context, *enginev1.InitializeResponse) {
	t.Helper()
	root, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	build, limits, _, _ := wireFixtureValues(t)
	sessions, err := daemon.NewSessionStore(root, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Shutdown(context.Background()) })
	token := endpointTokenFixture(t, 21)
	server, err := newServer(serverDependencies{
		root: root, token: token, host: host, sessions: sessions,
		build: build, capabilities: capabilities, maximumSessions: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	connection := serveGRPCFixture(t, server)
	api := enginev1.NewEngineServiceClient(connection)
	authorization, _ := token.AuthorizationValue()
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs(AuthenticationMetadataKey, authorization))
	request := grpcInitializeRequest(limits)
	request.SupportedCapabilities = &commonv1.CapabilitySet{Names: capabilities}
	request.RequiredCapabilities = &commonv1.CapabilitySet{Names: capabilities}
	initialized, err := api.Initialize(ctx, request)
	if err != nil || initialized.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_OK {
		t.Fatalf("initialize lifecycle fixture = %#v, %v", initialized, err)
	}
	return api, ctx, initialized
}

func lifecycleSnapshotFixture(t *testing.T, runID string) (*enginev1.SnapshotEnvelope, client.Snapshot) {
	t.Helper()
	codec, err := enginev1.NewHMACSnapshotAuthority(
		bytes.Repeat([]byte{0x41}, 32), 1, bytes.Repeat([]byte{0x42}, 32),
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := enginev1.NewSnapshotEnvelope(
		t.Context(), codec, runID, 8,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED,
		[]byte(`{"run_id":"`+runID+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.ParseSnapshot(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return envelope, snapshot
}

func assertLifecycleConflict(t *testing.T, got *commonv1.Status, err error) {
	t.Helper()
	if err != nil || got.GetCode() != commonv1.ErrorCode_ERROR_CODE_CONFLICT ||
		got.GetMessage() != "run lifecycle transition conflicts with current state" {
		t.Fatalf("lifecycle conflict = %#v, %v", got, err)
	}
}

func assertLifecycleInternal(t *testing.T, got *commonv1.Status, err error) {
	t.Helper()
	if err != nil || got.GetCode() != commonv1.ErrorCode_ERROR_CODE_INTERNAL || got.GetDetail() != nil {
		t.Fatalf("internal lifecycle status = %#v, %v", got, err)
	}
}

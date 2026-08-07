package grpcclient

import (
	"context"
	"errors"
	"testing"

	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestStatusTranslationPreservesTypedRecoveryFacts(t *testing.T) {
	t.Parallel()
	run, err := client.NewRunRef("run-1")
	if err != nil {
		t.Fatal(err)
	}
	operation, err := client.NewOperationID("operation-1")
	if err != nil {
		t.Fatal(err)
	}
	protocol1 := &commonv1.ProtocolVersion{Major: 1}
	protocol2 := &commonv1.ProtocolVersion{Major: 2}
	tests := []struct {
		name   string
		status *commonv1.Status
		facts  statusContext
		assert func(*testing.T, error)
	}{
		{
			name: "version mismatch",
			status: failedStatus(commonv1.ErrorCode_ERROR_CODE_INCOMPATIBLE_VERSION, func(status *commonv1.Status) {
				status.Detail = &commonv1.Status_VersionMismatch{VersionMismatch: &commonv1.VersionMismatch{
					Client: &commonv1.ProtocolRange{Minimum: protocol1, Maximum: protocol1},
					Server: &commonv1.ProtocolRange{Minimum: protocol2, Maximum: protocol2},
				}}
			}),
			assert: assertErrorType[*client.VersionMismatchError],
		},
		{
			name: "capability mismatch",
			status: failedStatus(commonv1.ErrorCode_ERROR_CODE_MISSING_CAPABILITY, func(status *commonv1.Status) {
				status.Detail = &commonv1.Status_CapabilityMismatch{CapabilityMismatch: &commonv1.CapabilityMismatch{
					Required: []string{"tools"}, Available: []string{"events"}, Missing: []string{"tools"},
				}}
			}),
			assert: assertErrorType[*client.CapabilityMismatchError],
		},
		{
			name: "cursor gap",
			status: failedStatus(commonv1.ErrorCode_ERROR_CODE_OUT_OF_RANGE, func(status *commonv1.Status) {
				status.Detail = &commonv1.Status_ReplayBounds{ReplayBounds: &commonv1.ReplayBounds{
					RequestedAfterSequence: 1, EarliestSequence: 3, LatestSequence: 5, RecoverySequence: 2,
				}}
			}),
			facts:  statusContext{run: &run, after: new(uint64(1)), readOnly: true},
			assert: assertErrorType[*client.CursorGapError],
		},
		{
			name: "overload",
			status: failedStatus(commonv1.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED, func(status *commonv1.Status) {
				status.Detail = &commonv1.Status_Overload{Overload: &commonv1.Overload{Resource: "runs", Limit: 1, Observed: 2}}
			}),
			assert: assertErrorType[*client.OverloadError],
		},
		{
			name: "stale client",
			status: failedStatus(commonv1.ErrorCode_ERROR_CODE_STALE_CLIENT, func(status *commonv1.Status) {
				status.Detail = &commonv1.Status_StaleClient{StaleClient: &commonv1.StaleClient{ExpectedEpoch: 2, ObservedEpoch: 1}}
			}),
			facts:  statusContext{sessionEpoch: 1},
			assert: assertErrorType[*client.StaleSessionError],
		},
		{
			name: "uncertain mutation",
			status: &commonv1.Status{
				Code: commonv1.ErrorCode_ERROR_CODE_UNCERTAIN_OPERATION, Message: "operation outcome is uncertain",
				OperationId: operation.String(),
				Detail: &commonv1.Status_UncertainOperation{UncertainOperation: &commonv1.UncertainOperation{
					OperationId: operation.String(), OperationKind: "start",
				}},
			},
			facts:  statusContext{operation: &operation},
			assert: assertErrorType[*client.UncertainOperationError],
		},
		{
			name: "snapshot mismatch",
			status: failedStatus(commonv1.ErrorCode_ERROR_CODE_SNAPSHOT_VERSION_MISMATCH, func(status *commonv1.Status) {
				status.Detail = &commonv1.Status_SnapshotVersionMismatch{SnapshotVersionMismatch: &commonv1.SnapshotVersionMismatch{
					Expected: "v2", Observed: "v1",
				}}
			}),
			assert: assertErrorType[*client.SnapshotVersionMismatchError],
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if validationErr := commonv1.ValidateStatus(test.status); validationErr != nil {
				t.Fatalf("fixture status: %v", validationErr)
			}
			test.assert(t, statusToError(test.status, test.facts))
		})
	}
}

func TestTransportErrorTranslationIsBoundedAndTyped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code      codes.Code
		want      client.ErrorCode
		retryable bool
	}{
		{codes.Canceled, client.ErrorCancelled, false},
		{codes.DeadlineExceeded, client.ErrorDeadlineExceeded, true},
		{codes.Unauthenticated, client.ErrorUnauthenticated, false},
		{codes.PermissionDenied, client.ErrorUnauthenticated, false},
		{codes.InvalidArgument, client.ErrorInvalidArgument, false},
		{codes.OutOfRange, client.ErrorInvalidArgument, false},
		{codes.FailedPrecondition, client.ErrorConflict, false},
		{codes.NotFound, client.ErrorNotFound, false},
		{codes.Aborted, client.ErrorConflict, false},
		{codes.AlreadyExists, client.ErrorConflict, false},
		{codes.Unavailable, client.ErrorUnavailable, true},
		{codes.ResourceExhausted, client.ErrorInternal, false},
		{codes.Unknown, client.ErrorInternal, false},
	}
	for _, test := range tests {
		err := transportError(context.Background(), status.Error(test.code, "sensitive transport detail"))
		translated, ok := errors.AsType[*client.StatusError](err)
		if !ok || translated.Code() != test.want || translated.Retryable() != test.retryable {
			t.Fatalf("transport %s = %T %v, want %s retry=%t", test.code, err, err, test.want, test.retryable)
		}
		if translated.Facts().Message() == "sensitive transport detail" {
			t.Fatal("transport detail escaped adapter boundary")
		}
	}
}

func TestTransportErrorsPreserveCallerCancellationAndUncertainMutation(t *testing.T) {
	t.Parallel()
	operation, err := client.NewOperationID("operation")
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("caller stopped")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	if err = transportError(ctx, status.Error(codes.Unavailable, "lost")); !errors.Is(err, context.Canceled) {
		t.Fatalf("transport cancellation = %v", err)
	}
	for name, translated := range map[string]error{
		"mutation":   mutationTransportError(ctx, status.Error(codes.Unavailable, "lost"), operation, "start"),
		"reconnect":  reconnectTransportError(ctx, status.Error(codes.Unavailable, "lost")),
		"initialize": initializeTransportError(ctx, status.Error(codes.Unavailable, "lost")),
	} {
		if !errors.Is(translated, cause) {
			t.Fatalf("%s error did not preserve cancellation cause: %v", name, translated)
		}
	}
	uncertain := mutationTransportError(
		context.Background(), status.Error(codes.DeadlineExceeded, "response lost"), operation, "start",
	)
	if _, ok := errors.AsType[*client.UncertainOperationError](uncertain); !ok {
		t.Fatalf("uncertain mutation = %T %v", uncertain, uncertain)
	}
}

func TestStatusTranslationRejectsMalformedRecoveryFacts(t *testing.T) {
	t.Parallel()
	run, err := client.NewRunRef("run")
	if err != nil {
		t.Fatal(err)
	}
	operation, err := client.NewOperationID("operation")
	if err != nil {
		t.Fatal(err)
	}
	badStatuses := []*commonv1.Status{
		commonv1.OKStatus(),
		{Code: commonv1.ErrorCode_ERROR_CODE_OUT_OF_RANGE, Message: "gap", Detail: &commonv1.Status_ReplayBounds{ReplayBounds: &commonv1.ReplayBounds{
			RequestedAfterSequence: 2, EarliestSequence: 3, LatestSequence: 4, RecoverySequence: 2,
		}}},
		{Code: commonv1.ErrorCode_ERROR_CODE_STALE_CLIENT, Message: "stale", Detail: &commonv1.Status_StaleClient{StaleClient: &commonv1.StaleClient{
			ExpectedEpoch: 3, ObservedEpoch: 2,
		}}},
		{Code: commonv1.ErrorCode_ERROR_CODE_UNCERTAIN_OPERATION, Message: "uncertain", OperationId: "other", Detail: &commonv1.Status_UncertainOperation{UncertainOperation: &commonv1.UncertainOperation{
			OperationId: "other", OperationKind: "start",
		}}},
	}
	after := uint64(1)
	contexts := []statusContext{
		{},
		{run: &run, after: &after, readOnly: true},
		{sessionEpoch: 1},
		{operation: &operation},
	}
	for index := range badStatuses {
		if code := errorCode(statusToError(badStatuses[index], contexts[index])); code != client.ErrorInternal {
			t.Fatalf("malformed status %d code = %s", index, code)
		}
	}
}

func TestResponseErrorDistinguishesProtocolFailureFromApplicationStatus(t *testing.T) {
	t.Parallel()
	if err := responseError(commonv1.OKStatus(), func() error { return nil }, responseContext{}); err != nil {
		t.Fatal(err)
	}
	if code := errorCode(responseError(commonv1.OKStatus(), func() error {
		return errors.New("malformed")
	}, responseContext{})); code != client.ErrorInternal {
		t.Fatalf("generic validation failure = %s", code)
	}
	failure := &commonv1.Status{Code: commonv1.ErrorCode_ERROR_CODE_NOT_FOUND, Message: "missing"}
	statusFailure := commonv1.AsError(failure)
	if code := errorCode(responseError(
		&commonv1.Status{Code: commonv1.ErrorCode_ERROR_CODE_CONFLICT, Message: "conflict"},
		func() error { return statusFailure }, responseContext{},
	)); code != client.ErrorInternal {
		t.Fatalf("mismatched response status = %s", code)
	}
}

func TestDefensiveNilAndConstructionBoundaries(t *testing.T) {
	t.Parallel()
	var lifetime *streamLifetime
	lifetime.close()
	lifetime.finish()
	lifetime.interrupt(errors.New("ignored"))
	var current *session
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	active, _ := newStreamLifetime(nil)
	if code := errorCode(receiveError(active, status.Error(codes.Unavailable, "lost"))); code != client.ErrorUnavailable {
		t.Fatalf("receive transport code = %s", code)
	}
	if code := errorCode(constructed[error](nil, errors.New("construction failed"))); code != client.ErrorInternal {
		t.Fatalf("construction failure code = %s", code)
	}
	if err := statusError(client.ErrorInternal, "", false); err == nil {
		t.Fatal("invalid status facts unexpectedly constructed")
	}
}

func TestStatusTranslationRejectsCorrelationMismatch(t *testing.T) {
	t.Parallel()
	operation, err := client.NewOperationID("expected-operation")
	if err != nil {
		t.Fatal(err)
	}
	status := &commonv1.Status{
		Code: commonv1.ErrorCode_ERROR_CODE_UNCERTAIN_OPERATION, Message: "operation outcome is uncertain",
		OperationId: "other-operation",
		Detail: &commonv1.Status_UncertainOperation{UncertainOperation: &commonv1.UncertainOperation{
			OperationId: "other-operation", OperationKind: "start",
		}},
	}
	statusErr := statusToError(status, statusContext{operation: &operation})
	var generic *client.StatusError
	if !errors.As(statusErr, &generic) || generic.Code() != client.ErrorInternal {
		t.Fatalf("mismatched status = %T %v", statusErr, statusErr)
	}
}

func TestGenericStatusTranslation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		wire commonv1.ErrorCode
		want client.ErrorCode
	}{
		{commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, client.ErrorInvalidArgument},
		{commonv1.ErrorCode_ERROR_CODE_UNAUTHENTICATED, client.ErrorUnauthenticated},
		{commonv1.ErrorCode_ERROR_CODE_NOT_FOUND, client.ErrorNotFound},
		{commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE, client.ErrorUnavailable},
		{commonv1.ErrorCode_ERROR_CODE_CANCELLED, client.ErrorCancelled},
		{commonv1.ErrorCode_ERROR_CODE_DEADLINE_EXCEEDED, client.ErrorDeadlineExceeded},
		{commonv1.ErrorCode_ERROR_CODE_CONFLICT, client.ErrorConflict},
		{commonv1.ErrorCode_ERROR_CODE_INTERNAL, client.ErrorInternal},
	}
	for _, test := range tests {
		status := &commonv1.Status{Code: test.wire, Message: "safe failure"}
		err := statusToError(status, statusContext{})
		translated, ok := errors.AsType[*client.StatusError](err)
		if !ok || translated.Code() != test.want {
			t.Fatalf("status %s = %T %v, want %s", test.wire, err, err, test.want)
		}
	}
}

func failedStatus(code commonv1.ErrorCode, detail func(*commonv1.Status)) *commonv1.Status {
	result := &commonv1.Status{Code: code, Message: "safe failure"}
	detail(result)
	return result
}

func assertErrorType[T error](t *testing.T, err error) {
	t.Helper()
	target, ok := errors.AsType[T](err)
	if !ok {
		t.Fatalf("error = %T %v, want %T", err, err, target)
	}
}

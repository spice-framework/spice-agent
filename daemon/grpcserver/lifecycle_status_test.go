package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLifecycleFailureStatusMapsSafeRecoveryFacts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		err           error
		operationID   string
		operationKind string
		code          commonv1.ErrorCode
		message       string
		retryable     bool
		assert        func(*testing.T, *commonv1.Status)
	}{
		{
			name: "typed stale", err: staleStatusFixture{expected: 7, observed: 6}, operationID: "op-stale",
			code: commonv1.ErrorCode_ERROR_CODE_STALE_CLIENT, message: "client ownership epoch is stale",
			assert: func(t *testing.T, got *commonv1.Status) {
				t.Helper()
				if got.GetStaleClient().GetExpectedEpoch() != 7 || got.GetStaleClient().GetObservedEpoch() != 6 {
					t.Fatalf("stale detail = %#v", got.GetStaleClient())
				}
			},
		},
		{
			name: "typed host capacity", err: fmt.Errorf(
				"secret host detail: %w", hostCapacityStatusFixture{resource: "active runs", limit: 4, observed: 5},
			),
			operationID: "op-host", code: commonv1.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED,
			message: "daemon capacity is exhausted", retryable: true,
			assert: assertOverloadStatus("active runs", 4, 5),
		},
		{
			name: "typed session capacity", err: sessionCapacityStatusFixture{resource: "stream leases", maximum: 8},
			operationID: "op-session", code: commonv1.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED,
			message: "client session capacity is exhausted", retryable: true,
			assert: assertOverloadStatus("stream leases", 8, 9),
		},
		{
			name: "uncertain", err: daemon.ErrRunHostUncertain, operationID: "op-import", operationKind: "import",
			code: commonv1.ErrorCode_ERROR_CODE_UNCERTAIN_OPERATION, message: "operation outcome is uncertain",
			assert: func(t *testing.T, got *commonv1.Status) {
				t.Helper()
				if got.GetUncertainOperation().GetOperationId() != "op-import" ||
					got.GetUncertainOperation().GetOperationKind() != "import" {
					t.Fatalf("uncertain detail = %#v", got.GetUncertainOperation())
				}
			},
		},
		{
			name: "missing", err: daemon.ErrHostedRunUnavailable, operationID: "op-missing",
			code: commonv1.ErrorCode_ERROR_CODE_NOT_FOUND, message: "run is unavailable",
		},
		{
			name: "closed", err: daemon.ErrRunHostClosed, operationID: "op-closed",
			code: commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE, message: "daemon is unavailable", retryable: true,
		},
		{
			name: "session store closed", err: daemon.ErrSessionStoreClosed, operationID: "op-store",
			code: commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE, message: "daemon is unavailable", retryable: true,
		},
		{
			name: "dependency unavailable", err: daemon.ErrRunHostUnavailable, operationID: "op-dependency",
			code: commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE, message: "daemon dependency is unavailable", retryable: true,
		},
		{
			name: "state conflict", err: daemon.ErrRunHostState, operationID: "op-state",
			code: commonv1.ErrorCode_ERROR_CODE_CONFLICT, message: "run lifecycle transition conflicts with current state",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := lifecycleFailureStatus(test.err, test.operationID, test.operationKind)
			if err := commonv1.ValidateStatus(got); err != nil {
				t.Fatalf("invalid status: %v (%#v)", err, got)
			}
			if got.GetCode() != test.code || got.GetMessage() != test.message || got.GetRetryable() != test.retryable ||
				got.GetOperationId() != test.operationID {
				t.Fatalf("status = %#v", got)
			}
			if test.assert != nil {
				test.assert(t, got)
			}
		})
	}
}

func TestLifecycleFailureStatusFailsClosedWithoutValidRecoveryFacts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		err           error
		operationID   string
		operationKind string
		code          commonv1.ErrorCode
		message       string
		retryable     bool
	}{
		{name: "nil", code: commonv1.ErrorCode_ERROR_CODE_INTERNAL, message: "daemon operation failed"},
		{name: "unknown", err: errors.New("secret database path and credential"), code: commonv1.ErrorCode_ERROR_CODE_INTERNAL, message: "daemon operation failed"},
		{name: "bare stale", err: daemon.ErrStaleSession, operationID: "op-stale", code: commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE, message: "daemon operation is temporarily unavailable", retryable: true},
		{name: "bare host capacity", err: daemon.ErrRunHostCapacity, operationID: "op-host", code: commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE, message: "daemon operation is temporarily unavailable", retryable: true},
		{name: "bare session capacity", err: daemon.ErrSessionGateCapacity, operationID: "op-session", code: commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE, message: "daemon operation is temporarily unavailable", retryable: true},
		{name: "invalid stale", err: staleStatusFixture{expected: 3, observed: 3}, operationID: "op-invalid", code: commonv1.ErrorCode_ERROR_CODE_INTERNAL, message: "daemon operation failed"},
		{name: "invalid host capacity", err: hostCapacityStatusFixture{resource: "active runs", limit: 4, observed: 4}, operationID: "op-invalid", code: commonv1.ErrorCode_ERROR_CODE_INTERNAL, message: "daemon operation failed"},
		{name: "invalid session capacity", err: sessionCapacityStatusFixture{resource: "streams", maximum: -1}, operationID: "op-invalid", code: commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE, message: "daemon operation is temporarily unavailable", retryable: true},
		{name: "uncertain missing ID", err: daemon.ErrRunHostUncertain, operationKind: "import", code: commonv1.ErrorCode_ERROR_CODE_INTERNAL, message: "daemon operation failed"},
		{name: "uncertain missing kind", err: daemon.ErrRunHostUncertain, operationID: "op-import", code: commonv1.ErrorCode_ERROR_CODE_INTERNAL, message: "daemon operation failed"},
		{name: "invalid operation ID", err: daemon.ErrRunHostState, operationID: "secret\nline", code: commonv1.ErrorCode_ERROR_CODE_INTERNAL, message: "daemon operation failed"},
		{name: "invalid operation kind", err: daemon.ErrRunHostUncertain, operationID: "op-import", operationKind: strings.Repeat("x", 257), code: commonv1.ErrorCode_ERROR_CODE_INTERNAL, message: "daemon operation failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := lifecycleFailureStatus(test.err, test.operationID, test.operationKind)
			if err := commonv1.ValidateStatus(got); err != nil {
				t.Fatalf("invalid fail-closed status: %v (%#v)", err, got)
			}
			if got.GetCode() != test.code || got.GetMessage() != test.message || got.GetRetryable() != test.retryable {
				t.Fatalf("status = %#v", got)
			}
			if strings.Contains(got.GetMessage(), "secret") || got.GetDetail() != nil {
				t.Fatalf("status leaked details: %#v", got)
			}
			if test.code == commonv1.ErrorCode_ERROR_CODE_INTERNAL && got.GetOperationId() != "" {
				t.Fatalf("internal status retained untrusted operation ID: %#v", got)
			}
		})
	}
}

func TestLifecycleTransportFailuresRemainGRPCStatusOnly(t *testing.T) {
	t.Parallel()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, deadlineCancel := context.WithDeadline(context.Background(), timeBeforeFixture())
	t.Cleanup(deadlineCancel)

	tests := []struct {
		name    string
		context func() context.Context
		err     error
		code    codes.Code
	}{
		{name: "error canceled", context: context.Background, err: fmt.Errorf("wrapped: %w", context.Canceled), code: codes.Canceled},
		{name: "context canceled", context: func() context.Context { return canceled }, err: errors.New("private failure"), code: codes.Canceled},
		{name: "error deadline", context: context.Background, err: fmt.Errorf("wrapped: %w", context.DeadlineExceeded), code: codes.DeadlineExceeded},
		{name: "context deadline", context: func() context.Context { return deadline }, err: errors.New("private failure"), code: codes.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transport := contextTransportError(test.context(), test.err)
			if status.Code(transport) != test.code || strings.Contains(transport.Error(), "private") {
				t.Fatalf("transport error = %v", transport)
			}
		})
	}
	if transport := contextTransportError(context.Background(), errors.New("private application failure")); transport != nil {
		t.Fatalf("application failure became transport error: %v", transport)
	}
}

func assertOverloadStatus(resource string, limit, observed uint64) func(*testing.T, *commonv1.Status) {
	return func(t *testing.T, got *commonv1.Status) {
		t.Helper()
		overload := got.GetOverload()
		if overload.GetResource() != resource || overload.GetLimit() != limit || overload.GetObserved() != observed {
			t.Fatalf("overload detail = %#v", overload)
		}
	}
}

type staleStatusFixture struct{ expected, observed uint64 }

func (value staleStatusFixture) Error() string         { return "stale secret" }
func (value staleStatusFixture) Is(target error) bool  { return target == daemon.ErrStaleSession }
func (value staleStatusFixture) ExpectedEpoch() uint64 { return value.expected }
func (value staleStatusFixture) ObservedEpoch() uint64 { return value.observed }

type hostCapacityStatusFixture struct {
	resource        string
	limit, observed uint64
}

func (value hostCapacityStatusFixture) Error() string { return "host capacity secret" }
func (value hostCapacityStatusFixture) Is(target error) bool {
	return target == daemon.ErrRunHostCapacity
}
func (value hostCapacityStatusFixture) Resource() string { return value.resource }
func (value hostCapacityStatusFixture) Limit() uint64    { return value.limit }
func (value hostCapacityStatusFixture) Observed() uint64 { return value.observed }

type sessionCapacityStatusFixture struct {
	resource string
	maximum  int
}

func (value sessionCapacityStatusFixture) Error() string { return "session capacity secret" }
func (value sessionCapacityStatusFixture) Is(target error) bool {
	return target == daemon.ErrSessionGateCapacity
}
func (value sessionCapacityStatusFixture) Resource() string { return value.resource }
func (value sessionCapacityStatusFixture) Maximum() int     { return value.maximum }

func timeBeforeFixture() time.Time { return time.Now().Add(-time.Second) }

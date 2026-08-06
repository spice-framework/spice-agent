package commonv1_test

import (
	"strings"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
)

func TestBoundaryValueValidation(t *testing.T) {
	t.Parallel()
	validBuild := &commonv1.BuildIdentity{
		Component: "spice-agentd", Version: "v0.1.0-preview.1",
		Commit: "0123456789ab", GoVersion: "go1.26.5",
	}
	if err := commonv1.ValidateBuildIdentity(validBuild); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]*commonv1.BuildIdentity{
		"nil":      nil,
		"missing":  {Component: "spice-agentd", Version: "v1", Commit: "commit"},
		"oversize": {Component: strings.Repeat("x", 257), Version: "v1", Commit: "commit", GoVersion: "go1.26.5"},
		"utf8":     {Component: string([]byte{0xff}), Version: "v1", Commit: "commit", GoVersion: "go1.26.5"},
	} {
		if err := commonv1.ValidateBuildIdentity(value); err == nil {
			t.Errorf("%s build identity succeeded", name)
		}
	}

	validLimits := limits(1024, 4, 2, 1024, 1, 1)
	if err := commonv1.ValidateLimits(validLimits); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]*commonv1.Limits{
		"nil":          nil,
		"zero":         {},
		"inconsistent": limits(1024, 4, 8, 4, 1, 1),
	} {
		if err := commonv1.ValidateLimits(value); err == nil {
			t.Errorf("%s limits succeeded", name)
		}
	}
	if negotiated, status := commonv1.NegotiateLimits(validLimits, &commonv1.Limits{}); negotiated != nil ||
		status.GetCode() != commonv1.ErrorCode_ERROR_CODE_INTERNAL {
		t.Fatalf("invalid server limits = %#v, %#v", negotiated, status)
	}

	if err := commonv1.ValidateEncodedSize(validBuild, 1024); err != nil {
		t.Fatal(err)
	}
	if err := commonv1.ValidateEncodedSize(nil, 1); err == nil {
		t.Fatal("nil encoded message succeeded")
	}
	if err := commonv1.ValidateEncodedSize(validBuild, 0); err == nil {
		t.Fatal("zero encoded bound succeeded")
	}
	if err := commonv1.ValidateEncodedSize(validBuild, 1); err == nil {
		t.Fatal("oversized encoded message succeeded")
	}
}

func TestProtocolCapabilityAndHealthFailureBoundaries(t *testing.T) {
	t.Parallel()
	for name, value := range map[string]*commonv1.ProtocolRange{
		"nil": nil,
		"missing minimum": {
			Maximum: &commonv1.ProtocolVersion{Major: 1},
		},
		"missing major": {
			Minimum: &commonv1.ProtocolVersion{}, Maximum: &commonv1.ProtocolVersion{},
		},
	} {
		if err := commonv1.ValidateProtocolRange(value); err == nil {
			t.Errorf("%s protocol range succeeded", name)
		}
	}
	if version, status := commonv1.NegotiateProtocol(
		commonv1.SupportedProtocolRange(),
		&commonv1.ProtocolRange{},
	); version != nil || status.GetCode() != commonv1.ErrorCode_ERROR_CODE_INTERNAL {
		t.Fatalf("invalid server protocol = %#v, %#v", version, status)
	}

	tooMany := make([]string, 1025)
	for name, value := range map[string]*commonv1.CapabilitySet{
		"nil":       nil,
		"duplicate": {Names: []string{"events", "events"}},
		"blank":     {Names: []string{""}},
		"too many":  {Names: tooMany},
	} {
		if err := commonv1.ValidateCapabilities(value); err == nil {
			t.Errorf("%s capabilities succeeded", name)
		}
	}
	if enabled, status := commonv1.NegotiateCapabilities(
		&commonv1.CapabilitySet{Names: []string{"events"}},
		&commonv1.CapabilitySet{Names: []string{"snapshots"}},
		&commonv1.CapabilitySet{Names: []string{"events"}},
	); enabled != nil || status.GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("unsupported required capability = %#v, %#v", enabled, status)
	}

	for _, state := range []commonv1.HealthState{
		commonv1.HealthState_HEALTH_STATE_STARTING,
		commonv1.HealthState_HEALTH_STATE_READY,
		commonv1.HealthState_HEALTH_STATE_DEGRADED,
		commonv1.HealthState_HEALTH_STATE_STOPPING,
	} {
		if err := commonv1.ValidateHealth(&commonv1.Health{State: state, Limits: limits(1024, 4, 2, 1024, 1, 1)}); err != nil {
			t.Errorf("health state %s: %v", state, err)
		}
	}
	for name, value := range map[string]*commonv1.Health{
		"nil": nil,
		"unknown state": {
			State: commonv1.HealthState(9000), Limits: limits(1024, 4, 2, 1024, 1, 1),
		},
		"too many reasons": {
			State:           commonv1.HealthState_HEALTH_STATE_DEGRADED,
			DegradedReasons: make([]string, 65), Limits: limits(1024, 4, 2, 1024, 1, 1),
		},
		"blank reason": {
			State:           commonv1.HealthState_HEALTH_STATE_DEGRADED,
			DegradedReasons: []string{""}, Limits: limits(1024, 4, 2, 1024, 1, 1),
		},
	} {
		if err := commonv1.ValidateHealth(value); err == nil {
			t.Errorf("%s health succeeded", name)
		}
	}
}

func TestEveryTypedStatusDetailIsValidated(t *testing.T) {
	t.Parallel()
	oldRange := protocolRange(1, 0, 1, 1)
	newRange := protocolRange(2, 0, 2, 1)
	statuses := []*commonv1.Status{
		{
			Code: commonv1.ErrorCode_ERROR_CODE_INCOMPATIBLE_VERSION, Message: "version mismatch",
			Detail: &commonv1.Status_VersionMismatch{VersionMismatch: &commonv1.VersionMismatch{Client: oldRange, Server: newRange}},
		},
		{
			Code: commonv1.ErrorCode_ERROR_CODE_MISSING_CAPABILITY, Message: "missing capability",
			Detail: &commonv1.Status_CapabilityMismatch{CapabilityMismatch: &commonv1.CapabilityMismatch{
				Required: []string{"events", "tools"}, Available: []string{"events"}, Missing: []string{"tools"},
			}},
		},
		{
			Code: commonv1.ErrorCode_ERROR_CODE_OUT_OF_RANGE, Message: "replay gap",
			Detail: &commonv1.Status_ReplayBounds{ReplayBounds: &commonv1.ReplayBounds{
				RequestedAfterSequence: 8, EarliestSequence: 10, LatestSequence: 20, RecoverySequence: 9,
			}},
		},
		{
			Code: commonv1.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED, Message: "overload",
			Detail: &commonv1.Status_Overload{Overload: &commonv1.Overload{Resource: "runs", Limit: 1, Observed: 2}},
		},
		{
			Code: commonv1.ErrorCode_ERROR_CODE_STALE_CLIENT, Message: "stale",
			Detail: &commonv1.Status_StaleClient{StaleClient: &commonv1.StaleClient{ExpectedEpoch: 2, ObservedEpoch: 1}},
		},
		{
			Code: commonv1.ErrorCode_ERROR_CODE_SNAPSHOT_VERSION_MISMATCH, Message: "snapshot mismatch",
			Detail: &commonv1.Status_SnapshotVersionMismatch{SnapshotVersionMismatch: &commonv1.SnapshotVersionMismatch{
				Expected: "snapshot/v2", Observed: "snapshot/v1",
			}},
		},
		{
			Code: commonv1.ErrorCode_ERROR_CODE_UNCERTAIN_OPERATION, Message: "uncertain",
			Detail: &commonv1.Status_UncertainOperation{UncertainOperation: &commonv1.UncertainOperation{
				OperationId: "operation-1", OperationKind: "run.start",
			}},
		},
	}
	for _, status := range statuses {
		if err := commonv1.ValidateStatus(status); err != nil {
			t.Errorf("valid %s status: %v", status.GetCode(), err)
		}
	}

	malformed := []*commonv1.Status{
		{Code: commonv1.ErrorCode_ERROR_CODE_INCOMPATIBLE_VERSION, Message: "bad", Detail: &commonv1.Status_VersionMismatch{}},
		{Code: commonv1.ErrorCode_ERROR_CODE_INCOMPATIBLE_VERSION, Message: "bad", Detail: &commonv1.Status_VersionMismatch{VersionMismatch: &commonv1.VersionMismatch{Client: oldRange, Server: oldRange}}},
		{Code: commonv1.ErrorCode_ERROR_CODE_MISSING_CAPABILITY, Message: "bad", Detail: &commonv1.Status_CapabilityMismatch{}},
		{Code: commonv1.ErrorCode_ERROR_CODE_MISSING_CAPABILITY, Message: "bad", Detail: &commonv1.Status_CapabilityMismatch{CapabilityMismatch: &commonv1.CapabilityMismatch{Required: []string{"tools"}, Available: []string{"events"}, Missing: []string{"wrong"}}}},
		{Code: commonv1.ErrorCode_ERROR_CODE_OUT_OF_RANGE, Message: "bad", Detail: &commonv1.Status_ReplayBounds{ReplayBounds: &commonv1.ReplayBounds{EarliestSequence: 10, LatestSequence: 20, RecoverySequence: 8}}},
		{Code: commonv1.ErrorCode_ERROR_CODE_STALE_CLIENT, Message: "bad", Detail: &commonv1.Status_StaleClient{StaleClient: &commonv1.StaleClient{ExpectedEpoch: 1, ObservedEpoch: 1}}},
		{Code: commonv1.ErrorCode_ERROR_CODE_SNAPSHOT_VERSION_MISMATCH, Message: "bad", Detail: &commonv1.Status_SnapshotVersionMismatch{SnapshotVersionMismatch: &commonv1.SnapshotVersionMismatch{Expected: "same", Observed: "same"}}},
		{Code: commonv1.ErrorCode_ERROR_CODE_UNCERTAIN_OPERATION, Message: "bad", Detail: &commonv1.Status_UncertainOperation{}},
	}
	for index, status := range malformed {
		if err := commonv1.ValidateStatus(status); err == nil {
			t.Errorf("malformed status %d succeeded", index)
		}
	}

	var typed *commonv1.StatusError
	if typed.Error() != "protocol status is unavailable" || typed.Status() != nil {
		t.Fatal("nil typed status error is not defensive")
	}
}

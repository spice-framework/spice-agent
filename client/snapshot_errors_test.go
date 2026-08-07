package client

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
)

func TestSnapshotIsOpaqueBoundedAndImmutable(t *testing.T) {
	t.Parallel()
	encoded := []byte("authenticated opaque envelope")
	snapshot, err := ParseSnapshot(encoded)
	if err != nil {
		t.Fatal(err)
	}
	encoded[0] = 'X'
	first, err := snapshot.MarshalBinary()
	if err != nil || len(first) == 0 {
		t.Fatalf("marshal snapshot: %v", err)
	}
	first[0] = 'X'
	second, err := snapshot.MarshalBinary()
	if err != nil || string(second) != "authenticated opaque envelope" {
		t.Fatalf("snapshot exposed bytes: %q, err=%v", second, err)
	}
	if _, err := ParseSnapshot(nil); err == nil {
		t.Fatal("empty snapshot accepted")
	}
	if _, err := ParseSnapshot(make([]byte, MaximumSnapshotBytes+1)); err == nil {
		t.Fatal("oversized snapshot accepted")
	}
}

func TestTypedRecoveryErrorsExposeStableDefensiveFacts(t *testing.T) {
	t.Parallel()
	run := mustRun(t, "run-recovery")
	statusOperation := mustOperation(t, "operation-status")
	facts, err := NewErrorFacts("safe status message", true, &statusOperation)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := NewTerminalError(facts, run)
	if err != nil || terminal.Run() != run {
		t.Fatalf("terminal error = %#v, err=%v", terminal, err)
	}
	uncertainOperation := mustOperation(t, "operation-uncertain")
	uncertain, _ := NewUncertainOperationError(facts, uncertainOperation, "import")
	gap, _ := NewCursorGapError(facts, run, 3, 5, 10, 4)
	stale, _ := NewStaleSessionError(facts, 3, 0)
	if uncertain.UncertainOperation() != uncertainOperation || gap.RecoverySequence() != 4 || stale.ObservedEpoch() != 0 {
		t.Fatalf("recovery errors = %#v %#v %#v", uncertain, gap, stale)
	}

	v1, _ := NewProtocolVersion(1, 0, 0)
	v2, _ := NewProtocolVersion(2, 0, 0)
	range1, _ := NewProtocolRange(v1, v1)
	range2, _ := NewProtocolRange(v2, v2)
	versionMismatch, err := NewVersionMismatchError(facts, range1, range2)
	if err != nil || versionMismatch.Client() != range1 || versionMismatch.Server() != range2 {
		t.Fatalf("version mismatch = %#v, err=%v", versionMismatch, err)
	}
	capabilityMismatch, err := NewCapabilityMismatchError(
		facts,
		[]string{"events", "snapshots"}, []string{"events"}, []string{"snapshots"},
	)
	if err != nil {
		t.Fatal(err)
	}
	missing := capabilityMismatch.Missing()
	if len(missing) != 1 {
		t.Fatalf("missing capabilities = %v", missing)
	}
	missing[0] = "mutated"
	missingAgain := capabilityMismatch.Missing()
	if !slices.Equal(capabilityMismatch.Required(), []string{"events", "snapshots"}) ||
		!slices.Equal(capabilityMismatch.Available(), []string{"events"}) ||
		len(missingAgain) != 1 || missingAgain[0] != "snapshots" {
		t.Fatal("capability mismatch exposed slice storage")
	}
	overload, _ := NewOverloadError(facts, "active-runs", 4, 5)
	if overload.Resource() != "active-runs" || overload.Limit() != 4 || overload.Observed() != 5 {
		t.Fatalf("overload = %#v", overload)
	}
	snapshotMismatch, _ := NewSnapshotVersionMismatchError(facts, "v2", "v1")
	if snapshotMismatch.Expected() != "v2" || snapshotMismatch.Observed() != "v1" {
		t.Fatalf("snapshot mismatch = %#v", snapshotMismatch)
	}
	status, err := NewStatusError(ErrorUnavailable, facts)
	if err != nil || status.Code() != ErrorUnavailable {
		t.Fatalf("status = %#v, err=%v", status, err)
	}
	for _, code := range []ErrorCode{
		ErrorInvalidArgument,
		ErrorUnauthenticated,
		ErrorNotFound,
		ErrorUnavailable,
		ErrorCancelled,
		ErrorDeadlineExceeded,
		ErrorConflict,
		ErrorInternal,
	} {
		generic, genericErr := NewStatusError(code, facts)
		if genericErr != nil || generic.Code() != code || generic.Facts() != facts {
			t.Errorf("generic status %q = %#v, err=%v", code, generic, genericErr)
		}
	}

	for name, failure := range map[string]StatusFailure{
		"terminal":            terminal,
		"uncertain operation": uncertain,
		"cursor gap":          gap,
		"stale session":       stale,
		"version mismatch":    versionMismatch,
		"capability mismatch": capabilityMismatch,
		"overload":            overload,
		"snapshot mismatch":   snapshotMismatch,
		"generic":             status,
	} {
		if failure.Error() != facts.Message() || failure.Facts() != facts || !failure.Retryable() {
			t.Errorf("%s lost common status facts: %#v", name, failure)
			continue
		}
		gotOperation, ok := failure.Operation()
		if !ok || gotOperation != statusOperation {
			t.Errorf("%s status operation = %#v, %t", name, gotOperation, ok)
		}
	}
}

func TestTypedRecoveryErrorsRejectInconsistentFacts(t *testing.T) {
	t.Parallel()
	run := mustRun(t, "run")
	operation := mustOperation(t, "operation")
	facts, err := NewErrorFacts("safe status", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, constructErr := NewUncertainOperationError(facts, operation, ""); constructErr == nil {
		t.Fatal("empty uncertain operation kind accepted")
	}
	if _, constructErr := NewUncertainOperationError(
		facts,
		operation,
		strings.Repeat("k", maximumTokenBytes),
	); constructErr != nil {
		t.Fatalf("maximum uncertain operation kind rejected: %v", constructErr)
	}
	if _, constructErr := NewUncertainOperationError(
		facts,
		operation,
		strings.Repeat("k", maximumTokenBytes+1),
	); constructErr == nil {
		t.Fatal("oversized uncertain operation kind accepted")
	}
	for _, bounds := range [][4]uint64{
		{0, 0, 1, 0}, {5, 5, 4, 4}, {4, 5, 10, 3}, {4, 5, 10, 4},
	} {
		if _, constructErr := NewCursorGapError(facts, run, bounds[0], bounds[1], bounds[2], bounds[3]); constructErr == nil {
			t.Fatalf("invalid cursor gap accepted: %v", bounds)
		}
	}
	futureGap, err := NewCursorGapError(facts, run, math.MaxUint64, 5, 10, 10)
	if err != nil {
		t.Fatalf("maximum requested cursor should be representable: %v", err)
	}
	if futureGap.RequestedAfterSequence() != math.MaxUint64 {
		t.Fatalf("future cursor = %d", futureGap.RequestedAfterSequence())
	}
	if _, err := NewStaleSessionError(facts, 1, 1); err == nil {
		t.Fatal("equal stale epochs accepted")
	}
	v1, _ := NewProtocolVersion(1, 0, 0)
	range1, _ := NewProtocolRange(v1, v1)
	if _, err := NewVersionMismatchError(facts, range1, range1); err == nil {
		t.Fatal("overlapping version ranges accepted")
	}
	if _, err := NewCapabilityMismatchError(facts, []string{"events"}, []string{"events"}, nil); err == nil {
		t.Fatal("empty capability mismatch accepted")
	}
	if _, err := NewOverloadError(facts, "runs", 4, 4); err == nil {
		t.Fatal("non-exceeded overload accepted")
	}
	if _, err := NewSnapshotVersionMismatchError(facts, "v1", "v1"); err == nil {
		t.Fatal("equal snapshot formats accepted")
	}
	if _, err := NewStatusError("typed-detail-required", facts); err == nil {
		t.Fatal("unknown generic status accepted")
	}
	for _, message := range []string{"", " unsafe", "unsafe\x00", "unsafe\r", "unsafe\n", "unsafe\t"} {
		if _, err := NewErrorFacts(message, false, nil); err == nil {
			t.Errorf("invalid status message %q accepted", message)
		}
	}
	invalidFactsConstructors := map[string]func() error{
		"terminal": func() error {
			_, constructErr := NewTerminalError(ErrorFacts{}, run)
			return constructErr
		},
		"uncertain": func() error {
			_, constructErr := NewUncertainOperationError(ErrorFacts{}, operation, "import")
			return constructErr
		},
		"cursor gap": func() error {
			_, constructErr := NewCursorGapError(ErrorFacts{}, run, 3, 5, 10, 4)
			return constructErr
		},
		"stale": func() error {
			_, constructErr := NewStaleSessionError(ErrorFacts{}, 2, 1)
			return constructErr
		},
		"version": func() error {
			_, constructErr := NewVersionMismatchError(ErrorFacts{}, range1, mustProtocolRange(t, 2))
			return constructErr
		},
		"capability": func() error {
			_, constructErr := NewCapabilityMismatchError(
				ErrorFacts{}, []string{"events"}, nil, []string{"events"},
			)
			return constructErr
		},
		"overload": func() error {
			_, constructErr := NewOverloadError(ErrorFacts{}, "runs", 1, 2)
			return constructErr
		},
		"snapshot": func() error {
			_, constructErr := NewSnapshotVersionMismatchError(ErrorFacts{}, "v2", "v1")
			return constructErr
		},
		"generic": func() error {
			_, constructErr := NewStatusError(ErrorInternal, ErrorFacts{})
			return constructErr
		},
	}
	for name, construct := range invalidFactsConstructors {
		if construct() == nil {
			t.Errorf("%s accepted zero status facts", name)
		}
	}

	var terminalTarget *TerminalError
	terminal, _ := NewTerminalError(facts, run)
	if !errors.As(terminal, &terminalTarget) || terminalTarget.Run() != run {
		t.Fatal("terminal error does not support errors.As")
	}
	if terminal.Retryable() {
		t.Fatal("non-retryable status changed to retryable")
	}
	if statusOperation, ok := terminal.Operation(); ok || statusOperation.String() != "" {
		t.Fatalf("absent status operation = %#v, %t", statusOperation, ok)
	}
}

func mustProtocolRange(t *testing.T, major uint32) ProtocolRange {
	t.Helper()
	version, err := NewProtocolVersion(major, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	value, err := NewProtocolRange(version, version)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestTypedNilErrorsAndOptionalFactsAreSafe(t *testing.T) {
	t.Parallel()
	var terminal *TerminalError
	var uncertain *UncertainOperationError
	var gap *CursorGapError
	var stale *StaleSessionError
	var version *VersionMismatchError
	var capabilities *CapabilityMismatchError
	var overload *OverloadError
	var snapshot *SnapshotVersionMismatchError
	var status *StatusError
	if terminal.Error() == "" || terminal.Run().ID() != "" || terminal.Facts() != (ErrorFacts{}) ||
		terminal.Retryable() || uncertain.Error() == "" || uncertain.UncertainOperation().String() != "" ||
		uncertain.Kind() != "" || gap.Error() == "" ||
		gap.Run().ID() != "" || gap.RequestedAfterSequence() != 0 || gap.EarliestSequence() != 0 ||
		gap.LatestSequence() != 0 || gap.RecoverySequence() != 0 || stale.Error() == "" ||
		stale.ExpectedEpoch() != 0 || stale.ObservedEpoch() != 0 || version.Client() != (ProtocolRange{}) ||
		version.Server() != (ProtocolRange{}) || version.Error() == "" || capabilities.Error() == "" ||
		len(capabilities.Required()) != 0 ||
		len(capabilities.Available()) != 0 || len(capabilities.Missing()) != 0 || overload.Resource() != "" ||
		overload.Limit() != 0 || overload.Observed() != 0 || overload.Error() == "" || snapshot.Error() == "" ||
		snapshot.Expected() != "" || snapshot.Observed() != "" ||
		status.Error() == "" || status.Code() != "" || status.Retryable() {
		t.Fatal("typed nil error accessor is unsafe")
	}
	for name, failure := range map[string]StatusFailure{
		"terminal": terminal, "uncertain": uncertain, "gap": gap, "stale": stale,
		"version": version, "capabilities": capabilities, "overload": overload,
		"snapshot": snapshot, "status": status,
	} {
		if operation, ok := failure.Operation(); ok || operation.String() != "" || failure.Facts() != (ErrorFacts{}) {
			t.Fatalf("nil %s common status = %#v, %t", name, operation, ok)
		}
	}
}

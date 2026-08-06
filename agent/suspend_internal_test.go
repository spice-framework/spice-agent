package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestResumeMakesSnapshotImmediatelyNonExportable(t *testing.T) {
	t.Parallel()
	resumeSignal := make(chan struct{})
	run := &Run{status: LifecycleSuspended, resumeSignal: resumeSignal}
	if err := run.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-resumeSignal:
	default:
		t.Fatal("resume signal remained open")
	}
	assertSnapshotUnsafeWhileRunning(t, run)
}

func TestSuspendCancellationAutoResumeMakesSnapshotImmediatelyNonExportable(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	run := &Run{status: runStatusRunning, started: true}
	done := make(chan error, 1)
	go func() { done <- run.Suspend(ctx) }()

	deadline := time.Now().Add(time.Second)
	for {
		run.stateMu.Lock()
		registered := run.suspendRequested && run.suspendWaiter != nil
		if registered {
			// Deterministically model suspendAtBoundary establishing the safe
			// boundary before the caller's context is cancelled. The waiter is
			// intentionally not selected, forcing Suspend's cancellation branch.
			run.suspendRequested = false
			run.suspendWaiter = nil
			run.status = LifecycleSuspended
			run.resumeSignal = make(chan struct{})
		}
		run.stateMu.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("suspend request was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("suspend cancellation = %v", err)
	}
	assertSnapshotUnsafeWhileRunning(t, run)
}

func assertSnapshotUnsafeWhileRunning(t *testing.T, run *Run) {
	t.Helper()
	_, err := run.ExportSnapshot()
	failure, ok := errors.AsType[*UnsafeSnapshotError](err)
	if !ok || failure.Status != runStatusRunning {
		t.Fatalf("immediate snapshot error = %#v, %v", failure, err)
	}
}

package agent

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/interaction"
)

func TestPrepareLocalResumeRejectsSequenceOverflow(t *testing.T) {
	t.Parallel()
	resumeSignal := make(chan struct{})
	run := &Run{
		ctx:                context.Background(),
		status:             LifecycleSuspended,
		resumeSignal:       resumeSignal,
		lastSequence:       math.MaxUint64,
		activeInteractions: make(map[interaction.ID]struct{}),
	}
	if _, err := run.PrepareLocalResume(); err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("overflow preparation = %v", err)
	}
	select {
	case <-resumeSignal:
		t.Fatal("overflow preparation unblocked execution")
	default:
	}
}

func TestPrepareLocalResumePrioritizesVisibleCancellationWhileFinishing(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	resumeSignal := make(chan struct{})
	run := &Run{
		ctx:                ctx,
		status:             runStatusFinishing,
		resumeSignal:       resumeSignal,
		lastSequence:       17,
		activeInteractions: make(map[interaction.ID]struct{}),
	}

	if prepared, err := run.PrepareLocalResume(); prepared != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("finishing cancellation preparation = %#v, %v", prepared, err)
	}
	if run.status != runStatusFinishing || run.localResume != nil || run.resumeSignal != resumeSignal || run.lastSequence != 17 {
		t.Fatalf(
			"cancelled preparation mutated boundary: status=%s prepared=%#v signal_changed=%t sequence=%d",
			run.status,
			run.localResume,
			run.resumeSignal != resumeSignal,
			run.lastSequence,
		)
	}
	select {
	case <-resumeSignal:
		t.Fatal("cancelled preparation closed the resume signal")
	default:
	}
}

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

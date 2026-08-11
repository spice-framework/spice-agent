package agent_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/tool"
)

type localResumeFixture struct {
	engine   *agent.Engine
	run      *agent.Run
	provider *scriptedProvider
	recorder *event.Recorder
	snapshot agent.Snapshot
}

func newLocalResumeFixture(t *testing.T) localResumeFixture {
	t.Helper()
	call, err := tool.NewCall("local-resume-call", "read", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	gate := &gatedTool{started: make(chan struct{}), release: make(chan struct{})}
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{
		{toolEvent(t, call), completed(t)},
		{delta(t, "resumed"), completed(t)},
	}}
	recorder := &event.Recorder{}
	engine := newEngine(t, provider, map[string]tool.Tool{"read": gate}, []event.Observer{recorder}, nil)
	run := startRun(t, engine, 3)
	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("tool did not start")
	}
	suspended := make(chan error, 1)
	go func() { suspended <- run.Suspend(t.Context()) }()
	waitForSuspendRequest(t, run)
	close(gate.release)
	if err = <-suspended; err != nil {
		t.Fatal(err)
	}
	snapshot, err := run.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	return localResumeFixture{engine: engine, run: run, provider: provider, recorder: recorder, snapshot: snapshot}
}

func (fixture localResumeFixture) providerCalls() int {
	fixture.provider.mu.Lock()
	defer fixture.provider.mu.Unlock()
	return len(fixture.provider.requests)
}

func TestPreparedLocalResumeIsInertAndAbortRestoresExactSnapshot(t *testing.T) {
	fixture := newLocalResumeFixture(t)
	before := mustSnapshotBytes(t, fixture.snapshot)
	eventCount := len(fixture.recorder.Events())
	prepared, err := fixture.run.PrepareLocalResume()
	if err != nil {
		t.Fatal(err)
	}
	if prepared.RunID() != fixture.run.ID() || prepared.NextSequence() != fixture.snapshot.LastSequence()+1 {
		t.Fatalf("prepared boundary = %q/%d", prepared.RunID(), prepared.NextSequence())
	}
	if fixture.providerCalls() != 1 || len(fixture.recorder.Events()) != eventCount {
		t.Fatalf("preparation performed work: provider=%d events=%d", fixture.providerCalls(), len(fixture.recorder.Events()))
	}
	if _, err = fixture.run.ExportSnapshot(); !unsafeResumePending(err) {
		t.Fatalf("export during reservation = %v", err)
	}
	if err = fixture.run.Suspend(t.Context()); !unsafeResumePending(err) {
		t.Fatalf("suspend during reservation = %v", err)
	}
	if _, err = fixture.run.PrepareLocalResume(); !unsafeResumePending(err) {
		t.Fatalf("duplicate reservation = %v", err)
	}
	request, requestErr := interaction.NewRequest("local-resume-interaction", "confirm", "Continue?", []byte(`{}`))
	if requestErr != nil {
		t.Fatal(requestErr)
	}
	if _, err = fixture.run.Interact(t.Context(), request); !unsafeResumePending(err) {
		t.Fatalf("interaction during reservation = %v", err)
	}
	if len(fixture.recorder.Events()) != eventCount {
		t.Fatalf("rejected operations emitted events: before=%d after=%d", eventCount, len(fixture.recorder.Events()))
	}
	if err = prepared.Abort(); err != nil {
		t.Fatal(err)
	}
	after, err := fixture.run.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, mustSnapshotBytes(t, after)) {
		t.Fatal("aborted local resume changed the suspended snapshot")
	}
	if err = prepared.Close(); err != nil {
		t.Fatalf("duplicate abort through Close = %v", err)
	}
	if err = prepared.Commit(); !errors.Is(err, agent.ErrPreparedExecutionAborted) {
		t.Fatalf("commit after abort = %v", err)
	}
	if err = fixture.run.Resume(); err != nil {
		t.Fatal(err)
	}
	if err = fixture.run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedLocalResumeCommitReleasesExactNextSequence(t *testing.T) {
	fixture := newLocalResumeFixture(t)
	prepared, err := fixture.run.PrepareLocalResume()
	if err != nil {
		t.Fatal(err)
	}
	next := prepared.NextSequence()
	if err = prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	events := collectAfter(t, fixture.run, next-1)
	if err = fixture.run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Sequence() != next || events[0].Kind() != event.TurnStarted {
		t.Fatalf("first resumed event = %v", eventKinds(events))
	}
	assertKindsFromSequence(t, events, next, []event.Kind{
		event.TurnStarted, event.ModelStarted, event.ModelDelta,
		event.ModelCompleted, event.TurnCompleted, event.RunCompleted,
	})
	if err = prepared.Commit(); !errors.Is(err, agent.ErrPreparedExecutionCommitted) {
		t.Fatalf("duplicate commit = %v", err)
	}
	if err = prepared.Abort(); !errors.Is(err, agent.ErrPreparedExecutionCommitted) {
		t.Fatalf("abort after commit = %v", err)
	}
}

func TestPreparedLocalResumeCommitAfterCancellationSkipsPostBoundaryWork(t *testing.T) {
	fixture := newLocalResumeFixture(t)
	prepared, err := fixture.run.PrepareLocalResume()
	if err != nil {
		t.Fatal(err)
	}
	next := prepared.NextSequence()
	eventCount := len(fixture.recorder.Events())
	fixture.run.Cancel()
	assertRunStillLatched(t, fixture.run)
	if fixture.providerCalls() != 1 || len(fixture.recorder.Events()) != eventCount {
		t.Fatal("cancelled preparation performed work before Commit")
	}
	if err = prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	events := collectAfter(t, fixture.run, next-1)
	if err = fixture.run.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run = %v", err)
	}
	assertKindsFromSequence(t, events, next, []event.Kind{event.RunCancelled})
	if len(events) == 0 {
		t.Fatal("cancelled commit produced no terminal event")
	}
	if events[0].Sequence() != next || fixture.providerCalls() != 1 {
		t.Fatalf("cancelled commit boundary = sequence %d, provider calls %d", events[0].Sequence(), fixture.providerCalls())
	}
}

func TestPreparedLocalResumeAbortAfterCancellationReleasesFinalization(t *testing.T) {
	fixture := newLocalResumeFixture(t)
	prepared, err := fixture.run.PrepareLocalResume()
	if err != nil {
		t.Fatal(err)
	}
	next := prepared.NextSequence()
	fixture.run.Cancel()
	assertRunStillLatched(t, fixture.run)
	if err = prepared.Abort(); err != nil {
		t.Fatal(err)
	}
	events := collectAfter(t, fixture.run, next-1)
	if err = fixture.run.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run = %v", err)
	}
	assertKindsFromSequence(t, events, next, []event.Kind{event.RunCancelled})
	if len(events) == 0 {
		t.Fatal("cancelled abort produced no terminal event")
	}
	if events[0].Sequence() != next || fixture.providerCalls() != 1 {
		t.Fatalf("cancelled abort boundary = sequence %d, provider calls %d", events[0].Sequence(), fixture.providerCalls())
	}
}

func TestPreparedLocalResumeLatchesShutdownUntilDecision(t *testing.T) {
	fixture := newLocalResumeFixture(t)
	prepared, err := fixture.run.PrepareLocalResume()
	if err != nil {
		t.Fatal(err)
	}
	shutdownContext, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err = fixture.engine.Shutdown(shutdownContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pending-decision shutdown = %v", err)
	}
	assertRunStillLatched(t, fixture.run)
	if err = prepared.Abort(); err != nil {
		t.Fatal(err)
	}
	if err = fixture.engine.Shutdown(t.Context()); err != nil {
		t.Fatalf("drained shutdown = %v", err)
	}
	if err = fixture.run.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown terminal = %v", err)
	}
}

func TestPreparedLocalResumeCommitAbortContention(t *testing.T) {
	for iteration := range 16 {
		fixture := newLocalResumeFixture(t)
		prepared, err := fixture.run.PrepareLocalResume()
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan struct {
			commit bool
			err    error
		}, 2)
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			results <- struct {
				commit bool
				err    error
			}{commit: true, err: prepared.Commit()}
		}()
		go func() {
			defer workers.Done()
			<-start
			results <- struct {
				commit bool
				err    error
			}{err: prepared.Abort()}
		}()
		close(start)
		workers.Wait()
		close(results)
		var commitWon, abortWon bool
		for result := range results {
			switch {
			case result.err == nil && result.commit:
				commitWon = true
			case result.err == nil:
				abortWon = true
			case result.commit && errors.Is(result.err, agent.ErrPreparedExecutionAborted):
			case !result.commit && errors.Is(result.err, agent.ErrPreparedExecutionCommitted):
			default:
				t.Fatalf("iteration %d unexpected decision result: commit=%t err=%v", iteration, result.commit, result.err)
			}
		}
		if commitWon == abortWon {
			t.Fatalf("iteration %d decision winners: commit=%t abort=%t", iteration, commitWon, abortWon)
		}
		if abortWon {
			if err = fixture.run.Resume(); err != nil {
				t.Fatal(err)
			}
		}
		if err = fixture.run.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPreparedLocalResumeCancellationDecisionRacesTerminateOnce(t *testing.T) {
	for _, decision := range []string{"commit", "abort"} {
		t.Run(decision, func(t *testing.T) {
			for iteration := range 12 {
				fixture := newLocalResumeFixture(t)
				prepared, err := fixture.run.PrepareLocalResume()
				if err != nil {
					t.Fatal(err)
				}
				next := prepared.NextSequence()
				start := make(chan struct{})
				decisionResult := make(chan error, 1)
				cancelDone := make(chan struct{})
				go func() {
					<-start
					fixture.run.Cancel()
					close(cancelDone)
				}()
				go func() {
					<-start
					if decision == "commit" {
						decisionResult <- prepared.Commit()
						return
					}
					decisionResult <- prepared.Abort()
				}()
				close(start)
				<-cancelDone
				if err = <-decisionResult; err != nil {
					t.Fatalf("iteration %d decision = %v", iteration, err)
				}
				events := collectAfter(t, fixture.run, next-1)
				if err = fixture.run.Wait(t.Context()); err != nil && !errors.Is(err, context.Canceled) {
					t.Fatalf("iteration %d terminal = %v", iteration, err)
				}
				terminal := 0
				for index, envelope := range events {
					if envelope.Sequence() != next+uint64(index) {
						t.Fatalf("iteration %d event %d sequence = %d", iteration, index, envelope.Sequence())
					}
					if envelope.Kind() == event.RunCompleted || envelope.Kind() == event.RunCancelled || envelope.Kind() == event.RunFailed {
						terminal++
					}
				}
				if terminal != 1 {
					t.Fatalf("iteration %d terminal events = %d: %v", iteration, terminal, eventKinds(events))
				}
				if decision == "abort" && fixture.providerCalls() != 1 {
					t.Fatalf("iteration %d abort invoked post-boundary provider: %d", iteration, fixture.providerCalls())
				}
			}
		})
	}
}

func TestPreparedLocalResumeRejectsNilAndNonSuspendedRuns(t *testing.T) {
	var nilRun *agent.Run
	if _, err := nilRun.PrepareLocalResume(); err == nil {
		t.Fatal("nil run prepared a local resume")
	}
	var nilPrepared *agent.PreparedLocalResume
	if nilPrepared.RunID() != "" || nilPrepared.NextSequence() != 0 {
		t.Fatal("nil preparation exposed boundary values")
	}
	if err := nilPrepared.Commit(); err == nil {
		t.Fatal("nil preparation committed")
	}
	if err := nilPrepared.Abort(); err == nil {
		t.Fatal("nil preparation aborted")
	}
	if err := nilPrepared.Close(); err == nil {
		t.Fatal("nil preparation closed")
	}
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{{delta(t, "done"), completed(t)}}}
	engine := newEngine(t, provider, nil, nil, nil)
	run := startRun(t, engine, 1)
	if _, err := run.PrepareLocalResume(); err == nil {
		t.Fatal("active run prepared a local resume")
	}
	if err := run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := run.PrepareLocalResume(); err == nil {
		t.Fatal("terminal run prepared a local resume")
	}
}

func TestPreparedLocalResumeRejectsCancellationBeforeReservation(t *testing.T) {
	fixture := newLocalResumeFixture(t)
	fixture.run.Cancel()
	prepared, err := fixture.run.PrepareLocalResume()
	if prepared != nil {
		t.Fatal("cancelled run prepared a local resume")
	}
	if !errors.Is(err, context.Canceled) {
		failure, ok := errors.AsType[*agent.UnsafeSnapshotError](err)
		if !ok || failure.Status != agent.LifecycleCancelled {
			t.Fatalf("prepare after cancellation = %v", err)
		}
	}
	if err := fixture.run.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run = %v", err)
	}
}

func assertRunStillLatched(t *testing.T, run *agent.Run) {
	t.Helper()
	waitContext, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := run.Wait(waitContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pending local resume did not latch finalization: %v", err)
	}
}

func unsafeResumePending(err error) bool {
	failure, ok := errors.AsType[*agent.UnsafeSnapshotError](err)
	return ok && failure.Status == agent.LifecycleStatus("resume-pending")
}

func assertKindsFromSequence(t *testing.T, events []event.Envelope, first uint64, want []event.Kind) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d: %v", len(events), len(want), eventKinds(events))
	}
	for index, envelope := range events {
		if envelope.Sequence() != first+uint64(index) || envelope.Kind() != want[index] {
			t.Fatalf("event %d = %d/%s, want %d/%s", index, envelope.Sequence(), envelope.Kind(), first+uint64(index), want[index])
		}
	}
}

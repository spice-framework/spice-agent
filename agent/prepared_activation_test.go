package agent_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
)

func TestPreparedRunStartIsInertUntilActivated(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{{completed(t)}}}
	id, _ := stage.NewPlanID("generation:activation-start")
	dispatcher, _ := stage.NewDispatcher(nil)
	source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher}})
	engine := newEngineWithPlanSource(t, provider, source, &agent.AtomicIDSource{}, nil)
	definition, _ := agent.NewDefinition("activation", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))
	prepared, err := engine.PrepareStart(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := prepared.CommitPaused(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if committed.RunID() == "" {
		t.Fatal("committed run identity is empty")
	}
	provider.mu.Lock()
	requestsBefore := len(provider.requests)
	provider.mu.Unlock()
	if requestsBefore != 0 || source.releaseCount(id) != 0 {
		t.Fatalf("inert gate requests=%d releases=%d", requestsBefore, source.releaseCount(id))
	}
	run, err := committed.Activate()
	if err != nil || run.ID() != committed.RunID() {
		t.Fatalf("activate = %#v, %v", run, err)
	}
	if err = run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	requestsAfter := len(provider.requests)
	provider.mu.Unlock()
	if requestsAfter != 1 || source.releaseCount(id) != 1 {
		t.Fatalf("activated requests=%d releases=%d", requestsAfter, source.releaseCount(id))
	}
	if _, err = committed.Activate(); !errors.Is(err, agent.ErrPreparedRunActivated) {
		t.Fatalf("duplicate activation = %v", err)
	}
	if err = committed.Abort(t.Context()); !errors.Is(err, agent.ErrPreparedRunActivated) {
		t.Fatalf("abort after activation = %v", err)
	}
}

func TestPreparedRunAbortFinalizesWithoutProviderWork(t *testing.T) {
	provider := &scriptedProvider{}
	recorder := &event.Recorder{}
	id, _ := stage.NewPlanID("generation:activation-abort")
	dispatcher, _ := stage.NewDispatcher(nil)
	source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher}})
	engine := newEngineWithPlanSource(t, provider, source, &agent.AtomicIDSource{}, []event.Observer{recorder})
	definition, _ := agent.NewDefinition("activation-abort", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))
	prepared, _ := engine.PrepareStart(t.Context(), definition, input)
	committed, err := prepared.CommitPaused(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = committed.Abort(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = committed.Close(); err != nil {
		t.Fatalf("idempotent close = %v", err)
	}
	provider.mu.Lock()
	requests := len(provider.requests)
	provider.mu.Unlock()
	if requests != 0 || source.releaseCount(id) != 1 {
		t.Fatalf("aborted requests=%d releases=%d", requests, source.releaseCount(id))
	}
	if len(recorder.Events()) != 0 {
		t.Fatalf("aborted observer publication = %#v", recorder.Events())
	}
	if _, err = committed.Activate(); !errors.Is(err, agent.ErrPreparedRunAborted) {
		t.Fatalf("activation after abort = %v", err)
	}
}

func TestPreparedResumeAbortPreservesImportedSequenceAndSkipsProvider(t *testing.T) {
	provider := &scriptedProvider{}
	recorder := &event.Recorder{}
	engine, source, snapshot, id := preparedResumeFixture(t, "run-paused-resume", provider, []event.Observer{recorder})
	prepared, err := engine.PrepareResumeSnapshot(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := prepared.CommitPaused(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = committed.Abort(t.Context()); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	requests := len(provider.requests)
	provider.mu.Unlock()
	if requests != 0 || source.releaseCount(id) != 1 || len(recorder.Events()) != 0 {
		t.Fatalf("resume abort requests=%d releases=%d observers=%#v", requests, source.releaseCount(id), recorder.Events())
	}
}

func TestPreparedRunLatchesRootCancellationUntilActivation(t *testing.T) {
	provider := &scriptedProvider{}
	id, _ := stage.NewPlanID("generation:gate-root")
	dispatcher, _ := stage.NewDispatcher(nil)
	source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher}})
	engine := newEngineWithPlanSource(t, provider, source, &agent.AtomicIDSource{}, nil)
	definition, _ := agent.NewDefinition("gate-root", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))
	prepared, _ := engine.PrepareStart(t.Context(), definition, input)
	root, cancel := context.WithCancel(t.Context())
	committed, err := prepared.CommitPaused(root)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	run, err := committed.Activate()
	if err != nil {
		t.Fatalf("activate after root cancellation = %v", err)
	}
	if err = run.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled activated run = %v", err)
	}
	provider.mu.Lock()
	requests := len(provider.requests)
	provider.mu.Unlock()
	if requests != 0 || source.releaseCount(id) != 1 {
		t.Fatalf("latched cancellation requests=%d releases=%d", requests, source.releaseCount(id))
	}
	events := collect(t, run)
	counts := countKinds(events)
	if counts[event.RunStarted] != 1 || counts[event.RunCancelled] != 1 || counts[event.ModelStarted] != 0 {
		t.Fatalf("latched cancellation lifecycle=%v", counts)
	}
}

func TestEngineShutdownWaitsForExplicitPreparedRunDecision(t *testing.T) {
	provider := &scriptedProvider{}
	id, _ := stage.NewPlanID("generation:gate-shutdown")
	dispatcher, _ := stage.NewDispatcher(nil)
	source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher}})
	engine := newEngineWithPlanSource(t, provider, source, &agent.AtomicIDSource{}, nil)
	definition, _ := agent.NewDefinition("gate-shutdown", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))
	prepared, _ := engine.PrepareStart(t.Context(), definition, input)
	committed, err := prepared.CommitPaused(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	shutdownCtx, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	err = engine.Shutdown(shutdownCtx)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown before explicit decision = %v", err)
	}
	if err = committed.Abort(t.Context()); err != nil {
		t.Fatalf("abort after shutdown = %v", err)
	}
	if err = engine.Shutdown(t.Context()); err != nil {
		t.Fatalf("shutdown after abort = %v", err)
	}
	provider.mu.Lock()
	requests := len(provider.requests)
	provider.mu.Unlock()
	if requests != 0 || source.releaseCount(id) != 1 {
		t.Fatalf("shutdown gate requests=%d releases=%d", requests, source.releaseCount(id))
	}
}

func TestPreparedRunRootCancellationAndExplicitAbortSkipsExecution(t *testing.T) {
	for iteration := range 8 {
		t.Run(fmt.Sprintf("iteration-%d", iteration), func(t *testing.T) {
			provider := &scriptedProvider{}
			id, _ := stage.NewPlanID(fmt.Sprintf("generation:gate-abort-%d", iteration))
			dispatcher, _ := stage.NewDispatcher(nil)
			source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher}})
			engine := newEngineWithPlanSource(t, provider, source, &agent.AtomicIDSource{}, nil)
			definition, _ := agent.NewDefinition("gate-abort", "model", 1)
			input, _ := agent.NewInput(inputMessage(t))
			prepared, _ := engine.PrepareStart(t.Context(), definition, input)
			root, cancel := context.WithCancel(t.Context())
			committed, err := prepared.CommitPaused(root)
			if err != nil {
				t.Fatal(err)
			}
			cancel()
			if err = committed.Close(); err != nil {
				t.Fatalf("close = %v", err)
			}
			provider.mu.Lock()
			requests := len(provider.requests)
			provider.mu.Unlock()
			if requests != 0 || source.releaseCount(id) != 1 {
				t.Fatalf("gate cancellation requests=%d releases=%d", requests, source.releaseCount(id))
			}
		})
	}
}

func TestPreparedRunActivateAbortRaceHasOneDecision(t *testing.T) {
	for iteration := range 32 {
		provider := &scriptedProvider{scripts: [][]model.StreamEvent{{completed(t)}}}
		id, _ := stage.NewPlanID(fmt.Sprintf("generation:gate-race-%d", iteration))
		dispatcher, _ := stage.NewDispatcher(nil)
		source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher}})
		engine := newEngineWithPlanSource(t, provider, source, &agent.AtomicIDSource{}, nil)
		definition, _ := agent.NewDefinition("gate-race", "model", 1)
		input, _ := agent.NewInput(inputMessage(t))
		prepared, _ := engine.PrepareStart(t.Context(), definition, input)
		committed, err := prepared.CommitPaused(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		activateResult := make(chan struct {
			run *agent.Run
			err error
		}, 1)
		abortResult := make(chan error, 1)
		var wait sync.WaitGroup
		wait.Go(func() {
			<-start
			run, activateErr := committed.Activate()
			activateResult <- struct {
				run *agent.Run
				err error
			}{run: run, err: activateErr}
		})
		wait.Go(func() {
			<-start
			abortResult <- committed.Abort(t.Context())
		})
		close(start)
		wait.Wait()
		activated, abortErr := <-activateResult, <-abortResult
		switch {
		case activated.err == nil && errors.Is(abortErr, agent.ErrPreparedRunActivated):
			if err = activated.run.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
		case errors.Is(activated.err, agent.ErrPreparedRunAborted) && abortErr == nil:
		default:
			t.Fatalf("race activate=%v abort=%v", activated.err, abortErr)
		}
		if source.releaseCount(id) != 1 {
			t.Fatalf("race releases=%d", source.releaseCount(id))
		}
	}
}

func TestNilPreparedRunFailsClosed(t *testing.T) {
	var prepared *agent.PreparedRun
	if prepared.RunID() != "" {
		t.Fatal("nil prepared run exposed state")
	}
	if _, err := prepared.Activate(); err == nil {
		t.Fatal("nil prepared run activated")
	}
	if err := prepared.Abort(t.Context()); err == nil {
		t.Fatal("nil prepared run aborted")
	}
	if err := prepared.Close(); err == nil {
		t.Fatal("nil prepared run closed")
	}
}

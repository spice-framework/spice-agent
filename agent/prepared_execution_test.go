package agent_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
)

type preparedDecision interface {
	Commit(context.Context) (*agent.Run, error)
	Abort() error
}

// cancelBeforeExecutionContext stays live through preparation and Commit's
// explicit precheck, then cancels as soon as context.WithCancel observes Done.
// That deterministically places cancellation before the execution goroutine.
type cancelBeforeExecutionContext struct {
	done chan struct{}
	once sync.Once
}

func newCancelBeforeExecutionContext() *cancelBeforeExecutionContext {
	return &cancelBeforeExecutionContext{done: make(chan struct{})}
}

func (*cancelBeforeExecutionContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (ctx *cancelBeforeExecutionContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.done) })
	return ctx.done
}

func (ctx *cancelBeforeExecutionContext) Err() error {
	select {
	case <-ctx.done:
		return context.Canceled
	default:
		return nil
	}
}

func (*cancelBeforeExecutionContext) Value(any) any { return nil }

func TestPreparedStartSeparatesSetupFromRunLifetime(t *testing.T) {
	id, _ := stage.NewPlanID("generation:prepared-start")
	dispatcher, _ := stage.NewDispatcher(nil)
	source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher}})
	recorder := &event.Recorder{}
	engine := newEngineWithPlanSource(t, blockingProvider{}, source, &countingIDSource{}, []event.Observer{recorder})
	definition, _ := agent.NewDefinition("prepared", "model", 2)
	input, _ := agent.NewInput(inputMessage(t))
	setupContext, cancelSetup := context.WithCancel(t.Context())
	prepared, err := engine.PrepareStart(setupContext, definition, input)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.RunID() != "run-counted" || source.releaseCount(id) != 0 || len(recorder.Events()) != 0 {
		t.Fatalf("prepared start mutated execution: id=%q releases=%d events=%d", prepared.RunID(), source.releaseCount(id), len(recorder.Events()))
	}
	cancelSetup()
	runRoot, cancelRun := context.WithCancel(t.Context())
	run, err := prepared.Commit(runRoot)
	if err != nil || run.ID() != prepared.RunID() {
		t.Fatalf("commit = %#v, %v", run, err)
	}
	events := cancelAtModelStart(t, run, 0, cancelRun)
	if err = run.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("run root cancellation = %v", err)
	}
	counts := countKinds(events)
	if counts[event.RunStarted] != 1 || counts[event.ModelStarted] != 1 || counts[event.RunCancelled] != 1 || source.releaseCount(id) != 1 {
		t.Fatalf("prepared start lifecycle = %v, releases=%d", counts, source.releaseCount(id))
	}
	if _, err = prepared.Commit(t.Context()); !errors.Is(err, agent.ErrPreparedExecutionCommitted) {
		t.Fatalf("second commit = %v", err)
	}
	if err = prepared.Close(); !errors.Is(err, agent.ErrPreparedExecutionCommitted) {
		t.Fatalf("close after commit = %v", err)
	}
}

func TestStartImmediateCancellationStillFinalizesRegisteredRun(t *testing.T) {
	id, _ := stage.NewPlanID("generation:immediate-cancel")
	dispatcher, _ := stage.NewDispatcher(nil)
	source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher}})
	engine := newEngineWithPlanSource(t, blockingProvider{}, source, &agent.AtomicIDSource{}, nil)
	definition, _ := agent.NewDefinition("immediate-cancel", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))
	run, err := engine.Start(newCancelBeforeExecutionContext(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = run.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait = %v", err)
	}
	events := collect(t, run)
	counts := countKinds(events)
	terminalCount := counts[event.RunCompleted] + counts[event.RunFailed] + counts[event.RunCancelled]
	if counts[event.RunStarted] != 1 || counts[event.RunCancelled] != 1 || terminalCount != 1 || source.releaseCount(id) != 1 {
		t.Fatalf("lifecycle = %v, releases=%d", counts, source.releaseCount(id))
	}
}

func TestRegisteredRunFinalizesAfterLifecycleStartFailures(t *testing.T) {
	t.Run("uncommitted oversized start", func(t *testing.T) {
		id, _ := stage.NewPlanID("generation:oversized-start")
		dispatcher, _ := stage.NewDispatcher(nil)
		source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher}})
		engine := newEngineWithPlanSource(t, blockingProvider{}, source, &agent.AtomicIDSource{}, nil)
		definition, err := agent.NewDefinition(strings.Repeat("x", event.MaximumPayloadBytes), "model", 1)
		if err != nil {
			t.Fatal(err)
		}
		input, _ := agent.NewInput(inputMessage(t))
		run, err := engine.Start(t.Context(), definition, input)
		if err != nil {
			t.Fatal(err)
		}
		if err = run.Wait(t.Context()); err == nil {
			t.Fatal("oversized lifecycle start did not fail the run")
		}
		events := collect(t, run)
		if len(events) != 1 || events[0].Sequence() != 1 || events[0].Kind() != event.RunFailed || source.releaseCount(id) != 1 {
			t.Fatalf("uncommitted start events = %#v, releases=%d", events, source.releaseCount(id))
		}
	})

	t.Run("committed observer failure", func(t *testing.T) {
		id, _ := stage.NewPlanID("generation:observer-start")
		dispatcher, _ := stage.NewDispatcher(nil)
		source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher}})
		engine := newEngineWithPlanSource(
			t, blockingProvider{}, source, &agent.AtomicIDSource{}, []event.Observer{failingObserver{event.RunStarted}},
		)
		definition, _ := agent.NewDefinition("observer-start", "model", 1)
		input, _ := agent.NewInput(inputMessage(t))
		run, err := engine.Start(t.Context(), definition, input)
		if err != nil {
			t.Fatal(err)
		}
		if err = run.Wait(t.Context()); err == nil {
			t.Fatal("observer lifecycle-start failure did not fail the run")
		}
		events := collect(t, run)
		assertKindsAndSequence(t, events, []event.Kind{event.RunStarted, event.RunFailed})
		if source.releaseCount(id) != 1 {
			t.Fatalf("committed start releases = %d", source.releaseCount(id))
		}
	})
}

func TestPreparedResumeSeparatesSetupFromRunLifetime(t *testing.T) {
	recorder := &event.Recorder{}
	engine, source, snapshot, id := preparedResumeFixture(t, "run-prepared-resume", blockingProvider{}, []event.Observer{recorder})
	setupContext, cancelSetup := context.WithCancel(t.Context())
	prepared, err := engine.PrepareResumeSnapshot(setupContext, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.RunID() != snapshot.RunID() || source.releaseCount(id) != 0 || len(recorder.Events()) != 0 {
		t.Fatalf("prepared resume = %q, releases=%d, events=%d", prepared.RunID(), source.releaseCount(id), len(recorder.Events()))
	}
	cancelSetup()
	runRoot, cancelRun := context.WithCancel(t.Context())
	run, err := prepared.Commit(runRoot)
	if err != nil {
		t.Fatal(err)
	}
	events := cancelAtModelStart(t, run, snapshot.LastSequence(), cancelRun)
	if err = run.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("resumed root cancellation = %v", err)
	}
	counts := countKinds(events)
	if counts[event.RunStarted] != 0 || counts[event.ModelStarted] != 1 || counts[event.RunCancelled] != 1 || source.releaseCount(id) != 1 {
		t.Fatalf("prepared resume lifecycle = %v, releases=%d", counts, source.releaseCount(id))
	}
}

func TestPreparedAbortAndCloseReleaseExactlyOnce(t *testing.T) {
	id, _ := stage.NewPlanID("generation:prepared-abort")
	dispatcher, _ := stage.NewDispatcher(nil)
	releaseFailure := errors.New("generation release failed")
	source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher, releaseErr: releaseFailure}})
	engine := newEngineWithPlanSource(t, blockingProvider{}, source, &countingIDSource{}, nil)
	definition, _ := agent.NewDefinition("prepared", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))
	prepared, err := engine.PrepareStart(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	first := prepared.Abort()
	second := prepared.Close()
	third := prepared.Abort()
	if first == nil || !strings.Contains(first.Error(), releaseFailure.Error()) ||
		second == nil || second.Error() != first.Error() ||
		third == nil || third.Error() != first.Error() || source.releaseCount(id) != 1 {
		t.Fatalf("abort results = %v / %v / %v, releases=%d", first, second, third, source.releaseCount(id))
	}
	if _, err = prepared.Commit(t.Context()); !errors.Is(err, agent.ErrPreparedExecutionAborted) || source.releaseCount(id) != 1 {
		t.Fatalf("commit after abort = %v, releases=%d", err, source.releaseCount(id))
	}
}

func TestPreparedCommitAndAbortRaceTransfersOwnershipOnce(t *testing.T) {
	for _, kind := range []string{"start", "resume"} {
		t.Run(kind, func(t *testing.T) {
			for iteration := range 32 {
				id, _ := stage.NewPlanID(fmt.Sprintf("generation:%s-%d", kind, iteration))
				dispatcher, _ := stage.NewDispatcher(nil)
				source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher}})
				var prepared preparedDecision
				if kind == "start" {
					engine := newEngineWithPlanSource(t, blockingProvider{}, source, &countingIDSource{}, nil)
					definition, _ := agent.NewDefinition("prepared", "model", 1)
					input, _ := agent.NewInput(inputMessage(t))
					value, err := engine.PrepareStart(t.Context(), definition, input)
					if err != nil {
						t.Fatal(err)
					}
					prepared = value
				} else {
					engine, _, snapshot, _ := preparedResumeFixtureWithPlan(t, fmt.Sprintf("run-race-%d", iteration), blockingProvider{}, nil, id, dispatcher, source)
					value, err := engine.PrepareResumeSnapshot(t.Context(), snapshot)
					if err != nil {
						t.Fatal(err)
					}
					prepared = value
				}

				start := make(chan struct{})
				commitResult := make(chan struct {
					run *agent.Run
					err error
				}, 1)
				abortResult := make(chan error, 1)
				var wait sync.WaitGroup
				wait.Go(func() {
					<-start
					run, commitErr := prepared.Commit(t.Context())
					commitResult <- struct {
						run *agent.Run
						err error
					}{run, commitErr}
				})
				wait.Go(func() {
					<-start
					abortResult <- prepared.Abort()
				})
				close(start)
				wait.Wait()
				committed := <-commitResult
				abortErr := <-abortResult
				if committed.run != nil {
					if committed.err != nil || !errors.Is(abortErr, agent.ErrPreparedExecutionCommitted) {
						t.Fatalf("commit winner = run %#v, commit %v, abort %v", committed.run, committed.err, abortErr)
					}
					committed.run.Cancel()
					_ = committed.run.Wait(t.Context())
				} else if !errors.Is(committed.err, agent.ErrPreparedExecutionAborted) || abortErr != nil {
					t.Fatalf("abort winner = commit %v, abort %v", committed.err, abortErr)
				}
				if source.releaseCount(id) != 1 {
					t.Fatalf("ownership release count = %d", source.releaseCount(id))
				}
			}
		})
	}
}

func TestPreparedDuplicateCommitAndEngineCloseFailClosed(t *testing.T) {
	engine, source, snapshot, id := preparedResumeFixture(t, "run-exclusive", blockingProvider{}, nil)
	first, err := engine.PrepareResumeSnapshot(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.PrepareResumeSnapshot(t.Context(), snapshot)
	if err == nil || !strings.Contains(err.Error(), "already imported") {
		t.Fatalf("duplicate preparation = %#v, %v", second, err)
	}
	if source.generationCallCount() != 1 {
		t.Fatalf("parallel preparation acquisitions = %d", source.generationCallCount())
	}
	run, err := first.Commit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if source.releaseCount(id) != 0 {
		t.Fatalf("duplicate preparation releases=%d", source.releaseCount(id))
	}
	if _, err = engine.PrepareResumeSnapshot(t.Context(), snapshot); err == nil || source.generationCallCount() != 1 {
		t.Fatalf("postcommit preparation = %v, acquisitions=%d", err, source.generationCallCount())
	}
	run.Cancel()
	_ = run.Wait(t.Context())
	if source.releaseCount(id) != 1 {
		t.Fatalf("duplicate ownership releases = %d", source.releaseCount(id))
	}

	closedID, _ := stage.NewPlanID("generation:closed-prepared")
	dispatcher, _ := stage.NewDispatcher(nil)
	closedSource := newFakePlanSource(closedID, map[stage.PlanID]fakePlanRecord{closedID: {dispatcher: dispatcher}})
	closedEngine := newEngineWithPlanSource(t, blockingProvider{}, closedSource, &countingIDSource{}, nil)
	definition, _ := agent.NewDefinition("prepared", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))
	prepared, _ := closedEngine.PrepareStart(t.Context(), definition, input)
	if err = closedEngine.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = prepared.Commit(t.Context()); err == nil || !strings.Contains(err.Error(), "engine is closed") || closedSource.releaseCount(closedID) != 1 {
		t.Fatalf("commit into closed engine = %v, releases=%d", err, closedSource.releaseCount(closedID))
	}
	closedSource.mu.Lock()
	acquisitions := closedSource.currentCalls
	closedSource.mu.Unlock()
	if _, err = closedEngine.PrepareStart(t.Context(), definition, input); err == nil {
		t.Fatal("prepare on closed engine succeeded")
	}
	closedSource.mu.Lock()
	defer closedSource.mu.Unlock()
	if closedSource.currentCalls != acquisitions {
		t.Fatalf("closed engine acquired a plan: %d -> %d", acquisitions, closedSource.currentCalls)
	}
}

func TestPreparedCanceledAndNilPathsDoNotPublishAuthority(t *testing.T) {
	id, _ := stage.NewPlanID("generation:cancelled-prepared")
	dispatcher, _ := stage.NewDispatcher(nil)
	source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher}})
	engine := newEngineWithPlanSource(t, blockingProvider{}, source, &countingIDSource{}, nil)
	definition, _ := agent.NewDefinition("prepared", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := engine.PrepareStart(cancelled, definition, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled setup = %v", err)
	}
	source.mu.Lock()
	if source.currentCalls != 0 {
		t.Fatalf("cancelled setup acquisitions = %d", source.currentCalls)
	}
	source.mu.Unlock()
	prepared, err := engine.PrepareStart(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = prepared.Commit(cancelled); !errors.Is(err, context.Canceled) || source.releaseCount(id) != 1 {
		t.Fatalf("cancelled commit = %v, releases=%d", err, source.releaseCount(id))
	}
	retry, err := engine.PrepareStart(t.Context(), definition, input)
	if err != nil {
		t.Fatalf("cancelled commit published run authority: %v", err)
	}
	if err = retry.Close(); err != nil || source.releaseCount(id) != 2 {
		t.Fatalf("retry close = %v, releases=%d", err, source.releaseCount(id))
	}
	nilRoot, err := engine.PrepareStart(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	//nolint:staticcheck // Deliberate nil input proves the public boundary fails closed.
	if _, err = nilRoot.Commit(nil); err == nil || source.releaseCount(id) != 3 {
		t.Fatalf("nil run root = %v, releases=%d", err, source.releaseCount(id))
	}

	resumeEngine, resumeSource, snapshot, _ := preparedResumeFixture(t, "run-cancelled-prepare", blockingProvider{}, nil)
	if _, err = resumeEngine.PrepareResumeSnapshot(cancelled, snapshot); !errors.Is(err, context.Canceled) || resumeSource.generationCallCount() != 0 {
		t.Fatalf("cancelled resume setup = %v, acquisitions=%d", err, resumeSource.generationCallCount())
	}

	var nilEngine *agent.Engine
	if _, err = nilEngine.PrepareStart(t.Context(), definition, input); err == nil {
		t.Fatal("nil engine start preparation succeeded")
	}
	//nolint:staticcheck // Deliberate nil input proves the public boundary fails closed.
	if _, err = engine.PrepareStart(nil, definition, input); err == nil {
		t.Fatal("nil setup context succeeded")
	}
	if _, err = nilEngine.PrepareResumeSnapshot(t.Context(), snapshot); err == nil {
		t.Fatal("nil engine resume preparation succeeded")
	}
	//nolint:staticcheck // Deliberate nil input proves the public boundary fails closed.
	if _, err = resumeEngine.PrepareResumeSnapshot(nil, snapshot); err == nil {
		t.Fatal("nil resume setup context succeeded")
	}
	var nilStart *agent.PreparedStart
	if nilStart.RunID() != "" {
		t.Fatal("nil prepared start has an ID")
	}
	if _, err = nilStart.Commit(t.Context()); err == nil {
		t.Fatal("nil prepared start committed")
	}
	if err = nilStart.Close(); err == nil {
		t.Fatal("nil prepared start closed successfully")
	}
	var nilResume *agent.PreparedResume
	if nilResume.RunID() != "" {
		t.Fatal("nil prepared resume has an ID")
	}
	if _, err = nilResume.Commit(t.Context()); err == nil {
		t.Fatal("nil prepared resume committed")
	}
	if err = nilResume.Abort(); err == nil {
		t.Fatal("nil prepared resume aborted successfully")
	}
}

func preparedResumeFixture(
	t *testing.T,
	runID string,
	provider model.Provider,
	observers []event.Observer,
) (*agent.Engine, *fakePlanSource, agent.Snapshot, stage.PlanID) {
	t.Helper()
	id, _ := stage.NewPlanID("generation:prepared-resume")
	dispatcher, err := stage.NewDispatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: dispatcher}})
	return preparedResumeFixtureWithPlan(t, runID, provider, observers, id, dispatcher, source)
}

func preparedResumeFixtureWithPlan(
	t *testing.T,
	runID string,
	provider model.Provider,
	observers []event.Observer,
	id stage.PlanID,
	dispatcher stage.ToolDispatcher,
	source *fakePlanSource,
) (*agent.Engine, *fakePlanSource, agent.Snapshot, stage.PlanID) {
	t.Helper()
	engine := newEngineWithPlanSource(t, provider, source, &agent.AtomicIDSource{}, observers)
	identity, err := agent.NewPlanIdentity(
		agent.DefaultEngineOptions().CompiledPlanIdentities,
		testSnapshotCompatibility,
		testWorkspaceFingerprint,
		id,
		dispatcher.Definitions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := agent.NewDefinition("prepared", "model", 2)
	snapshot, err := agent.NewSnapshot(
		runID, definition, 1, []message.Message{inputMessage(t)}, identity, 4, agent.LifecycleSuspended,
	)
	if err != nil {
		t.Fatal(err)
	}
	return engine, source, snapshot, id
}

func cancelAtModelStart(t *testing.T, run *agent.Run, after uint64, cancel context.CancelFunc) []event.Envelope {
	t.Helper()
	ctx, stop := context.WithTimeout(t.Context(), 2*time.Second)
	defer stop()
	subscription, err := run.Subscribe(ctx, after)
	if err != nil {
		t.Fatal(err)
	}
	var events []event.Envelope
	for envelope := range subscription.Events() {
		events = append(events, envelope)
		if envelope.Kind() == event.ModelStarted {
			cancel()
		}
	}
	if err = subscription.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	return events
}

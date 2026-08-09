package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

type fakePlanRecord struct {
	dispatcher   stage.ToolDispatcher
	releaseErr   error
	releaseBlock <-chan struct{}
}

type fakePlanSource struct {
	mu                sync.Mutex
	current           stage.PlanID
	plans             map[stage.PlanID]fakePlanRecord
	currentErr        error
	panicCurrent      bool
	nilCurrent        bool
	currentWithErr    bool
	generationAs      stage.PlanID
	currentBlock      <-chan struct{}
	generationBlock   <-chan struct{}
	currentStarted    chan struct{}
	generationStarted chan struct{}
	ignoreContext     bool
	currentCalls      int
	generationCalls   []stage.PlanID
	releases          map[stage.PlanID]int
}

func newFakePlanSource(current stage.PlanID, plans map[stage.PlanID]fakePlanRecord) *fakePlanSource {
	return &fakePlanSource{current: current, plans: plans, releases: make(map[stage.PlanID]int)}
}

func (source *fakePlanSource) LeaseCurrent(ctx context.Context) (*stage.ToolPlanLease, error) {
	source.mu.Lock()
	source.currentCalls++
	panicCurrent := source.panicCurrent
	nilCurrent := source.nilCurrent
	err := source.currentErr
	id := source.current
	source.mu.Unlock()
	if source.currentStarted != nil {
		close(source.currentStarted)
	}
	if source.currentBlock != nil {
		<-source.currentBlock
	}
	if panicCurrent {
		panic("SECRET source panic")
	}
	if err != nil {
		if source.currentWithErr {
			lease, leaseErr := source.lease(ctx, id)
			if leaseErr != nil {
				return nil, leaseErr
			}
			return lease, err
		}
		return nil, err
	}
	if nilCurrent {
		//nolint:nilnil // Deliberate broken-source fixture proves host fail-closed validation.
		return nil, nil
	}
	return source.lease(ctx, id)
}

func (source *fakePlanSource) LeaseGeneration(ctx context.Context, id stage.PlanID) (*stage.ToolPlanLease, error) {
	source.mu.Lock()
	source.generationCalls = append(source.generationCalls, id)
	if source.generationAs != "" {
		id = source.generationAs
	}
	source.mu.Unlock()
	if source.generationStarted != nil {
		close(source.generationStarted)
	}
	if source.generationBlock != nil {
		<-source.generationBlock
	}
	return source.lease(ctx, id)
}

func (source *fakePlanSource) lease(ctx context.Context, id stage.PlanID) (*stage.ToolPlanLease, error) {
	if err := ctx.Err(); err != nil && !source.ignoreContext {
		return nil, err
	}
	source.mu.Lock()
	record, found := source.plans[id]
	source.mu.Unlock()
	if !found {
		return nil, errors.New("requested tool plan generation is unavailable")
	}
	return stage.NewToolPlanLease(id, record.dispatcher, func() error {
		source.mu.Lock()
		source.releases[id]++
		source.mu.Unlock()
		if record.releaseBlock != nil {
			<-record.releaseBlock
		}
		return record.releaseErr
	})
}

func (source *fakePlanSource) generationCallCount() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return len(source.generationCalls)
}

func (source *fakePlanSource) setCurrent(id stage.PlanID) {
	source.mu.Lock()
	source.current = id
	source.mu.Unlock()
}

func (source *fakePlanSource) releaseCount(id stage.PlanID) int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.releases[id]
}

type labeledTool struct {
	label string
	calls chan<- string
}

func (implementation labeledTool) Definition() tool.Definition {
	definition, _ := tool.NewDefinition("read", "Read a fixture.", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe)
	return definition
}

func (implementation labeledTool) Execute(_ context.Context, call tool.Call, _ tool.Reporter) (tool.Result, error) {
	implementation.calls <- implementation.label
	result, _ := tool.NewResult(call.ID(), json.RawMessage(`"`+implementation.label+`"`))
	return result, nil
}

func TestRunsLeaseCurrentPlanAndKeepOldAndNewGenerationsSeparate(t *testing.T) {
	oldID, _ := stage.NewPlanID("generation:old")
	newID, _ := stage.NewPlanID("generation:new")
	calls := make(chan string, 2)
	oldDispatcher, _ := stage.NewDispatcher(map[string]tool.Tool{"read": labeledTool{label: "old", calls: calls}})
	newDispatcher, _ := stage.NewDispatcher(map[string]tool.Tool{"read": labeledTool{label: "new", calls: calls}})
	source := newFakePlanSource(oldID, map[stage.PlanID]fakePlanRecord{
		oldID: {dispatcher: oldDispatcher},
		newID: {dispatcher: newDispatcher},
	})
	call, _ := tool.NewCall("call-1", "read", json.RawMessage(`{}`))
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{
		{toolEvent(t, call), completed(t)},
		{delta(t, "old done"), completed(t)},
		{toolEvent(t, call), completed(t)},
		{delta(t, "new done"), completed(t)},
	}}
	engine := newEngineWithPlanSource(t, provider, source, &agent.AtomicIDSource{}, nil)
	first := startRun(t, engine, 3)
	firstIdentity := first.PlanIdentity()
	source.setCurrent(newID)
	_ = collect(t, first)
	if err := first.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := <-calls; got != "old" || first.ToolPlanID() != oldID ||
		firstIdentity.ToolPlanID() != oldID || firstIdentity.Fingerprint() == "" {
		t.Fatalf("first plan/call = %q, %q, %#v", got, first.ToolPlanID(), firstIdentity)
	}
	compiled := firstIdentity.CompiledIdentities()
	compiled[0] = "broker:mutated"
	if first.PlanIdentity().CompiledIdentities()[0] == "broker:mutated" {
		t.Fatal("run plan identity was mutable")
	}
	second := startRun(t, engine, 3)
	_ = collect(t, second)
	if err := second.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := <-calls; got != "new" || second.ToolPlanID() != newID ||
		second.PlanIdentity().Fingerprint() == firstIdentity.Fingerprint() {
		t.Fatalf("second plan/call = %q, %q", got, second.ToolPlanID())
	}
	if source.releaseCount(oldID) != 1 || source.releaseCount(newID) != 1 {
		t.Fatalf("plan releases old=%d new=%d", source.releaseCount(oldID), source.releaseCount(newID))
	}
}

type countingIDSource struct{ calls atomic.Int32 }

func (source *countingIDSource) Next(string) (string, error) {
	source.calls.Add(1)
	return "run-counted", nil
}

func TestPlanAcquireFailureMutatesNothingAndEmitsNoRun(t *testing.T) {
	id, _ := stage.NewPlanID("generation:failed")
	empty, _ := stage.NewDispatcher(nil)
	for name, test := range map[string]struct {
		configure        func(*fakePlanSource)
		expectedReleases int
	}{
		"error": {func(source *fakePlanSource) { source.currentErr = errors.New("unavailable") }, 0},
		"nil":   {func(source *fakePlanSource) { source.nilCurrent = true }, 0},
		"panic": {func(source *fakePlanSource) { source.panicCurrent = true }, 0},
		"lease and error": {func(source *fakePlanSource) {
			source.currentErr = errors.New("ambiguous acquisition")
			source.currentWithErr = true
		}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: empty}})
			test.configure(source)
			ids := &countingIDSource{}
			recorder := &event.Recorder{}
			engine := newEngineWithPlanSource(t, blockingProvider{}, source, ids, []event.Observer{recorder})
			definition, _ := agent.NewDefinition("test", "model", 1)
			input, _ := agent.NewInput(inputMessage(t))
			if _, err := engine.Start(t.Context(), definition, input); err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("acquire error = %v", err)
			}
			if ids.calls.Load() != 0 || len(recorder.Events()) != 0 || source.releaseCount(id) != test.expectedReleases {
				t.Fatalf("acquire mutated state: ids=%d events=%d releases=%d", ids.calls.Load(), len(recorder.Events()), source.releaseCount(id))
			}
		})
	}
}

func TestPlanLeaseReleasesOnceAcrossTerminalPathsAndRollback(t *testing.T) {
	id, _ := stage.NewPlanID("generation:terminal")
	empty, _ := stage.NewDispatcher(nil)
	for _, test := range []struct {
		name      string
		provider  model.Provider
		observers []event.Observer
		cancel    bool
	}{
		{"success", &scriptedProvider{scripts: [][]model.StreamEvent{{completed(t)}}}, nil, false},
		{"provider failure", &scriptedProvider{startErr: errors.New("provider unavailable")}, nil, false},
		{"panic", &scriptedProvider{panicAt: true}, nil, false},
		{"observer failure", &scriptedProvider{scripts: [][]model.StreamEvent{{completed(t)}}}, []event.Observer{failingObserver{event.RunStarted}}, false},
		{"cancel", blockingProvider{}, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: empty}})
			engine := newEngineWithPlanSource(t, test.provider, source, &agent.AtomicIDSource{}, test.observers)
			run := startRun(t, engine, 1)
			if test.cancel {
				run.Cancel()
			}
			_ = collect(t, run)
			_ = run.Wait(t.Context())
			if source.releaseCount(id) != 1 {
				t.Fatalf("release count = %d", source.releaseCount(id))
			}
			run.Cancel()
			_ = run.Wait(t.Context())
			if source.releaseCount(id) != 1 {
				t.Fatalf("release count after repeated terminal calls = %d", source.releaseCount(id))
			}
		})
	}

	source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: empty}})
	engine := newEngineWithPlanSource(t, blockingProvider{}, source, fixedIDSource{value: "same"}, nil)
	first := startRun(t, engine, 1)
	definition, _ := agent.NewDefinition("test", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))
	if _, err := engine.Start(t.Context(), definition, input); err == nil {
		t.Fatal("duplicate start succeeded")
	}
	if source.releaseCount(id) != 1 {
		t.Fatalf("rollback release count = %d", source.releaseCount(id))
	}
	first.Cancel()
	_ = first.Wait(t.Context())
	if source.releaseCount(id) != 2 {
		t.Fatalf("rollback plus run release count = %d", source.releaseCount(id))
	}
}

func TestReleaseFailureSelectsOneAuthoritativeFailedTerminal(t *testing.T) {
	id, _ := stage.NewPlanID("generation:release-failure")
	empty, _ := stage.NewDispatcher(nil)
	source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{
		id: {dispatcher: empty, releaseErr: errors.New("generation drain failed")},
	})
	engine := newEngineWithPlanSource(
		t,
		&scriptedProvider{scripts: [][]model.StreamEvent{{completed(t)}}},
		source,
		&agent.AtomicIDSource{},
		nil,
	)
	run := startRun(t, engine, 1)
	events := collect(t, run)
	err := run.Wait(t.Context())
	if err == nil || !strings.Contains(err.Error(), "generation drain failed") {
		t.Fatalf("Wait error = %v", err)
	}
	counts := countKinds(events)
	if counts[event.RunFailed] != 1 || counts[event.RunCompleted] != 0 || source.releaseCount(id) != 1 {
		t.Fatalf("terminal/release counts = %v/%d", counts, source.releaseCount(id))
	}
	snapshot, snapshotErr := run.ExportSnapshot()
	if snapshotErr != nil || snapshot.Status() != agent.LifecycleFailed {
		t.Fatalf("release failure snapshot = %s, %v", snapshot.Status(), snapshotErr)
	}
}

func TestBlockingReleaseCannotSuppressAuthoritativeTerminal(t *testing.T) {
	id, _ := stage.NewPlanID("generation:blocking-release")
	empty, _ := stage.NewDispatcher(nil)
	block := make(chan struct{})
	source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{
		id: {dispatcher: empty, releaseBlock: block},
	})
	options := agent.DefaultEngineOptions()
	options.FinalizationTimeout = 20 * time.Millisecond
	options.SnapshotCompatibilityIdentity = testSnapshotCompatibility
	options.WorkspaceFingerprint = testWorkspaceFingerprint
	engine, err := agent.NewEngineWithToolPlanSource(
		&scriptedProvider{scripts: [][]model.StreamEvent{{completed(t)}}}, source,
		&agent.AtomicIDSource{}, time.Now, nil, nil, options,
	)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	run := startRun(t, engine, 1)
	events := collect(t, run)
	waitErr := run.Wait(t.Context())
	if waitErr == nil || !strings.Contains(waitErr.Error(), "release") || time.Since(started) > time.Second {
		t.Fatalf("bounded release wait = %v after %s", waitErr, time.Since(started))
	}
	if counts := countKinds(events); counts[event.RunFailed] != 1 || counts[event.RunCompleted] != 0 {
		t.Fatalf("blocking release terminals = %v", counts)
	}
	if source.releaseCount(id) != 1 {
		t.Fatalf("blocking release starts = %d", source.releaseCount(id))
	}
	close(block)
	if second := run.Wait(t.Context()); second.Error() != waitErr.Error() || source.releaseCount(id) != 1 {
		t.Fatalf("repeated wait/release = %v/%d", second, source.releaseCount(id))
	}
}

func TestCancelledAcquisitionsReleaseBeforeStartOrResumeMutation(t *testing.T) {
	id, _ := stage.NewPlanID("generation:delayed")
	empty, _ := stage.NewDispatcher(nil)

	t.Run("start", func(t *testing.T) {
		block := make(chan struct{})
		source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: empty}})
		source.currentBlock = block
		source.currentStarted = make(chan struct{})
		source.ignoreContext = true
		ids := &countingIDSource{}
		recorder := &event.Recorder{}
		engine := newEngineWithPlanSource(t, blockingProvider{}, source, ids, []event.Observer{recorder})
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 1)
		go func() {
			definition, _ := agent.NewDefinition("test", "model", 1)
			input, _ := agent.NewInput(inputMessage(t))
			_, startErr := engine.Start(ctx, definition, input)
			result <- startErr
		}()
		<-source.currentStarted
		cancel()
		close(block)
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("delayed start acquisition = %v", err)
		}
		if ids.calls.Load() != 0 || len(recorder.Events()) != 0 || source.releaseCount(id) != 1 {
			t.Fatalf("delayed start mutated: ids=%d events=%d releases=%d", ids.calls.Load(), len(recorder.Events()), source.releaseCount(id))
		}
	})

	t.Run("resume", func(t *testing.T) {
		identity, err := agent.NewPlanIdentity(
			agent.DefaultEngineOptions().CompiledPlanIdentities,
			testSnapshotCompatibility,
			testWorkspaceFingerprint,
			id,
			empty.Definitions(),
		)
		if err != nil {
			t.Fatal(err)
		}
		definition, _ := agent.NewDefinition("test", "model", 2)
		snapshot, err := agent.NewSnapshot(
			"run-delayed", definition, 1, []message.Message{inputMessage(t)},
			identity, 4, agent.LifecycleSuspended,
		)
		if err != nil {
			t.Fatal(err)
		}
		block := make(chan struct{})
		source := newFakePlanSource(id, map[stage.PlanID]fakePlanRecord{id: {dispatcher: empty}})
		source.generationBlock = block
		source.generationStarted = make(chan struct{})
		source.ignoreContext = true
		engine := newEngineWithPlanSource(t, blockingProvider{}, source, &agent.AtomicIDSource{}, nil)
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 1)
		go func() {
			_, resumeErr := engine.ResumeSnapshot(ctx, snapshot)
			result <- resumeErr
		}()
		<-source.generationStarted
		cancel()
		close(block)
		if err = <-result; !errors.Is(err, context.Canceled) || source.releaseCount(id) != 1 {
			t.Fatalf("delayed resume acquisition = %v, releases=%d", err, source.releaseCount(id))
		}
		source.generationStarted = nil
		source.generationBlock = nil
		run, resumeErr := engine.ResumeSnapshot(t.Context(), snapshot)
		if resumeErr != nil {
			t.Fatalf("resume after cancelled import = %v", resumeErr)
		}
		run.Cancel()
		_ = run.Wait(t.Context())
		if source.releaseCount(id) != 2 {
			t.Fatalf("second resume release count = %d", source.releaseCount(id))
		}
	})
}

func TestResumeSnapshotLeasesExactRecordedPlan(t *testing.T) {
	oldID, _ := stage.NewPlanID("generation:resume-old")
	newID, _ := stage.NewPlanID("generation:resume-new")
	oldDispatcher, _ := stage.NewDispatcher(map[string]tool.Tool{"read": testTool{}})
	newDispatcher, _ := stage.NewDispatcher(nil)
	compiled := agent.DefaultEngineOptions().CompiledPlanIdentities
	identity, err := agent.NewPlanIdentity(
		compiled, testSnapshotCompatibility, testWorkspaceFingerprint, oldID, oldDispatcher.Definitions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := agent.NewDefinition("test", "model", 3)
	snapshot, err := agent.NewSnapshot(
		"run-resume",
		definition,
		1,
		[]message.Message{inputMessage(t)},
		identity,
		7,
		agent.LifecycleSuspended,
	)
	if err != nil {
		t.Fatal(err)
	}
	defaultSource := newFakePlanSource(oldID, map[stage.PlanID]fakePlanRecord{oldID: {dispatcher: oldDispatcher}})
	defaultEngine, err := agent.NewEngineWithToolPlanSource(
		blockingProvider{}, defaultSource, &agent.AtomicIDSource{}, time.Now, nil, nil,
		agent.DefaultEngineOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = defaultEngine.ResumeSnapshot(t.Context(), snapshot); err == nil ||
		!strings.Contains(err.Error(), "compatibility identity") || defaultSource.generationCallCount() != 0 {
		t.Fatalf("default portable resume = %v, generation calls=%d", err, defaultSource.generationCallCount())
	}
	mismatchSource := newFakePlanSource(oldID, map[stage.PlanID]fakePlanRecord{oldID: {dispatcher: oldDispatcher}})
	mismatchOptions := agent.DefaultEngineOptions()
	mismatchOptions.SnapshotCompatibilityIdentity = "tests:v2"
	mismatchOptions.WorkspaceFingerprint = testWorkspaceFingerprint
	mismatchEngine, err := agent.NewEngineWithToolPlanSource(
		blockingProvider{}, mismatchSource, &agent.AtomicIDSource{}, time.Now, nil, nil, mismatchOptions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mismatchEngine.ResumeSnapshot(t.Context(), snapshot); err == nil ||
		!strings.Contains(err.Error(), "compiled compatibility") || mismatchSource.generationCallCount() != 0 {
		t.Fatalf("incompatible resume = %v, generation calls=%d", err, mismatchSource.generationCallCount())
	}
	workspaceMismatchSource := newFakePlanSource(oldID, map[stage.PlanID]fakePlanRecord{oldID: {dispatcher: oldDispatcher}})
	workspaceMismatchOptions := agent.DefaultEngineOptions()
	workspaceMismatchOptions.SnapshotCompatibilityIdentity = testSnapshotCompatibility
	workspaceMismatchOptions.WorkspaceFingerprint = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	workspaceMismatchEngine, err := agent.NewEngineWithToolPlanSource(
		blockingProvider{}, workspaceMismatchSource, &agent.AtomicIDSource{}, time.Now, nil, nil, workspaceMismatchOptions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workspaceMismatchEngine.ResumeSnapshot(t.Context(), snapshot); err == nil ||
		!strings.Contains(err.Error(), "compiled compatibility") || workspaceMismatchSource.generationCallCount() != 0 {
		t.Fatalf("cross-workspace resume = %v, generation calls=%d", err, workspaceMismatchSource.generationCallCount())
	}
	compiledMismatchSource := newFakePlanSource(
		oldID, map[stage.PlanID]fakePlanRecord{oldID: {dispatcher: oldDispatcher}},
	)
	compiledMismatchOptions := agent.DefaultEngineOptions()
	compiledMismatchOptions.SnapshotCompatibilityIdentity = testSnapshotCompatibility
	compiledMismatchOptions.WorkspaceFingerprint = testWorkspaceFingerprint
	compiledMismatchOptions.CompiledPlanIdentities = append(
		compiledMismatchOptions.CompiledPlanIdentities, "tool:changed-implementation",
	)
	compiledMismatchEngine, err := agent.NewEngineWithToolPlanSource(
		blockingProvider{}, compiledMismatchSource, &agent.AtomicIDSource{}, time.Now,
		nil, nil, compiledMismatchOptions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = compiledMismatchEngine.ResumeSnapshot(t.Context(), snapshot); err == nil ||
		!strings.Contains(err.Error(), "compiled compatibility") ||
		compiledMismatchSource.generationCallCount() != 0 {
		t.Fatalf(
			"compiled mismatch resume = %v, generation calls=%d",
			err, compiledMismatchSource.generationCallCount(),
		)
	}
	source := newFakePlanSource(newID, map[stage.PlanID]fakePlanRecord{
		oldID: {dispatcher: oldDispatcher},
		newID: {dispatcher: newDispatcher},
	})
	engine := newEngineWithPlanSource(
		t,
		&scriptedProvider{scripts: [][]model.StreamEvent{{delta(t, "resumed"), completed(t)}}},
		source,
		&agent.AtomicIDSource{},
		nil,
	)
	run, err := engine.ResumeSnapshot(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if run.ToolPlanID() != oldID {
		t.Fatalf("resumed tool plan = %q", run.ToolPlanID())
	}
	_ = collectAfter(t, run, snapshot.LastSequence())
	if err = run.Wait(t.Context()); err != nil || source.releaseCount(oldID) != 1 {
		t.Fatalf("resumed run/release = %v/%d", err, source.releaseCount(oldID))
	}

	missing := newFakePlanSource(newID, map[stage.PlanID]fakePlanRecord{newID: {dispatcher: newDispatcher}})
	missingEngine := newEngineWithPlanSource(t, blockingProvider{}, missing, &agent.AtomicIDSource{}, nil)
	if _, err = missingEngine.ResumeSnapshot(t.Context(), snapshot); err == nil || missing.releaseCount(newID) != 0 {
		t.Fatalf("missing exact generation = %v, releases=%d", err, missing.releaseCount(newID))
	}

	changed := newFakePlanSource(newID, map[stage.PlanID]fakePlanRecord{
		oldID: {dispatcher: newDispatcher},
		newID: {dispatcher: newDispatcher},
	})
	changedEngine := newEngineWithPlanSource(t, blockingProvider{}, changed, &agent.AtomicIDSource{}, nil)
	if _, err = changedEngine.ResumeSnapshot(t.Context(), snapshot); err == nil ||
		!strings.Contains(err.Error(), "plan identity") || changed.releaseCount(oldID) != 1 {
		t.Fatalf("changed exact generation = %v, releases=%d", err, changed.releaseCount(oldID))
	}

	wrong := newFakePlanSource(newID, map[stage.PlanID]fakePlanRecord{
		oldID: {dispatcher: oldDispatcher},
		newID: {dispatcher: newDispatcher},
	})
	wrong.generationAs = newID
	wrongEngine := newEngineWithPlanSource(t, blockingProvider{}, wrong, &agent.AtomicIDSource{}, nil)
	if _, err = wrongEngine.ResumeSnapshot(t.Context(), snapshot); err == nil ||
		!strings.Contains(err.Error(), "requested") || wrong.releaseCount(newID) != 1 {
		t.Fatalf("substituted generation = %v, releases=%d", err, wrong.releaseCount(newID))
	}
}

func TestPlanIdentityCoversCompatibilityAndAllExecutableBeanCategories(t *testing.T) {
	id, _ := stage.NewPlanID("generation:identity")
	compiled := []string{
		"broker:b", "decorator:policy", "observer:o", "provider:p", "stage:s", "tool:read",
	}
	first, err := agent.NewPlanIdentity(compiled, "application:v1", testWorkspaceFingerprint, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := agent.NewPlanIdentity(compiled, "application:v2", testWorkspaceFingerprint, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotCompatibilityIdentity() != "application:v1" ||
		first.WorkspaceFingerprint() != testWorkspaceFingerprint ||
		first.Fingerprint() == second.Fingerprint() {
		t.Fatalf("compatibility fingerprint = %q/%q", first.Fingerprint(), second.Fingerprint())
	}
	otherWorkspace, err := agent.NewPlanIdentity(
		compiled, "application:v1", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", id, nil,
	)
	if err != nil || otherWorkspace.Fingerprint() == first.Fingerprint() {
		t.Fatalf("workspace identity fingerprint = %q/%q, %v", first.Fingerprint(), otherWorkspace.Fingerprint(), err)
	}
}

type commitAndCancelTool struct{ cancel context.CancelFunc }

func (implementation commitAndCancelTool) Definition() tool.Definition {
	definition, _ := tool.NewDefinition("write", "Commit a fixture.", json.RawMessage(`{}`), tool.EffectMutating, tool.ReplayUnsafe)
	return definition
}

func (implementation commitAndCancelTool) Execute(_ context.Context, call tool.Call, _ tool.Reporter) (tool.Result, error) {
	implementation.cancel()
	result, _ := tool.NewResult(call.ID(), json.RawMessage(`{"committed":true}`))
	return result, nil
}

func TestCommittedToolResultWinsConcurrentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	call, _ := tool.NewCall("write-1", "write", json.RawMessage(`{}`))
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{{toolEvent(t, call), completed(t)}}}
	dispatcher, _ := stage.NewDispatcher(map[string]tool.Tool{"write": commitAndCancelTool{cancel: cancel}})
	engine, err := agent.NewEngine(provider, dispatcher, &agent.AtomicIDSource{}, time.Now, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := agent.NewDefinition("test", "model", 2)
	input, _ := agent.NewInput(inputMessage(t))
	run, err := engine.Start(ctx, definition, input)
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, run)
	if waitErr := run.Wait(t.Context()); !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("cancelled committed run = %v", waitErr)
	}
	counts := countKinds(events)
	if counts[event.ToolCompleted] != 1 || counts[event.ToolFailed] != 0 || counts[event.RunCancelled] != 1 || len(provider.requests) != 1 {
		t.Fatalf("terminal counts/requests = %v/%d", counts, len(provider.requests))
	}
	if payload := eventData(t, events, event.ToolCompleted); !strings.Contains(payload, `"call_id":"write-1"`) || !strings.Contains(payload, `"name":"write"`) {
		t.Fatalf("committed tool terminal correlation = %s", payload)
	}
	snapshot, err := run.ExportSnapshot()
	if err != nil || snapshot.Status() != agent.LifecycleCancelled {
		t.Fatalf("cancelled snapshot = %s, %v", snapshot.Status(), err)
	}
}

func newEngineWithPlanSource(
	t *testing.T,
	provider model.Provider,
	source stage.ToolPlanSource,
	ids agent.IDSource,
	observers []event.Observer,
) *agent.Engine {
	t.Helper()
	options := agent.DefaultEngineOptions()
	options.SnapshotCompatibilityIdentity = testSnapshotCompatibility
	options.WorkspaceFingerprint = testWorkspaceFingerprint
	engine, err := agent.NewEngineWithToolPlanSource(
		provider,
		source,
		ids,
		func() time.Time { return time.Unix(1, 0).UTC() },
		observers,
		nil,
		options,
	)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

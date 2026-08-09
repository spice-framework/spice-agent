package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

func testToolDispatchScope(t *testing.T) stage.ToolDispatchScope {
	t.Helper()
	return testToolDispatchScopeForPlan(t, stage.PlanID("generation:test"))
}

func testToolDispatchScopeForPlan(t *testing.T, planID stage.PlanID) stage.ToolDispatchScope {
	t.Helper()
	authority, err := interaction.NewScope("run-test")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := stage.NewToolDispatchScope(
		"run-test", 1, planID, "sha256:"+strings.Repeat("0", 64), "", authority,
		interaction.UnavailableRequester{},
	)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

type hostTestTool struct {
	definition tool.Definition
	result     string
}

func newHostTestTool(t *testing.T, name, result string) tool.Tool {
	t.Helper()
	definition, err := tool.NewDefinition(
		name, "host test tool", json.RawMessage(`{"type":"object"}`), tool.EffectReadOnly, tool.ReplaySafe,
	)
	if err != nil {
		t.Fatal(err)
	}
	return hostTestTool{definition: definition, result: result}
}

func (implementation hostTestTool) Definition() tool.Definition {
	return implementation.definition.Clone()
}

func (implementation hostTestTool) Execute(
	_ context.Context,
	call tool.Call,
	_ tool.Reporter,
) (tool.Result, error) {
	return tool.NewResult(call.ID(), json.RawMessage(`"`+implementation.result+`"`))
}

type fakeHostStarter struct {
	fn func(context.Context, Executable) (generationCandidate, error)
}

type hostGuardFunc func(context.Context, stage.ToolDispatchScope, tool.Definition, tool.Call, stage.ToolDispatchNext) (tool.Result, error)

func (guard hostGuardFunc) Guard(ctx context.Context, scope stage.ToolDispatchScope, definition tool.Definition, call tool.Call, next stage.ToolDispatchNext) (tool.Result, error) {
	return guard(ctx, scope, definition, call, next)
}

func (starter fakeHostStarter) start(
	ctx context.Context,
	executable Executable,
) (generationCandidate, error) {
	return starter.fn(ctx, executable)
}

type fakeHostCandidate struct {
	mu          sync.Mutex
	identity    []byte
	toolSet     map[string]tool.Tool
	health      error
	doneSignal  chan struct{}
	doneOnce    sync.Once
	closeErrors []error
	closeCalls  int
	closeOrder  *[]string
	closeBlock  <-chan struct{}
	closeStart  chan<- struct{}
	startOnce   sync.Once
	name        string
}

func newFakeHostCandidate(identity string, tools map[string]tool.Tool) *fakeHostCandidate {
	return &fakeHostCandidate{
		identity: []byte(identity), toolSet: tools, doneSignal: make(chan struct{}),
	}
}

func (candidate *fakeHostCandidate) tools() map[string]tool.Tool {
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	result := make(map[string]tool.Tool, len(candidate.toolSet))
	maps.Copy(result, candidate.toolSet)
	return result
}

func (candidate *fakeHostCandidate) done() <-chan struct{} { return candidate.doneSignal }

func (candidate *fakeHostCandidate) healthFailure() error {
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	return candidate.health
}

func (candidate *fakeHostCandidate) close(context.Context) error {
	candidate.mu.Lock()
	candidate.closeCalls++
	if candidate.closeOrder != nil {
		*candidate.closeOrder = append(*candidate.closeOrder, candidate.name)
	}
	block := candidate.closeBlock
	start := candidate.closeStart
	candidate.mu.Unlock()
	if start != nil {
		candidate.startOnce.Do(func() { start <- struct{}{} })
	}
	if block != nil {
		<-block
	}
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	if candidate.closeCalls <= len(candidate.closeErrors) {
		return candidate.closeErrors[candidate.closeCalls-1]
	}
	candidate.doneOnce.Do(func() { close(candidate.doneSignal) })
	return nil
}

func (candidate *fakeHostCandidate) launchIdentity() []byte {
	return slices.Clone(candidate.identity)
}

func (candidate *fakeHostCandidate) crash(err error) {
	candidate.mu.Lock()
	candidate.health = err
	candidate.mu.Unlock()
	candidate.doneOnce.Do(func() { close(candidate.doneSignal) })
}

func (candidate *fakeHostCandidate) closes() int {
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	return candidate.closeCalls
}

func newFakeHost(
	t *testing.T,
	compiled map[string]tool.Tool,
	starter generationStarter,
	decorators ...stage.ToolDispatchDecorator,
) *Host {
	t.Helper()
	dispatcher, err := stage.NewDispatcher(compiled)
	if err != nil {
		t.Fatal(err)
	}
	host, err := newHost(HostConfig{Compiled: dispatcher, Decorators: decorators}, nil, starter)
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func oneExecutableSet(t *testing.T, id string) Set {
	t.Helper()
	set, err := NewSet([]Executable{executableNamed(t, id, "manifest."+id)})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func dispatchHostTool(t *testing.T, lease *stage.ToolPlanLease, name string) string {
	t.Helper()
	dispatcher := lease.Dispatcher()
	if dispatcher == nil {
		t.Error("tool dispatcher is nil")
		return ""
	}
	call, err := tool.NewCall(tool.CallID("call-"+name), name, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.Dispatch(t.Context(), testToolDispatchScopeForPlan(t, lease.ToolPlanID()), call, nil)
	if err != nil {
		t.Fatal(err)
	}
	return string(result.Content())
}

func TestHostStartsCompiledAndAtomicallyActivatesMergedGeneration(t *testing.T) {
	t.Parallel()
	runtimeTool := newHostTestTool(t, "runtime", "runtime")
	candidate := newFakeHostCandidate("launch-one", map[string]tool.Tool{"runtime": runtimeTool})
	host := newFakeHost(t, map[string]tool.Tool{"compiled": newHostTestTool(t, "compiled", "compiled")},
		fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
			return candidate, nil
		}})

	old, err := host.LeaseCurrent(t.Context())
	if err != nil || dispatchHostTool(t, old, "compiled") != `"compiled"` {
		t.Fatalf("compiled lease = %#v, %v", old, err)
	}
	newID, err := host.Activate(t.Context(), oneExecutableSet(t, "one"))
	if err != nil || newID == old.ToolPlanID() {
		t.Fatalf("Activate = %q, %v", newID, err)
	}
	current, err := host.LeaseCurrent(t.Context())
	if err != nil || current.ToolPlanID() != newID ||
		dispatchHostTool(t, current, "compiled") != `"compiled"` ||
		dispatchHostTool(t, current, "runtime") != `"runtime"` {
		t.Fatalf("merged current = %#v, %v", current, err)
	}
	retired, err := host.LeaseGeneration(t.Context(), old.ToolPlanID())
	if err != nil || retired.ToolPlanID() != old.ToolPlanID() {
		t.Fatalf("retired lease = %#v, %v", retired, err)
	}
	_ = retired.Release()
	_ = old.Release()
	if _, err = host.LeaseGeneration(t.Context(), old.ToolPlanID()); err == nil {
		t.Fatal("cleaning retired generation remained leasable")
	}
	_ = current.Release()
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHostAppliesOneTerminalGuardToCompiledAndRuntimeRoutes(t *testing.T) {
	t.Parallel()
	compiled, _ := stage.NewDispatcher(map[string]tool.Tool{
		"compiled": newHostTestTool(t, "compiled", "compiled"),
	})
	runtimeCandidate := newFakeHostCandidate("guarded-runtime", map[string]tool.Tool{
		"runtime": newHostTestTool(t, "runtime", "runtime"),
	})
	var calls atomic.Uint32
	guard := hostGuardFunc(func(ctx context.Context, scope stage.ToolDispatchScope, definition tool.Definition, call tool.Call, next stage.ToolDispatchNext) (tool.Result, error) {
		if scope.ToolPlanID() == "" || definition.Name() != call.Name() {
			return tool.Result{}, errors.New("guard received incomplete dispatch facts")
		}
		calls.Add(1)
		return next()
	})
	host, err := newHost(
		HostConfig{Compiled: compiled, Guards: []stage.ToolDispatchGuard{guard}}, nil,
		fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
			return runtimeCandidate, nil
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	initial, _ := host.LeaseCurrent(t.Context())
	if got := dispatchHostTool(t, initial, "compiled"); got != `"compiled"` {
		t.Fatalf("initial compiled result = %s", got)
	}
	_ = initial.Release()
	if _, err = host.Activate(t.Context(), oneExecutableSet(t, "guarded")); err != nil {
		t.Fatal(err)
	}
	current, _ := host.LeaseCurrent(t.Context())
	if got := dispatchHostTool(t, current, "compiled"); got != `"compiled"` {
		t.Fatalf("merged compiled result = %s", got)
	}
	if got := dispatchHostTool(t, current, "runtime"); got != `"runtime"` {
		t.Fatalf("runtime result = %s", got)
	}
	if calls.Load() != 3 {
		t.Fatalf("terminal guard calls = %d, want 3", calls.Load())
	}
	_ = current.Release()
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHostKeepsStagedSetInvisibleAndCancellationPreservesCurrent(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	first := newFakeHostCandidate("launch-first", map[string]tool.Tool{"first": newHostTestTool(t, "first", "first")})
	second := newFakeHostCandidate("launch-second", map[string]tool.Tool{"second": newHostTestTool(t, "second", "second")})
	starter := fakeHostStarter{fn: func(ctx context.Context, executable Executable) (generationCandidate, error) {
		if executable.ID() == "first" {
			return first, nil
		}
		close(started)
		<-ctx.Done()
		return second, ctx.Err()
	}}
	host := newFakeHost(t, map[string]tool.Tool{"compiled": newHostTestTool(t, "compiled", "compiled")}, starter)
	initial, _ := host.LeaseCurrent(t.Context())
	_ = initial.Release()

	firstExecutable := executableNamed(t, "first", "manifest.first")
	secondExecutable := executableNamed(t, "second", "manifest.second")
	set, _ := NewSet([]Executable{firstExecutable, secondExecutable})
	ctx, cancel := context.WithCancel(t.Context())
	activation := make(chan error, 1)
	go func() {
		_, err := host.Activate(ctx, set)
		activation <- err
	}()
	<-started
	during, err := host.LeaseCurrent(t.Context())
	if err != nil || during.ToolPlanID() != initial.ToolPlanID() {
		t.Fatalf("current changed during staging: %#v, %v", during, err)
	}
	_ = during.Release()
	cancel()
	if err = <-activation; !errors.Is(err, context.Canceled) && err == nil {
		t.Fatalf("canceled activation = %v", err)
	}
	after, err := host.LeaseCurrent(t.Context())
	if err != nil || after.ToolPlanID() != initial.ToolPlanID() {
		t.Fatalf("current changed after cancellation: %#v, %v", after, err)
	}
	_ = after.Release()
	if first.closes() == 0 || second.closes() == 0 {
		t.Fatalf("aborted candidates were not cleaned: %d, %d", first.closes(), second.closes())
	}
	_ = host.Close(t.Context())
}

func TestHostRejectsRuntimeCollisionsWithoutChangingCurrent(t *testing.T) {
	t.Parallel()
	for name, candidates := range map[string][]*fakeHostCandidate{
		"compiled": {
			newFakeHostCandidate("one", map[string]tool.Tool{"same": newHostTestTool(t, "same", "runtime")}),
		},
		"runtime": {
			newFakeHostCandidate("one", map[string]tool.Tool{"duplicate": newHostTestTool(t, "duplicate", "one")}),
			newFakeHostCandidate("two", map[string]tool.Tool{"duplicate": newHostTestTool(t, "duplicate", "two")}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			index := 0
			starter := fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
				candidate := candidates[index]
				index++
				return candidate, nil
			}}
			compiled := map[string]tool.Tool{}
			if name == "compiled" {
				compiled["same"] = newHostTestTool(t, "same", "compiled")
			}
			host := newFakeHost(t, compiled, starter)
			before, _ := host.LeaseCurrent(t.Context())
			_ = before.Release()
			executables := []Executable{executableNamed(t, "one", "manifest.one")}
			if len(candidates) == 2 {
				executables = append(executables, executableNamed(t, "two", "manifest.two"))
			}
			set, _ := NewSet(executables)
			if _, err := host.Activate(t.Context(), set); err == nil {
				t.Fatal("collision activation succeeded")
			}
			after, err := host.LeaseCurrent(t.Context())
			if err != nil || after.ToolPlanID() != before.ToolPlanID() {
				t.Fatalf("collision changed current: %#v, %v", after, err)
			}
			_ = after.Release()
			for _, candidate := range candidates {
				if candidate.closes() == 0 {
					t.Fatal("rejected candidate was not closed")
				}
			}
			_ = host.Close(t.Context())
		})
	}
}

func TestHostCurrentCrashFailsClosedButExistingLeaseRemainsStable(t *testing.T) {
	t.Parallel()
	candidate := newFakeHostCandidate("launch-crash", map[string]tool.Tool{
		"runtime": newHostTestTool(t, "runtime", "stable"),
	})
	host := newFakeHost(t, nil, fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
		return candidate, nil
	}})
	id, err := host.Activate(t.Context(), oneExecutableSet(t, "crash"))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := host.LeaseCurrent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	candidate.crash(errors.New("plugin crashed"))
	eventually(t, func() bool {
		_, leaseErr := host.LeaseCurrent(t.Context())
		return leaseErr != nil
	})
	if _, err = host.LeaseGeneration(t.Context(), id); err == nil {
		t.Fatal("unhealthy exact generation was leased")
	}
	if got := dispatchHostTool(t, lease, "runtime"); got != `"stable"` {
		t.Fatalf("existing lease changed after crash: %s", got)
	}
	_ = lease.Release()
	_ = host.Close(t.Context())
}

func TestHostCloseWaitsForLeasesRetriesOwnershipAndCleansInReverse(t *testing.T) {
	t.Parallel()
	var order []string
	first := newFakeHostCandidate("launch-first", map[string]tool.Tool{"first": newHostTestTool(t, "first", "first")})
	first.name, first.closeOrder = "first", &order
	second := newFakeHostCandidate("launch-second", map[string]tool.Tool{"second": newHostTestTool(t, "second", "second")})
	second.name, second.closeOrder = "second", &order
	second.closeErrors = []error{
		errors.New("containment failed"), errors.New("containment failed"),
		errors.New("containment failed"), errors.New("containment failed"),
	}
	index := 0
	host := newFakeHost(t, nil, fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
		values := []generationCandidate{first, second}
		result := values[index]
		index++
		return result, nil
	}})
	set, _ := NewSet([]Executable{
		executableNamed(t, "first", "manifest.first"), executableNamed(t, "second", "manifest.second"),
	})
	if _, err := host.Activate(t.Context(), set); err != nil {
		t.Fatal(err)
	}
	lease, _ := host.LeaseCurrent(t.Context())
	closeResult := make(chan error, 1)
	go func() { closeResult <- host.Close(t.Context()) }()
	select {
	case err := <-closeResult:
		t.Fatalf("Close returned with active lease: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	_ = lease.Release()
	if err := <-closeResult; err == nil {
		t.Fatal("first Close did not preserve failed cleanup")
	}
	if len(order) < 2 || order[0] != "second" || order[1] != "second" {
		t.Fatalf("first cleanup order = %v", order)
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatalf("retry Close = %v", err)
	}
	if order[len(order)-2] != "second" || order[len(order)-1] != "first" {
		t.Fatalf("successful reverse cleanup order = %v", order)
	}
	if _, err := host.LeaseCurrent(t.Context()); err == nil {
		t.Fatal("closed host admitted a lease")
	}
}

func TestHostPlanIDsAreUniqueForRepeatedEquivalentActivations(t *testing.T) {
	t.Parallel()
	host := newFakeHost(t, nil, fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
		t.Fatal("empty set started a candidate")
		return nil, errors.New("unexpected candidate start")
	}})
	empty, _ := NewSet(nil)
	first, err := host.Activate(t.Context(), empty)
	if err != nil {
		t.Fatal(err)
	}
	second, err := host.Activate(t.Context(), empty)
	if err != nil || first == second {
		t.Fatalf("repeated IDs = %q, %q, %v", first, second, err)
	}
	_ = host.Close(t.Context())
}

type countingHostDecorator struct {
	mu    sync.Mutex
	wraps int
}

type nilHostDecorator struct{}

func (nilHostDecorator) Wrap(stage.ToolDispatcher) stage.ToolDispatcher { return nil }

func (decorator *countingHostDecorator) Wrap(next stage.ToolDispatcher) stage.ToolDispatcher {
	decorator.mu.Lock()
	decorator.wraps++
	decorator.mu.Unlock()
	return next
}

func (decorator *countingHostDecorator) count() int {
	decorator.mu.Lock()
	defer decorator.mu.Unlock()
	return decorator.wraps
}

func TestHostAppliesDecoratorsAfterEachCompleteMerge(t *testing.T) {
	t.Parallel()
	decorator := &countingHostDecorator{}
	candidate := newFakeHostCandidate("launch", map[string]tool.Tool{
		"runtime": newHostTestTool(t, "runtime", "runtime"),
	})
	host := newFakeHost(t, map[string]tool.Tool{"compiled": newHostTestTool(t, "compiled", "compiled")},
		fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
			return candidate, nil
		}}, decorator)
	if decorator.count() != 1 {
		t.Fatalf("initial decorator wraps = %d", decorator.count())
	}
	if _, err := host.Activate(t.Context(), oneExecutableSet(t, "decorated")); err != nil {
		t.Fatal(err)
	}
	if decorator.count() != 2 {
		t.Fatalf("activated decorator wraps = %d", decorator.count())
	}
	_ = host.Close(t.Context())
}

func TestHostLastReleaseSchedulesCleanupWithoutBlocking(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	candidate := newFakeHostCandidate("launch", map[string]tool.Tool{
		"runtime": newHostTestTool(t, "runtime", "runtime"),
	})
	candidate.closeBlock, candidate.closeStart = block, started
	host := newFakeHost(t, nil, fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
		return candidate, nil
	}})
	if _, err := host.Activate(t.Context(), oneExecutableSet(t, "blocked")); err != nil {
		t.Fatal(err)
	}
	lease, _ := host.LeaseCurrent(t.Context())
	empty, _ := NewSet(nil)
	if _, err := host.Activate(t.Context(), empty); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() { released <- lease.Release() }()
	select {
	case err := <-released:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("last release blocked on candidate cleanup")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("last release did not schedule cleanup")
	}
	close(block)
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHostCloseCancelsAndJoinsActiveStaging(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	candidate := newFakeHostCandidate("launch", map[string]tool.Tool{
		"runtime": newHostTestTool(t, "runtime", "runtime"),
	})
	host := newFakeHost(t, nil, fakeHostStarter{fn: func(ctx context.Context, _ Executable) (generationCandidate, error) {
		close(started)
		<-ctx.Done()
		return candidate, ctx.Err()
	}})
	activation := make(chan error, 1)
	go func() {
		_, err := host.Activate(context.Background(), oneExecutableSet(t, "closing"))
		activation <- err
	}()
	<-started
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-activation; err == nil {
		t.Fatal("Close did not cancel activation")
	}
	if candidate.closes() == 0 {
		t.Fatal("Close did not join staged candidate cleanup")
	}
}

func TestHostBoundsRetainedGenerationsBeforeStaging(t *testing.T) {
	t.Parallel()
	host := newFakeHost(t, nil, fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
		t.Fatal("retention rejection started a candidate")
		return nil, errors.New("unexpected candidate start")
	}})
	host.mu.Lock()
	dummies := make([]*hostGeneration, maximumRetainedGenerations-1)
	for index := range dummies {
		dummies[index] = &hostGeneration{}
		host.owned[dummies[index]] = struct{}{}
	}
	host.mu.Unlock()
	empty, _ := NewSet(nil)
	if _, err := host.Activate(t.Context(), empty); err == nil {
		t.Fatal("retained-generation bound admitted activation")
	}
	host.mu.Lock()
	for _, generation := range dummies {
		delete(host.owned, generation)
	}
	host.mu.Unlock()
	_ = host.Close(t.Context())
}

func TestHostEpochPreventsPlanIDReuseAcrossProcesses(t *testing.T) {
	t.Parallel()
	dispatcher, err := stage.NewDispatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	starter := fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
		return nil, errors.New("not used")
	}}
	first, err := newHost(HostConfig{Compiled: dispatcher}, bytes.NewReader(bytes.Repeat([]byte{1}, hostEpochBytes)), starter)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newHost(HostConfig{Compiled: dispatcher}, bytes.NewReader(bytes.Repeat([]byte{2}, hostEpochBytes)), starter)
	if err != nil {
		t.Fatal(err)
	}
	firstLease, _ := first.LeaseCurrent(t.Context())
	secondLease, _ := second.LeaseCurrent(t.Context())
	if firstLease.ToolPlanID() == secondLease.ToolPlanID() {
		t.Fatalf("separate hosts reused plan ID %q", firstLease.ToolPlanID())
	}
	_ = firstLease.Release()
	_ = secondLease.Release()
	_ = first.Close(t.Context())
	_ = second.Close(t.Context())
}

func TestHostCanceledCloseDoesNotChangeAdmissionAndConcurrentCloseIsSerialized(t *testing.T) {
	t.Parallel()
	host := newFakeHost(t, nil, fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
		return nil, errors.New("not used")
	}})
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := host.Close(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Close = %v", err)
	}
	lease, err := host.LeaseCurrent(t.Context())
	if err != nil {
		t.Fatalf("pre-canceled Close changed admission: %v", err)
	}
	_ = lease.Release()

	results := make(chan error, 2)
	go func() { results <- host.Close(t.Context()) }()
	go func() { results <- host.Close(t.Context()) }()
	for range 2 {
		if err = <-results; err != nil {
			t.Fatalf("concurrent Close = %v", err)
		}
	}
}

func TestHostRejectsInvalidConstructionInputs(t *testing.T) {
	t.Parallel()
	empty, err := stage.NewDispatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	starter := fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
		return nil, errors.New("not used")
	}}
	nonzeroEpoch := bytes.NewReader(bytes.Repeat([]byte{1}, hostEpochBytes))

	if host, hostErr := NewHost(HostConfig{}); host != nil || hostErr == nil {
		t.Fatal("production constructor accepted a missing compiled dispatcher")
	}
	if host, hostErr := newHost(
		HostConfig{Compiled: empty, Decorators: []stage.ToolDispatchDecorator{nil}},
		nonzeroEpoch,
		starter,
	); host != nil || hostErr == nil {
		t.Fatal("host accepted a nil decorator")
	}
	if host, hostErr := newHost(HostConfig{Compiled: empty}, bytes.NewReader(nil), starter); host != nil || hostErr == nil {
		t.Fatal("host accepted an incomplete cryptographic epoch")
	}
	if host, hostErr := newHost(
		HostConfig{Compiled: empty},
		bytes.NewReader(make([]byte, hostEpochBytes)),
		starter,
	); host != nil || hostErr == nil {
		t.Fatal("host accepted an all-zero cryptographic epoch")
	}
	if host, hostErr := newHost(
		HostConfig{Compiled: empty, Decorators: []stage.ToolDispatchDecorator{nilHostDecorator{}}},
		bytes.NewReader(bytes.Repeat([]byte{1}, hostEpochBytes)),
		starter,
	); host != nil || hostErr == nil {
		t.Fatal("host accepted a decorator that removed the dispatcher")
	}
	if host, hostErr := NewHost(HostConfig{Compiled: empty}); host != nil || hostErr == nil {
		t.Fatal("production constructor accepted missing process and endpoint dependencies")
	}
}

func TestHostRejectsInvalidActivationAndLeaseInputs(t *testing.T) {
	t.Parallel()
	const privateFailure = "private starter failure"
	host := newFakeHost(t, nil, fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
		return nil, errors.New(privateFailure)
	}})

	if _, err := (*Host)(nil).Activate(t.Context(), Set{}); err == nil {
		t.Fatal("nil host activation succeeded")
	}
	//nolint:staticcheck // Boundary coverage intentionally proves nil contexts fail closed.
	if _, err := host.Activate(nil, Set{}); err == nil {
		t.Fatal("activation without a context succeeded")
	}
	if _, err := host.Activate(t.Context(), Set{executables: []Executable{{}}}); err == nil {
		t.Fatal("activation accepted a corrupt plugin set")
	}
	if _, err := host.Activate(t.Context(), oneExecutableSet(t, "rejected")); err == nil ||
		strings.Contains(err.Error(), privateFailure) {
		t.Fatalf("starter rejection was absent or leaked plugin detail: %v", err)
	}
	if _, err := (*Host)(nil).LeaseCurrent(t.Context()); err == nil {
		t.Fatal("nil host lease succeeded")
	}
	//nolint:staticcheck // Boundary coverage intentionally proves nil contexts fail closed.
	if _, err := host.LeaseCurrent(nil); err == nil {
		t.Fatal("lease without a context succeeded")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := host.LeaseCurrent(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled lease = %v", err)
	}
	if _, err := host.LeaseGeneration(t.Context(), "not-a-plan-id"); err == nil {
		t.Fatal("invalid generation identity was accepted")
	}
	if _, err := host.LeaseGeneration(t.Context(), stage.PlanID("runtime:00000000000000000099:missing")); err == nil {
		t.Fatal("unknown generation identity was accepted")
	}
	if err := (*Host)(nil).Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	//nolint:staticcheck // Boundary coverage intentionally proves nil contexts fail closed.
	if err := host.Close(nil); err == nil {
		t.Fatal("host close without a context succeeded")
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHostRejectsMissingCandidateIdentityAndCleansCandidate(t *testing.T) {
	t.Parallel()
	candidate := newFakeHostCandidate("", map[string]tool.Tool{
		"runtime": newHostTestTool(t, "runtime", "runtime"),
	})
	host := newFakeHost(t, nil, fakeHostStarter{fn: func(context.Context, Executable) (generationCandidate, error) {
		return candidate, nil
	}})
	before, err := host.LeaseCurrent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	beforeID := before.ToolPlanID()
	_ = before.Release()

	if _, err = host.Activate(t.Context(), oneExecutableSet(t, "missing-identity")); err == nil {
		t.Fatal("candidate without an authenticated launch identity was activated")
	}
	after, err := host.LeaseCurrent(t.Context())
	if err != nil || after.ToolPlanID() != beforeID {
		t.Fatalf("failed activation changed current generation: %#v, %v", after, err)
	}
	_ = after.Release()
	if candidate.closes() == 0 {
		t.Fatal("candidate rejected before publication was not contained")
	}
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationPlanAndMergedDispatcherRejectInvalidState(t *testing.T) {
	t.Parallel()
	empty, err := stage.NewDispatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = generationPlanID(make([]byte, hostEpochBytes-1), 1, nil, nil); err == nil {
		t.Fatal("short host epoch produced a generation identity")
	}
	missingIdentity := newFakeHostCandidate("", nil)
	if _, err = generationPlanID(bytes.Repeat([]byte{1}, hostEpochBytes), 1, []generationCandidate{missingIdentity}, nil); err == nil {
		t.Fatal("missing launch identity produced a generation identity")
	}
	if _, err = generationPlanID(bytes.Repeat([]byte{1}, hostEpochBytes), 1, nil, []tool.Definition{{}}); err == nil {
		t.Fatal("invalid tool definition produced a generation identity")
	}
	if merged, mergeErr := newMergedDispatcher(nil, empty); merged != nil || mergeErr == nil {
		t.Fatal("merged dispatcher accepted a missing compiled dispatcher")
	}
	compiled, err := stage.NewDispatcher(map[string]tool.Tool{
		"compiled": newHostTestTool(t, "compiled", "compiled"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if merged, mergeErr := newMergedDispatcher(compiled, compiled); merged != nil || mergeErr == nil {
		t.Fatal("merged dispatcher accepted colliding definitions")
	}
	runtime, err := stage.NewDispatcher(map[string]tool.Tool{
		"runtime": newHostTestTool(t, "runtime", "runtime"),
	})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := newMergedDispatcher(compiled, runtime)
	if err != nil {
		t.Fatal(err)
	}
	definition, found := merged.Definition("runtime")
	if !found || definition.Name() != "runtime" {
		t.Fatalf("runtime definition = %#v, %t", definition, found)
	}
	if _, found = merged.Definition("absent"); found {
		t.Fatal("merged dispatcher reported an absent definition")
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

package planning

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

const testWorkspace = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type echoTool struct {
	definition tool.Definition
	executions atomic.Int64
}

func newEchoTool(t *testing.T, name string) *echoTool {
	t.Helper()
	definition, err := tool.NewDefinition(
		name, "Return one bounded fixture result.", json.RawMessage(`{"type":"object"}`),
		tool.EffectMutating, tool.ReplayIdempotent, tool.CapabilityFilesystemWrite,
	)
	if err != nil {
		t.Fatal(err)
	}
	return &echoTool{definition: definition}
}

func (implementation *echoTool) Definition() tool.Definition {
	return implementation.definition.Clone()
}

func (implementation *echoTool) Execute(_ context.Context, call tool.Call, _ tool.Reporter) (tool.Result, error) {
	implementation.executions.Add(1)
	return tool.NewResult(call.ID(), json.RawMessage(`{"ok":true}`))
}

type suspendingProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int64
}

func (provider *suspendingProvider) Stream(ctx context.Context, _ model.Request) (model.Stream, error) {
	callNumber := provider.calls.Add(1)
	completed, _ := model.Completed(model.NewUsage(1, 1))
	if callNumber == 1 {
		provider.once.Do(func() { close(provider.started) })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-provider.release:
		}
		call, _ := tool.NewCall("call-1", "fixture.echo", json.RawMessage(`{}`))
		callEvent, _ := model.ToolCallEvent(call)
		return &testStream{values: []model.StreamEvent{callEvent, completed}}, nil
	}
	text, _ := model.TextDelta("resumed worker complete")
	return &testStream{values: []model.StreamEvent{text, completed}}, nil
}

type observedDoneContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
}

func (ctx *observedDoneContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.entered) })
	return ctx.Context.Done()
}

func TestSuspendedSnapshotExtractResumeAndSemanticMismatch(t *testing.T) {
	planner := &testPlanner{identity: "snapshot-planner:v1"}
	semantic, err := SemanticIdentity("planning-proof:v1", planner.identity)
	if err != nil {
		t.Fatal(err)
	}
	implementation := newEchoTool(t, "fixture.echo")
	dispatcher, err := stage.NewDispatcher(map[string]tool.Tool{"fixture.echo": implementation})
	if err != nil {
		t.Fatal(err)
	}
	provider := &suspendingProvider{started: make(chan struct{}), release: make(chan struct{})}
	engine := portableEngine(t, provider, dispatcher, semantic)
	service, err := NewService(planner, engine)
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := agent.NewDefinition("worker", "scripted", 2)
	_, initial := testInput(t)
	prepared, err := service.Prepare(t.Context(), definition, initial)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartPrepared(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not reach first turn")
	}
	suspendContext, cancelSuspend := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelSuspend()
	observed := &observedDoneContext{Context: suspendContext, entered: make(chan struct{})}
	suspended := make(chan error, 1)
	go func() { suspended <- run.Suspend(observed) }()
	select {
	case <-observed.entered:
		close(provider.release)
	case <-time.After(2 * time.Second):
		t.Fatal("suspend did not reserve the safe boundary")
	}
	if err = <-suspended; err != nil {
		t.Fatal(err)
	}
	snapshot, err := run.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	extracted, found, err := Extract(snapshot)
	if err != nil || !found || !bytesEqual(extracted.CanonicalJSON(), prepared.Plan().CanonicalJSON()) {
		t.Fatalf("suspended snapshot extraction found=%t err=%v", found, err)
	}

	mismatchIdentity, _ := SemanticIdentity("planning-proof:v1", "snapshot-planner:v2")
	mismatchProvider := &terminalProvider{}
	mismatchEngine := portableEngine(t, mismatchProvider, dispatcher, mismatchIdentity)
	if _, err = mismatchEngine.ResumeSnapshot(t.Context(), snapshot); err == nil {
		t.Fatal("semantic identity mismatch resumed")
	}
	if mismatchProvider.calls.Load() != 0 {
		t.Fatal("semantic identity mismatch reached provider")
	}

	resumeProvider := &suspendingProvider{started: make(chan struct{}), release: make(chan struct{})}
	resumeProvider.calls.Store(1)
	resumeEngine := portableEngine(t, resumeProvider, dispatcher, semantic)
	resumed, err := resumeEngine.ResumeSnapshot(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = resumed.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	terminal, err := resumed.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	terminalPlan, found, err := Extract(terminal)
	if err != nil || !found || !bytesEqual(terminalPlan.CanonicalJSON(), extracted.CanonicalJSON()) {
		t.Fatalf("resumed snapshot changed plan found=%t err=%v", found, err)
	}
	if planner.calls.Load() != 1 || resumeProvider.calls.Load() != 2 {
		t.Fatalf("resume reran planner or skipped provider: planner=%d provider=%d", planner.calls.Load(), resumeProvider.calls.Load())
	}
	shutdownEngine(t, resumeEngine)
	shutdownEngine(t, mismatchEngine)
	shutdownEngine(t, engine)
}

type callProvider struct{ calls atomic.Int64 }

func (provider *callProvider) Stream(ctx context.Context, _ model.Request) (model.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	provider.calls.Add(1)
	call, _ := tool.NewCall("denied-call", "danger.run", json.RawMessage(`{}`))
	callEvent, _ := model.ToolCallEvent(call)
	completed, _ := model.Completed(model.NewUsage(1, 1))
	return &testStream{values: []model.StreamEvent{callEvent, completed}}, nil
}

type denyGuard struct{ calls atomic.Int64 }

func (guard *denyGuard) Guard(
	context.Context,
	stage.ToolDispatchScope,
	tool.Definition,
	tool.Call,
	stage.ToolDispatchNext,
) (tool.Result, error) {
	guard.calls.Add(1)
	return tool.Result{}, errors.New("application policy denied the call")
}

func TestAdvisoryPlanNeverBypassesTerminalToolGuard(t *testing.T) {
	implementation := newEchoTool(t, "danger.run")
	base, err := stage.NewDispatcher(map[string]tool.Tool{"danger.run": implementation})
	if err != nil {
		t.Fatal(err)
	}
	guard := &denyGuard{}
	guarded, err := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guard}, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := guarded.Definitions()
	provider := &callProvider{}
	recorder := &event.Recorder{}
	engine, err := agent.NewEngine(provider, guarded, &agent.AtomicIDSource{}, time.Now, []event.Observer{recorder}, nil)
	if err != nil {
		t.Fatal(err)
	}
	planner := &testPlanner{identity: "authority-proof:v1", process: func(context.Context, Request) (Draft, error) {
		step, _ := NewStep("invoke", "Invoke danger.run even if the plan recommends it.")
		return NewDraft("Recommend one unavailable authority.", step)
	}}
	service, err := NewService(planner, engine)
	if err != nil {
		t.Fatal(err)
	}
	definition, initial := testInput(t)
	prepared, err := service.Prepare(t.Context(), definition, initial)
	if err != nil {
		t.Fatal(err)
	}
	after := guarded.Definitions()
	if len(before) != 1 || len(after) != 1 || before[0].Fingerprint() != after[0].Fingerprint() || guard.calls.Load() != 0 {
		t.Fatal("planning changed dispatcher authority before worker start")
	}
	run, err := service.StartPrepared(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if err = run.Wait(t.Context()); err == nil {
		t.Fatal("guard-denied worker unexpectedly succeeded")
	}
	if guard.calls.Load() != 1 || implementation.executions.Load() != 0 || provider.calls.Load() != 1 {
		t.Fatalf("authority boundary guard=%d tool=%d provider=%d", guard.calls.Load(), implementation.executions.Load(), provider.calls.Load())
	}
	if events := recorder.Events(); countKind(events, event.ToolStarted) != 1 || countKind(events, event.ToolCompleted) != 0 || countKind(events, event.ToolFailed) != 1 || countKind(events, event.RunFailed) != 1 {
		t.Fatalf("guard lifecycle events=%v", eventKinds(events))
	}
	shutdownEngine(t, engine)
}

func portableEngine(t *testing.T, provider model.Provider, dispatcher stage.ToolDispatcher, semantic string) *agent.Engine {
	t.Helper()
	options := agent.DefaultEngineOptions()
	options.SnapshotCompatibilityIdentity = semantic
	options.WorkspaceFingerprint = testWorkspace
	engine, err := agent.NewEngineWithOptions(
		provider, dispatcher, &agent.AtomicIDSource{}, time.Now, nil, nil, options,
	)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func countKind(events []event.Envelope, kind event.Kind) int {
	count := 0
	for _, envelope := range events {
		if envelope.Kind() == kind {
			count++
		}
	}
	return count
}

var (
	_ tool.Tool               = (*echoTool)(nil)
	_ model.Provider          = (*suspendingProvider)(nil)
	_ model.Provider          = (*callProvider)(nil)
	_ stage.ToolDispatchGuard = (*denyGuard)(nil)
	_ io.Closer               = (*testStream)(nil)
)

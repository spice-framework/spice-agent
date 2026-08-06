package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

type scriptedProvider struct {
	mu      sync.Mutex
	scripts [][]model.StreamEvent
	panicAt bool
}

func (provider *scriptedProvider) Stream(_ context.Context, _ model.Request) (model.Stream, error) {
	if provider.panicAt {
		panic("provider boom")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.scripts) == 0 {
		return nil, errors.New("no scripted stream")
	}
	stream := &scriptedStream{events: provider.scripts[0]}
	provider.scripts = provider.scripts[1:]
	return stream, nil
}

type scriptedStream struct {
	events []model.StreamEvent
	index  int
	block  bool
}

func (stream *scriptedStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	if stream.block {
		<-ctx.Done()
		return model.StreamEvent{}, ctx.Err()
	}
	if stream.index == len(stream.events) {
		return model.StreamEvent{}, io.EOF
	}
	value := stream.events[stream.index]
	stream.index++
	return value, nil
}

func (*scriptedStream) Close() error { return nil }

type blockingProvider struct{}

func (blockingProvider) Stream(context.Context, model.Request) (model.Stream, error) {
	return &scriptedStream{block: true}, nil
}

type testTool struct{ panicAt bool }

func (testTool) Definition() tool.Definition {
	definition, _ := tool.NewDefinition("read", "Read a fixture.", json.RawMessage(`{}`), tool.CapabilityFilesystemRead)
	return definition
}

func (implementation testTool) Execute(ctx context.Context, call tool.Call, reporter tool.Reporter) tool.Result {
	if implementation.panicAt {
		panic("tool boom")
	}
	_ = reporter.Report(ctx, tool.Progress{CallID: call.ID, Message: "reading"})
	return tool.Result{CallID: call.ID, Content: json.RawMessage(`{"content":"fixture"}`)}
}

func TestEngineCompletesTextRunDeterministically(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{{
		{Kind: model.EventTextDelta, Text: "hello"},
		{Kind: model.EventCompleted},
	}}}
	engine := newEngine(t, provider, nil, nil, nil)
	run := startRun(t, engine, 2)
	events := collect(run)
	if err := run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []event.Kind{event.RunStarted, event.TurnStarted, event.ModelStarted, event.ModelDelta, event.ModelCompleted, event.TurnCompleted, event.RunCompleted}
	assertKindsAndSequence(t, events, want)
}

func TestEngineExecutesToolThenContinues(t *testing.T) {
	call := tool.Call{ID: "call-1", Name: "read", Arguments: json.RawMessage(`{}`)}
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{
		{{Kind: model.EventToolCall, Call: call}, {Kind: model.EventCompleted}},
		{{Kind: model.EventTextDelta, Text: "done"}, {Kind: model.EventCompleted}},
	}}
	recorder := &event.Recorder{}
	engine := newEngine(t, provider, map[string]tool.Tool{"read": testTool{}}, []event.Observer{recorder}, nil)
	run := startRun(t, engine, 3)
	events := collect(run)
	if err := run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertKindsAndSequence(t, events, nil)
	counts := countKinds(events)
	if counts[event.ToolStarted] != 1 || counts[event.ToolProgress] != 1 || counts[event.ToolCompleted] != 1 || counts[event.TurnCompleted] != 2 {
		t.Fatalf("event counts = %v", counts)
	}
	if len(recorder.Events()) != len(events) {
		t.Fatalf("observer recorded %d of %d", len(recorder.Events()), len(events))
	}
}

func TestEngineCancellationFinalizesActiveOperations(t *testing.T) {
	engine := newEngine(t, blockingProvider{}, nil, nil, nil)
	run := startRun(t, engine, 2)
	var events []event.Envelope
	for envelope := range run.Events() {
		events = append(events, envelope)
		if envelope.Kind() == event.ModelStarted {
			break
		}
	}
	run.Cancel()
	for envelope := range run.Events() {
		events = append(events, envelope)
	}
	if err := run.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v", err)
	}
	counts := countKinds(events)
	if counts[event.ModelFailed] != 1 || counts[event.TurnFailed] != 1 || counts[event.RunCancelled] != 1 {
		t.Fatalf("terminal counts = %v", counts)
	}
}

func TestRunWaitDoesNotDependOnEventConsumption(t *testing.T) {
	script := make([]model.StreamEvent, 0, 302)
	for range 300 {
		script = append(script, model.StreamEvent{Kind: model.EventTextDelta, Text: "x"})
	}
	script = append(script, model.StreamEvent{Kind: model.EventCompleted})
	engine := newEngine(t, &scriptedProvider{scripts: [][]model.StreamEvent{script}}, nil, nil, nil)
	run := startRun(t, engine, 1)
	waitContext, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := run.Wait(waitContext); err != nil {
		t.Fatalf("Wait without draining events: %v", err)
	}
	events := collect(run)
	if len(events) < 300 || events[len(events)-1].Kind() != event.RunCompleted {
		t.Fatalf("events = %d, terminal = %s", len(events), events[len(events)-1].Kind())
	}
}

type failingObserver struct{ kind event.Kind }

func (observer failingObserver) Publish(_ context.Context, envelope event.Envelope) error {
	if envelope.Kind() == observer.kind {
		return errors.New("observer failure")
	}
	return nil
}

func TestRequiredObserverFailureFinalizesEveryStartedOperation(t *testing.T) {
	call := tool.Call{ID: "call", Name: "read", Arguments: json.RawMessage(`{}`)}
	for _, test := range []struct {
		name     string
		failure  event.Kind
		script   []model.StreamEvent
		tools    map[string]tool.Tool
		started  event.Kind
		terminal event.Kind
	}{
		{"turn", event.TurnStarted, nil, nil, event.TurnStarted, event.TurnFailed},
		{"model", event.ModelStarted, nil, nil, event.ModelStarted, event.ModelFailed},
		{"tool", event.ToolStarted, []model.StreamEvent{{Kind: model.EventToolCall, Call: call}, {Kind: model.EventCompleted}}, map[string]tool.Tool{"read": testTool{}}, event.ToolStarted, event.ToolFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := test.script
			if script == nil {
				script = []model.StreamEvent{{Kind: model.EventCompleted}}
			}
			provider := &scriptedProvider{scripts: [][]model.StreamEvent{script}}
			engine := newEngine(t, provider, test.tools, []event.Observer{failingObserver{test.failure}}, nil)
			run := startRun(t, engine, 1)
			events := collect(run)
			if err := run.Wait(t.Context()); err == nil {
				t.Fatal("observer failure did not fail run")
			}
			counts := countKinds(events)
			if counts[test.started] != 1 || counts[test.terminal] != 1 || counts[event.RunFailed] != 1 {
				t.Fatalf("event counts = %v", counts)
			}
		})
	}
}

func TestEngineContainsProviderAndToolPanics(t *testing.T) {
	t.Run("provider", func(t *testing.T) {
		engine := newEngine(t, &scriptedProvider{panicAt: true}, nil, nil, nil)
		run := startRun(t, engine, 1)
		events := collect(run)
		if err := run.Wait(t.Context()); err == nil || !strings.Contains(err.Error(), "provider panic") {
			t.Fatalf("Wait error = %v", err)
		}
		if countKinds(events)[event.RunFailed] != 1 {
			t.Fatal("provider panic did not finalize run")
		}
	})
	t.Run("tool", func(t *testing.T) {
		call := tool.Call{ID: "call", Name: "read", Arguments: json.RawMessage(`{}`)}
		provider := &scriptedProvider{scripts: [][]model.StreamEvent{{{Kind: model.EventToolCall, Call: call}, {Kind: model.EventCompleted}}}}
		engine := newEngine(t, provider, map[string]tool.Tool{"read": testTool{panicAt: true}}, nil, nil)
		run := startRun(t, engine, 1)
		events := collect(run)
		if err := run.Wait(t.Context()); err == nil || !strings.Contains(err.Error(), "tool \"read\" panic") {
			t.Fatalf("Wait error = %v", err)
		}
		counts := countKinds(events)
		if counts[event.ToolFailed] != 1 || counts[event.RunFailed] != 1 {
			t.Fatalf("terminal counts = %v", counts)
		}
	})
}

func TestEngineNormalizesStreamFailuresAndTurnLimit(t *testing.T) {
	call := tool.Call{ID: "call", Name: "read", Arguments: json.RawMessage(`{}`)}
	for _, test := range []struct {
		name   string
		events []model.StreamEvent
		turns  uint32
		tools  map[string]tool.Tool
		part   string
	}{
		{"typed failure", []model.StreamEvent{{Kind: model.EventFailed, Problem: model.Problem{Code: "unavailable", Message: "offline"}}}, 1, nil, "model unavailable"},
		{"premature eof", nil, 1, nil, "before completion"},
		{"turn limit", []model.StreamEvent{{Kind: model.EventToolCall, Call: call}, {Kind: model.EventCompleted}}, 1, map[string]tool.Tool{"read": testTool{}}, "maximum turns"},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &scriptedProvider{scripts: [][]model.StreamEvent{test.events}}
			engine := newEngine(t, provider, test.tools, nil, nil)
			run := startRun(t, engine, test.turns)
			events := collect(run)
			if err := run.Wait(t.Context()); err == nil || !strings.Contains(err.Error(), test.part) {
				t.Fatalf("Wait error = %v", err)
			}
			if countKinds(events)[event.RunFailed] != 1 {
				t.Fatalf("events = %v", countKinds(events))
			}
		})
	}
}

func TestEngineRejectsInvalidConstructionAndRunInputs(t *testing.T) {
	dispatcher, _ := stage.NewDispatcher(nil)
	ids := &agent.AtomicIDSource{}
	clock := time.Now
	provider := &scriptedProvider{}
	if _, err := agent.NewEngine(nil, dispatcher, ids, clock, nil, nil); err == nil {
		t.Fatal("nil provider succeeded")
	}
	if _, err := agent.NewEngine(provider, nil, ids, clock, nil, nil); err == nil {
		t.Fatal("nil dispatcher succeeded")
	}
	if _, err := agent.NewEngine(provider, dispatcher, nil, clock, nil, nil); err == nil {
		t.Fatal("nil IDs succeeded")
	}
	if _, err := agent.NewEngine(provider, dispatcher, ids, nil, nil, nil); err == nil {
		t.Fatal("nil clock succeeded")
	}
	if _, err := agent.NewDefinition("", "model", 1); err == nil {
		t.Fatal("invalid definition succeeded")
	}
	if _, err := agent.NewDefinition("agent", "model", 0); err == nil {
		t.Fatal("zero turns succeeded")
	}
	definitionAccessors, _ := agent.NewDefinition("agent", "model", 3)
	if definitionAccessors.Name() != "agent" || definitionAccessors.Model() != "model" || definitionAccessors.MaxTurns() != 3 {
		t.Fatal("definition accessors mismatch")
	}
	part, _ := message.Text("system")
	id, _ := message.NewID("m")
	system, _ := message.New(id, message.RoleSystem, part)
	if _, err := agent.NewInput(system); err == nil {
		t.Fatal("system input succeeded")
	}
	var nilEngine *agent.Engine
	definition, _ := agent.NewDefinition("agent", "model", 1)
	user := inputMessage(t)
	input, _ := agent.NewInput(user)
	if _, err := nilEngine.Start(t.Context(), definition, input); err == nil {
		t.Fatal("nil engine succeeded")
	}
	var nilIDs *agent.AtomicIDSource
	if _, err := nilIDs.Next("run"); err == nil {
		t.Fatal("nil ID source succeeded")
	}
}

func TestWaitHonorsCallerContextWithoutCancellingRun(t *testing.T) {
	engine := newEngine(t, blockingProvider{}, nil, nil, nil)
	run := startRun(t, engine, 1)
	waitContext, cancelWait := context.WithCancel(t.Context())
	cancelWait()
	if err := run.Wait(waitContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v", err)
	}
	run.Cancel()
	_ = collect(run)
}

func TestBestEffortObserverDropsWithoutBlocking(t *testing.T) {
	mailbox, err := event.NewBestEffortObserver(1)
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{{{Kind: model.EventTextDelta, Text: "x"}, {Kind: model.EventCompleted}}}}
	engine := newEngine(t, provider, nil, nil, []*event.BestEffortObserver{mailbox})
	run := startRun(t, engine, 1)
	_ = collect(run)
	if err := run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if mailbox.Dropped() == 0 {
		t.Fatal("full best-effort observer did not drop")
	}
}

func newEngine(t *testing.T, provider model.Provider, tools map[string]tool.Tool, observers []event.Observer, bestEffort []*event.BestEffortObserver) *agent.Engine {
	t.Helper()
	dispatcher, err := stage.NewDispatcher(tools)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := agent.NewEngine(provider, dispatcher, &agent.AtomicIDSource{}, func() time.Time { return time.Unix(1, 0).UTC() }, observers, bestEffort)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func startRun(t *testing.T, engine *agent.Engine, turns uint32) *agent.Run {
	t.Helper()
	definition, _ := agent.NewDefinition("test", "scripted", turns)
	input, _ := agent.NewInput(inputMessage(t))
	run, err := engine.Start(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	if run.ID() == "" {
		t.Fatal("empty run ID")
	}
	return run
}

func inputMessage(t *testing.T) message.Message {
	t.Helper()
	part, _ := message.Text("hello")
	id, _ := message.NewID("input")
	value, err := message.New(id, message.RoleUser, part)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func collect(run *agent.Run) []event.Envelope {
	var result []event.Envelope
	for envelope := range run.Events() {
		result = append(result, envelope)
	}
	return result
}

func countKinds(events []event.Envelope) map[event.Kind]int {
	result := make(map[event.Kind]int)
	for _, envelope := range events {
		result[envelope.Kind()]++
	}
	return result
}

func assertKindsAndSequence(t *testing.T, events []event.Envelope, want []event.Kind) {
	t.Helper()
	if want != nil && len(events) != len(want) {
		t.Fatalf("event count = %d, want %d", len(events), len(want))
	}
	for index, envelope := range events {
		if envelope.Sequence() != uint64(index+1) {
			t.Fatalf("event %d sequence = %d", index, envelope.Sequence())
		}
		if want != nil && envelope.Kind() != want[index] {
			t.Fatalf("event %d kind = %s, want %s", index, envelope.Kind(), want[index])
		}
	}
}

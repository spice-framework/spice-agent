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
	mu       sync.Mutex
	scripts  [][]model.StreamEvent
	startErr error
	panicAt  bool
	closeErr error
	recvErr  error
	requests []model.Request
}

func (provider *scriptedProvider) Stream(ctx context.Context, request model.Request) (model.Stream, error) {
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	if provider.panicAt {
		panic("provider boom")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.requests = append(provider.requests, request)
	if provider.startErr != nil {
		return nil, provider.startErr
	}
	if len(provider.scripts) == 0 {
		return nil, errors.New("no scripted stream")
	}
	stream := &scriptedStream{events: provider.scripts[0], closeErr: provider.closeErr, recvErr: provider.recvErr}
	provider.scripts = provider.scripts[1:]
	return stream, nil
}

type scriptedStream struct {
	events   []model.StreamEvent
	index    int
	block    bool
	closeErr error
	recvErr  error
}

func (stream *scriptedStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	if stream.block {
		<-ctx.Done()
		return model.StreamEvent{}, ctx.Err()
	}
	if stream.index == len(stream.events) {
		if stream.recvErr != nil {
			return model.StreamEvent{}, stream.recvErr
		}
		return model.StreamEvent{}, io.EOF
	}
	value := stream.events[stream.index]
	stream.index++
	return value, nil
}

func (stream *scriptedStream) Close() error { return stream.closeErr }

type blockingProvider struct{}

func (blockingProvider) Stream(context.Context, model.Request) (model.Stream, error) {
	return &scriptedStream{block: true}, nil
}

type fixedIDSource struct{ value string }

func (source fixedIDSource) Next(string) (string, error) { return source.value, nil }

type testTool struct{ panicAt bool }

func (testTool) Definition() tool.Definition {
	definition, _ := tool.NewDefinition("read", "Read a fixture.", json.RawMessage(`{}`), tool.CapabilityFilesystemRead)
	return definition
}

func (implementation testTool) Execute(ctx context.Context, call tool.Call, reporter tool.Reporter) tool.Result {
	if implementation.panicAt {
		panic("tool boom")
	}
	progress, _ := tool.NewProgress(call.ID(), "reading")
	_ = reporter.Report(ctx, progress)
	result, _ := tool.NewResult(call.ID(), json.RawMessage(`{"content":"fixture"}`))
	return result
}

func TestEngineCompletesTextRunDeterministically(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{{delta(t, "hello"), completed(t)}}}
	engine := newEngine(t, provider, nil, nil, nil)
	run := startRun(t, engine, 2)
	events := collect(t, run)
	if err := run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []event.Kind{event.RunStarted, event.TurnStarted, event.ModelStarted, event.ModelDelta, event.ModelCompleted, event.TurnCompleted, event.RunCompleted}
	assertKindsAndSequence(t, events, want)
	if len(provider.requests) != 1 || provider.requests[0].OperationID() == "" || provider.requests[0].Model() != "scripted" {
		t.Fatal("provider request did not preserve operation or model")
	}
}

func TestEngineExecutesToolThenContinues(t *testing.T) {
	call, _ := tool.NewCall("call-1", "read", json.RawMessage(`{}`))
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{
		{toolEvent(t, call), completed(t)},
		{delta(t, "done"), completed(t)},
	}}
	recorder := &event.Recorder{}
	engine := newEngine(t, provider, map[string]tool.Tool{"read": testTool{}}, []event.Observer{recorder}, nil)
	run := startRun(t, engine, 3)
	events := collect(t, run)
	if err := run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	counts := countKinds(events)
	if counts[event.ToolStarted] != 1 || counts[event.ToolProgress] != 1 || counts[event.ToolCompleted] != 1 || counts[event.TurnCompleted] != 2 {
		t.Fatalf("event counts = %v", counts)
	}
	if len(recorder.Events()) != len(events) {
		t.Fatalf("observer recorded %d of %d", len(recorder.Events()), len(events))
	}
	if got := provider.requests[0].Tools(); len(got) != 1 || got[0].Name() != "read" {
		t.Fatalf("immutable dispatcher definitions = %v", got)
	}
}

func TestEngineCancellationFinalizesActiveOperations(t *testing.T) {
	engine := newEngine(t, blockingProvider{}, nil, nil, nil)
	run := startRun(t, engine, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	subscription, err := run.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	var events []event.Envelope
	for envelope := range subscription.Events() {
		events = append(events, envelope)
		if envelope.Kind() == event.ModelStarted {
			run.Cancel()
		}
	}
	if err = run.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v", err)
	}
	counts := countKinds(events)
	if counts[event.ModelFailed] != 1 || counts[event.TurnFailed] != 1 || counts[event.RunCancelled] != 1 {
		t.Fatalf("terminal counts = %v", counts)
	}
}

func TestRunCompletionDoesNotRequireSubscriber(t *testing.T) {
	script := make([]model.StreamEvent, 0, 302)
	for range 300 {
		script = append(script, delta(t, "x"))
	}
	script = append(script, completed(t))
	engine := newEngine(t, &scriptedProvider{scripts: [][]model.StreamEvent{script}}, nil, nil, nil)
	run := startRun(t, engine, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait without subscriber: %v", err)
	}
	events := collect(t, run)
	if len(events) < 300 || events[len(events)-1].Kind() != event.RunCompleted {
		t.Fatalf("events = %d", len(events))
	}
}

type failingObserver struct{ kind event.Kind }

func (observer failingObserver) Publish(_ context.Context, envelope event.Envelope) error {
	if envelope.Kind() == observer.kind {
		return errors.New("observer failure after external side effect")
	}
	return nil
}

func TestObserverFailureConsumesSequenceAndTerminalUsesNext(t *testing.T) {
	call, _ := tool.NewCall("call", "read", json.RawMessage(`{}`))
	for _, test := range []struct {
		name   string
		kind   event.Kind
		script []model.StreamEvent
		tools  map[string]tool.Tool
		want   []event.Kind
	}{
		{
			"turn", event.TurnStarted,
			[]model.StreamEvent{completed(t)},
			nil,
			[]event.Kind{event.RunStarted, event.TurnStarted, event.TurnFailed, event.RunFailed},
		},
		{
			"model", event.ModelStarted,
			[]model.StreamEvent{completed(t)},
			nil,
			[]event.Kind{event.RunStarted, event.TurnStarted, event.ModelStarted, event.ModelFailed, event.TurnFailed, event.RunFailed},
		},
		{
			"tool", event.ToolStarted,
			[]model.StreamEvent{toolEvent(t, call), completed(t)},
			map[string]tool.Tool{"read": testTool{}},
			[]event.Kind{event.RunStarted, event.TurnStarted, event.ModelStarted, event.ModelCompleted, event.ToolStarted, event.ToolFailed, event.TurnFailed, event.RunFailed},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &scriptedProvider{scripts: [][]model.StreamEvent{test.script}}
			engine := newEngine(t, provider, test.tools, []event.Observer{failingObserver{test.kind}}, nil)
			run := startRun(t, engine, 1)
			events := collect(t, run)
			if err := run.Wait(t.Context()); err == nil {
				t.Fatal("observer failure did not fail run")
			}
			assertKindsAndSequence(t, events, test.want)
		})
	}
}

type blockingObserver struct{ kind event.Kind }

func (observer blockingObserver) Publish(ctx context.Context, envelope event.Envelope) error {
	if envelope.Kind() != observer.kind {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestTerminalDurabilityIsBoundedAndTyped(t *testing.T) {
	dispatcher, _ := stage.NewDispatcher(nil)
	options := agent.DefaultEngineOptions()
	options.FinalizationTimeout = 10 * time.Millisecond
	engine, err := agent.NewEngineWithOptions(&scriptedProvider{scripts: [][]model.StreamEvent{{completed(t)}}}, dispatcher, &agent.AtomicIDSource{}, time.Now, []event.Observer{blockingObserver{event.RunCompleted}}, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	run := startRun(t, engine, 1)
	waitContext, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err = run.Wait(waitContext)
	if _, ok := errors.AsType[*agent.DurabilityError](err); !ok {
		t.Fatalf("Wait error = %v", err)
	}
}

func TestEngineContainsPanicsAndAggregatesCloseError(t *testing.T) {
	t.Run("provider", func(t *testing.T) {
		engine := newEngine(t, &scriptedProvider{panicAt: true}, nil, nil, nil)
		run := startRun(t, engine, 1)
		_ = collect(t, run)
		if err := run.Wait(t.Context()); err == nil {
			t.Fatalf("Wait error = %v", err)
		}
	})
	t.Run("tool", func(t *testing.T) {
		call, _ := tool.NewCall("call", "read", json.RawMessage(`{}`))
		provider := &scriptedProvider{scripts: [][]model.StreamEvent{{toolEvent(t, call), completed(t)}}}
		engine := newEngine(t, provider, map[string]tool.Tool{"read": testTool{panicAt: true}}, nil, nil)
		run := startRun(t, engine, 1)
		events := collect(t, run)
		if err := run.Wait(t.Context()); err == nil || !strings.Contains(err.Error(), "tool \"read\" panic") {
			t.Fatalf("Wait error = %v", err)
		}
		if countKinds(events)[event.ToolFailed] != 1 {
			t.Fatal("tool panic lacked terminal")
		}
	})
	t.Run("close", func(t *testing.T) {
		provider := &scriptedProvider{scripts: [][]model.StreamEvent{{completed(t)}}, closeErr: errors.New("close failed")}
		run := startRun(t, newEngine(t, provider, nil, nil, nil), 1)
		_ = collect(t, run)
		if err := run.Wait(t.Context()); err == nil || !strings.Contains(err.Error(), "close failed") {
			t.Fatalf("Wait error = %v", err)
		}
	})
}

func TestFirstFailedStreamItemIsObservedAndNotRetryable(t *testing.T) {
	problem, _ := model.NewProblem("unavailable", "offline", true)
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{{failed(t, problem)}}}
	run := startRun(t, newEngine(t, provider, nil, nil, nil), 1)
	_ = collect(t, run)
	err := run.Wait(t.Context())
	var operationError *model.OperationError
	if !errors.As(err, &operationError) || operationError.BeforeStream() || operationError.Retryable() {
		t.Fatalf("operation error = %#v, %v", operationError, err)
	}
}

func TestTypedRecvFailurePreservesHostObservedRetryPosition(t *testing.T) {
	metadata, _ := model.NewMetadata("openai.responses", json.RawMessage(`{"request_id":"req-1"}`))
	problem, _ := model.NewProblem("connection_reset", "provider connection reset", true, metadata)
	streamError, _ := model.NewStreamError(problem, errors.New("transport detail"))
	for _, test := range []struct {
		name      string
		events    []model.StreamEvent
		before    bool
		retryable bool
	}{
		{"before first item", nil, true, true},
		{"after delta", []model.StreamEvent{delta(t, "partial")}, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &scriptedProvider{scripts: [][]model.StreamEvent{test.events}, recvErr: streamError}
			run := startRun(t, newEngine(t, provider, nil, nil, nil), 1)
			_ = collect(t, run)
			err := run.Wait(t.Context())
			operationError, ok := errors.AsType[*model.OperationError](err)
			if !ok || operationError.BeforeStream() != test.before || operationError.Retryable() != test.retryable || operationError.Problem().Code() != "connection_reset" {
				t.Fatalf("operation error = %#v, %v", operationError, err)
			}
		})
	}
	t.Run("generic", func(t *testing.T) {
		provider := &scriptedProvider{scripts: [][]model.StreamEvent{nil}, recvErr: errors.New("raw transport detail")}
		run := startRun(t, newEngine(t, provider, nil, nil, nil), 1)
		_ = collect(t, run)
		err := run.Wait(t.Context())
		operationError, ok := errors.AsType[*model.OperationError](err)
		if !ok || operationError.Problem().Code() != "provider_stream" || !operationError.BeforeStream() || operationError.Retryable() {
			t.Fatalf("generic operation error = %#v, %v", operationError, err)
		}
	})
}

func TestEngineCarriesOnlyAllowlistedSafeProviderMetadata(t *testing.T) {
	allowed, _ := model.NewMetadata("openai.responses", json.RawMessage(`{"response_id":"resp-1"}`))
	blocked, _ := model.NewMetadata("private.debug", json.RawMessage(`{"secret":"DO_NOT_EMIT"}`))
	options := agent.DefaultEngineOptions()
	options.MetadataNamespaces = []string{"openai.responses"}
	newConfigured := func(t *testing.T, provider model.Provider) *agent.Engine {
		t.Helper()
		dispatcher, _ := stage.NewDispatcher(nil)
		engine, err := agent.NewEngineWithOptions(provider, dispatcher, &agent.AtomicIDSource{}, time.Now, nil, nil, options)
		if err != nil {
			t.Fatal(err)
		}
		return engine
	}
	t.Run("completion", func(t *testing.T) {
		terminal, err := model.Completed(model.NewUsage(2, 3), blocked, allowed)
		if err != nil {
			t.Fatal(err)
		}
		run := startRun(t, newConfigured(t, &scriptedProvider{scripts: [][]model.StreamEvent{{terminal}}}), 1)
		events := collect(t, run)
		payload := eventData(t, events, event.ModelCompleted)
		if !strings.Contains(payload, "resp-1") || strings.Contains(payload, "DO_NOT_EMIT") {
			t.Fatalf("completed payload = %s", payload)
		}
	})
	t.Run("failure cause redaction", func(t *testing.T) {
		problem, _ := model.NewProblem("rate_limit", "try later", true, allowed, blocked)
		providerError, _ := model.NewProviderError(problem, errors.New("SUPERSECRET provider cause"))
		run := startRun(t, newConfigured(t, &scriptedProvider{startErr: providerError}), 1)
		events := collect(t, run)
		payload := eventData(t, events, event.ModelFailed)
		if !strings.Contains(payload, `"code":"rate_limit"`) || !strings.Contains(payload, "resp-1") ||
			strings.Contains(payload, "SUPERSECRET") || strings.Contains(payload, "DO_NOT_EMIT") {
			t.Fatalf("failed payload = %s", payload)
		}
	})
}

func TestEngineLifecycleRejectsNewAndCanForceCancellation(t *testing.T) {
	engine := newEngine(t, blockingProvider{}, nil, nil, nil)
	run := startRun(t, engine, 1)
	if err := engine.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := run.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v", err)
	}
	definition, _ := agent.NewDefinition("test", "scripted", 1)
	input, _ := agent.NewInput(inputMessage(t))
	if _, err := engine.Start(t.Context(), definition, input); err == nil {
		t.Fatal("closed engine accepted run")
	}
	var nilContext context.Context
	if err := engine.Close(nilContext); err == nil {
		t.Fatal("nil close context succeeded")
	}
}

func TestEngineRejectsDuplicateActiveRunID(t *testing.T) {
	dispatcher, _ := stage.NewDispatcher(nil)
	engine, err := agent.NewEngine(blockingProvider{}, dispatcher, fixedIDSource{value: "same"}, time.Now, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := agent.NewDefinition("test", "scripted", 1)
	input, _ := agent.NewInput(inputMessage(t))
	first, err := engine.Start(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.Start(t.Context(), definition, input); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate start error = %v", err)
	}
	if err = engine.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = first.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("first run = %v", err)
	}
}

func TestEngineRejectsInvalidConstructionAndContexts(t *testing.T) {
	dispatcher, _ := stage.NewDispatcher(nil)
	ids := &agent.AtomicIDSource{}
	provider := &scriptedProvider{}
	if _, err := agent.NewEngine(nil, dispatcher, ids, time.Now, nil, nil); err == nil {
		t.Fatal("nil provider succeeded")
	}
	if _, err := agent.NewEngine(provider, nil, ids, time.Now, nil, nil); err == nil {
		t.Fatal("nil dispatcher succeeded")
	}
	if _, err := agent.NewEngine(provider, dispatcher, nil, time.Now, nil, nil); err == nil {
		t.Fatal("nil IDs succeeded")
	}
	if _, err := agent.NewEngine(provider, dispatcher, ids, nil, nil, nil); err == nil {
		t.Fatal("nil clock succeeded")
	}
	options := agent.DefaultEngineOptions()
	options.FinalizationTimeout = 0
	if _, err := agent.NewEngineWithOptions(provider, dispatcher, ids, time.Now, nil, nil, options); err == nil {
		t.Fatal("zero finalization timeout succeeded")
	}
	options = agent.DefaultEngineOptions()
	options.MetadataNamespaces = []string{"duplicate", "duplicate"}
	if _, err := agent.NewEngineWithOptions(provider, dispatcher, ids, time.Now, nil, nil, options); err == nil {
		t.Fatal("duplicate metadata allowlist succeeded")
	}
	if _, err := agent.NewDefinition("", "model", 1); err == nil {
		t.Fatal("invalid definition name succeeded")
	}
	if _, err := agent.NewDefinition("agent", "", 1); err == nil {
		t.Fatal("invalid model name succeeded")
	}
	if _, err := agent.NewDefinition("agent", "model", 0); err == nil {
		t.Fatal("invalid maximum turns succeeded")
	}
	definition, _ := agent.NewDefinition("test", "scripted", 1)
	input, _ := agent.NewInput(inputMessage(t))
	engine := newEngine(t, provider, nil, nil, nil)
	var nilContext context.Context
	if _, err := engine.Start(nilContext, definition, input); err == nil {
		t.Fatal("nil start context succeeded")
	}
	var nilEngine *agent.Engine
	if _, err := nilEngine.Start(t.Context(), definition, input); err == nil {
		t.Fatal("nil engine succeeded")
	}
	var nilIDs *agent.AtomicIDSource
	if _, err := nilIDs.Next("run"); err == nil {
		t.Fatal("nil ID source succeeded")
	}
}

func TestPublicAccessorsAndTypedDurabilityErrors(t *testing.T) {
	definition, _ := agent.NewDefinition("agent", "model", 3)
	if definition.Name() != "agent" || definition.Model() != "model" || definition.MaxTurns() != 3 {
		t.Fatal("definition accessors mismatch")
	}
	durability := &agent.DurabilityError{Kind: event.RunFailed, Cause: io.ErrUnexpectedEOF}
	if !strings.Contains(durability.Error(), "run.failed") || !errors.Is(durability, io.ErrUnexpectedEOF) {
		t.Fatal("durability error contract mismatch")
	}
	engine := newEngine(t, &scriptedProvider{scripts: [][]model.StreamEvent{{completed(t)}}}, nil, nil, nil)
	run := startRun(t, engine, 1)
	if run.ID() == "" {
		t.Fatal("run ID accessor empty")
	}
	_ = collect(t, run)
	if _, err := (*agent.Run)(nil).Subscribe(t.Context(), 0); err == nil {
		t.Fatal("nil run subscription succeeded")
	}
}

func TestBestEffortObserverDropsWithoutBlocking(t *testing.T) {
	mailbox, err := event.NewBestEffortObserver(1)
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{{delta(t, "x"), completed(t)}}}
	run := startRun(t, newEngine(t, provider, nil, nil, []*event.BestEffortObserver{mailbox}), 1)
	_ = collect(t, run)
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

func delta(t *testing.T, value string) model.StreamEvent {
	t.Helper()
	result, err := model.TextDelta(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func completed(t *testing.T) model.StreamEvent {
	t.Helper()
	result, err := model.Completed(model.NewUsage(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func toolEvent(t *testing.T, call tool.Call) model.StreamEvent {
	t.Helper()
	result, err := model.ToolCallEvent(call)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func failed(t *testing.T, problem model.Problem) model.StreamEvent {
	t.Helper()
	result, err := model.Failed(problem)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func collect(t *testing.T, run *agent.Run) []event.Envelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	subscription, err := run.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	var result []event.Envelope
	for envelope := range subscription.Events() {
		result = append(result, envelope)
	}
	if err := subscription.Wait(ctx); err != nil {
		t.Fatal(err)
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

func eventData(t *testing.T, events []event.Envelope, kind event.Kind) string {
	t.Helper()
	for _, envelope := range events {
		if envelope.Kind() == kind {
			return string(envelope.Data())
		}
	}
	t.Fatalf("event %s not found", kind)
	return ""
}

func assertKindsAndSequence(t *testing.T, events []event.Envelope, want []event.Kind) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d: %v", len(events), len(want), countKinds(events))
	}
	for index, envelope := range events {
		if envelope.Sequence() != uint64(index+1) || envelope.Kind() != want[index] {
			t.Fatalf("event %d = %d/%s, want %d/%s", index, envelope.Sequence(), envelope.Kind(), index+1, want[index])
		}
	}
}

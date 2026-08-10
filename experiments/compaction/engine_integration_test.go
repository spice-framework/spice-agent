package compaction_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	compaction "github.com/spice-framework/spice-agent/experiments/compaction"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

func TestEngineKeepsAuthoritativeHistorySnapshotsAndEventsUncompacted(t *testing.T) {
	base := &roundProvider{}
	provider, err := compaction.NewProvider(base, compaction.Options{
		TriggerBytes: 256, RetainRecentRounds: 0, MaximumSummaryBytes: 512,
	})
	if err != nil {
		t.Fatal(err)
	}
	implementation := newEchoTool(t)
	dispatcher, err := stage.NewDispatcher(map[string]tool.Tool{implementation.definition.Name(): implementation})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &event.Recorder{}
	engine, err := agent.NewEngine(
		provider, dispatcher, &agent.AtomicIDSource{}, time.Now,
		[]event.Observer{recorder}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if closeErr := engine.Close(closeContext); closeErr != nil {
			t.Errorf("engine.Close() error = %v", closeErr)
		}
	})
	definition, err := agent.NewDefinition("compaction-proof", "scripted", 2)
	if err != nil {
		t.Fatal(err)
	}
	initial := mustTextMessage(t, "input", message.RoleUser, strings.Repeat("durable input ", 80))
	input, err := agent.NewInput(initial)
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.Start(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := run.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	history := snapshot.History()
	if len(history) != 3 || history[0].ID() != "input" || history[1].Role() != message.RoleAssistant ||
		history[2].Role() != message.RoleTool {
		t.Fatalf("authoritative snapshot history = %s", messageSignature(history))
	}
	for _, value := range history {
		if value.Role() == message.RoleSystem {
			t.Fatal("transient compaction summary entered the authoritative snapshot")
		}
	}
	requests := base.Requests()
	if len(requests) != 2 || len(requests[0].Messages()) != 1 || len(requests[1].Messages()) != 2 ||
		requests[1].Messages()[1].Role() != message.RoleSystem {
		t.Fatalf("delegate request histories = %d/%s", len(requests), requestSignatures(requests))
	}
	kinds := make(map[event.Kind]int)
	for _, envelope := range recorder.Events() {
		kinds[envelope.Kind()]++
	}
	if kinds[event.ToolStarted] != 1 || kinds[event.ToolCompleted] != 1 || kinds[event.RunCompleted] != 1 ||
		kinds[event.ModelStarted] != 2 || kinds[event.ModelCompleted] != 2 {
		t.Fatalf("authoritative event counts = %#v", kinds)
	}
}

type roundProvider struct {
	mu       sync.Mutex
	requests []model.Request
}

func (provider *roundProvider) Stream(_ context.Context, request model.Request) (model.Stream, error) {
	provider.mu.Lock()
	provider.requests = append(provider.requests, request)
	call := len(provider.requests)
	provider.mu.Unlock()
	completed, _ := model.Completed(model.NewUsage(1, 1))
	if call == 1 {
		arguments, _ := json.Marshal(map[string]string{"value": strings.Repeat("large input ", 200)})
		toolCall, _ := tool.NewCall("call-1", "fixture.echo", arguments)
		callEvent, _ := model.ToolCallEvent(toolCall)
		return &eventStream{events: []model.StreamEvent{callEvent, completed}}, nil
	}
	text, _ := model.TextDelta("done")
	return &eventStream{events: []model.StreamEvent{text, completed}}, nil
}

func (provider *roundProvider) Requests() []model.Request {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]model.Request(nil), provider.requests...)
}

type eventStream struct {
	events []model.StreamEvent
	index  int
}

func (stream *eventStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return model.StreamEvent{}, err
	}
	if stream.index >= len(stream.events) {
		return model.StreamEvent{}, io.EOF
	}
	value := stream.events[stream.index]
	stream.index++
	return value, nil
}

func (*eventStream) Close() error { return nil }

type echoTool struct{ definition tool.Definition }

func newEchoTool(t *testing.T) *echoTool {
	t.Helper()
	definition, err := tool.NewDefinition(
		"fixture.echo", "Echo one fixture value.", json.RawMessage(`{"type":"object"}`),
		tool.EffectReadOnly, tool.ReplaySafe,
	)
	if err != nil {
		t.Fatal(err)
	}
	return &echoTool{definition: definition}
}

func (implementation *echoTool) Definition() tool.Definition {
	return implementation.definition.Clone()
}

func (*echoTool) Execute(_ context.Context, call tool.Call, _ tool.Reporter) (tool.Result, error) {
	content, _ := json.Marshal(map[string]string{"value": strings.Repeat("large result ", 200)})
	return tool.NewResult(call.ID(), content)
}

func requestSignatures(requests []model.Request) string {
	var result strings.Builder
	for _, request := range requests {
		result.WriteString(messageSignature(request.Messages()))
		result.WriteByte('|')
	}
	return result.String()
}

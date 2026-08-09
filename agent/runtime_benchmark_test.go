package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

func BenchmarkKernelEngineConstruction(b *testing.B) {
	provider := benchmarkFixedProvider{events: []model.StreamEvent{benchmarkCompletedEvent(b)}}
	b.ReportAllocs()
	for b.Loop() {
		engine := benchmarkEngine(b, provider, nil)
		if err := engine.Close(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKernelTextRun(b *testing.B) {
	provider := benchmarkFixedProvider{events: []model.StreamEvent{benchmarkCompletedEvent(b)}}
	engine := benchmarkEngine(b, provider, nil)
	definition, input := benchmarkRunValues(b, 1)
	b.Cleanup(func() { closeBenchmarkEngine(b, engine) })
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		run, err := engine.Start(b.Context(), definition, input)
		if err != nil {
			b.Fatal(err)
		}
		if err = run.Wait(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKernelToolRound(b *testing.B) {
	call, err := tool.NewCall("benchmark-call", "read", json.RawMessage(`{}`))
	if err != nil {
		b.Fatal(err)
	}
	provider := benchmarkToolProvider{
		call:      benchmarkToolCallEvent(b, call),
		completed: benchmarkCompletedEvent(b),
	}
	engine := benchmarkEngine(b, provider, map[string]tool.Tool{"read": benchmarkReadTool{}})
	definition, input := benchmarkRunValues(b, 2)
	b.Cleanup(func() { closeBenchmarkEngine(b, engine) })
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		run, startErr := engine.Start(b.Context(), definition, input)
		if startErr != nil {
			b.Fatal(startErr)
		}
		if waitErr := run.Wait(b.Context()); waitErr != nil {
			b.Fatal(waitErr)
		}
	}
}

func BenchmarkKernelCancellation(b *testing.B) {
	started := make(chan struct{})
	engine := benchmarkEngine(b, benchmarkBlockingProvider{started: started}, nil)
	definition, input := benchmarkRunValues(b, 1)
	b.Cleanup(func() { closeBenchmarkEngine(b, engine) })
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		run, err := engine.Start(b.Context(), definition, input)
		if err != nil {
			b.Fatal(err)
		}
		select {
		case <-started:
		case <-b.Context().Done():
			b.Fatal(b.Context().Err())
		}
		run.Cancel()
		if err = run.Wait(b.Context()); !errors.Is(err, context.Canceled) {
			b.Fatalf("cancelled run = %v", err)
		}
	}
}

type benchmarkFixedProvider struct{ events []model.StreamEvent }

func (provider benchmarkFixedProvider) Stream(context.Context, model.Request) (model.Stream, error) {
	return &benchmarkStream{events: provider.events}, nil
}

type benchmarkToolProvider struct {
	call      model.StreamEvent
	completed model.StreamEvent
}

func (provider benchmarkToolProvider) Stream(_ context.Context, request model.Request) (model.Stream, error) {
	events := []model.StreamEvent{provider.call, provider.completed}
	messages := request.Messages()
	if len(messages) != 0 && messages[len(messages)-1].Role() == message.RoleTool {
		events = []model.StreamEvent{provider.completed}
	}
	return &benchmarkStream{events: events}, nil
}

type benchmarkBlockingProvider struct{ started chan<- struct{} }

func (provider benchmarkBlockingProvider) Stream(ctx context.Context, _ model.Request) (model.Stream, error) {
	select {
	case provider.started <- struct{}{}:
		return benchmarkBlockingStream{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type benchmarkStream struct {
	events []model.StreamEvent
	index  int
}

func (stream *benchmarkStream) Recv(context.Context) (model.StreamEvent, error) {
	if stream.index == len(stream.events) {
		return model.StreamEvent{}, io.EOF
	}
	result := stream.events[stream.index]
	stream.index++
	return result, nil
}

func (*benchmarkStream) Close() error { return nil }

type benchmarkBlockingStream struct{}

func (benchmarkBlockingStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	<-ctx.Done()
	return model.StreamEvent{}, ctx.Err()
}

func (benchmarkBlockingStream) Close() error { return nil }

type benchmarkReadTool struct{}

func (benchmarkReadTool) Definition() tool.Definition {
	definition, _ := tool.NewDefinition(
		"read", "Read benchmark data.", json.RawMessage(`{}`),
		tool.EffectReadOnly, tool.ReplaySafe, tool.CapabilityFilesystemRead,
	)
	return definition
}

func (benchmarkReadTool) Execute(_ context.Context, call tool.Call, _ tool.Reporter) (tool.Result, error) {
	return tool.NewResult(call.ID(), json.RawMessage(`{"content":"fixture"}`))
}

func benchmarkEngine(b *testing.B, provider model.Provider, tools map[string]tool.Tool) *agent.Engine {
	b.Helper()
	dispatcher, err := stage.NewDispatcher(tools)
	if err != nil {
		b.Fatal(err)
	}
	engine, err := agent.NewEngineWithOptions(
		provider,
		dispatcher,
		&agent.AtomicIDSource{},
		func() time.Time { return time.Unix(1, 0).UTC() },
		nil,
		nil,
		agent.DefaultEngineOptions(),
	)
	if err != nil {
		b.Fatal(err)
	}
	return engine
}

func benchmarkRunValues(b *testing.B, turns uint32) (agent.Definition, agent.Input) {
	b.Helper()
	definition, err := agent.NewDefinition("benchmark", "scripted", turns)
	if err != nil {
		b.Fatal(err)
	}
	part, err := message.Text("hello")
	if err != nil {
		b.Fatal(err)
	}
	messageID, err := message.NewID("benchmark-input")
	if err != nil {
		b.Fatal(err)
	}
	initial, err := message.New(messageID, message.RoleUser, part)
	if err != nil {
		b.Fatal(err)
	}
	input, err := agent.NewInput(initial)
	if err != nil {
		b.Fatal(err)
	}
	return definition, input
}

func benchmarkCompletedEvent(b *testing.B) model.StreamEvent {
	b.Helper()
	result, err := model.Completed(model.NewUsage(1, 1))
	if err != nil {
		b.Fatal(err)
	}
	return result
}

func benchmarkToolCallEvent(b *testing.B, call tool.Call) model.StreamEvent {
	b.Helper()
	result, err := model.ToolCallEvent(call)
	if err != nil {
		b.Fatal(err)
	}
	return result
}

func closeBenchmarkEngine(b *testing.B, engine *agent.Engine) {
	b.Helper()
	if err := engine.Close(context.Background()); err != nil {
		b.Error(err)
	}
}

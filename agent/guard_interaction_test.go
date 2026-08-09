package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

func TestGuardInteractionUsesRunLifecycleAndPreservesTerminalOrder(t *testing.T) {
	request, _ := interaction.NewRequest("tool-approval", "confirm", "Allow read?", json.RawMessage(`{}`))
	response, _ := interaction.NewResponse(request.ID(), json.RawMessage(`true`))
	broker := &scriptedBroker{response: response}
	guard := engineGuardFunc(func(ctx context.Context, scope stage.ToolDispatchScope, _ tool.Definition, _ tool.Call, next stage.ToolDispatchNext) (tool.Result, error) {
		got, err := scope.RequestInteraction(ctx, request)
		if err != nil {
			return tool.Result{}, err
		}
		if got.ID() != response.ID() {
			return tool.Result{}, errors.New("guard interaction response mismatch")
		}
		return next()
	})
	run := startRun(t, newGuardInteractionEngine(t, broker, guard), 2)
	events := collect(t, run)
	if err := run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	counts := countKinds(events)
	if counts[event.ToolStarted] != 1 || counts[event.InteractionStarted] != 1 ||
		counts[event.InteractionCompleted] != 1 || counts[event.InteractionFailed] != 0 ||
		counts[event.InteractionCancelled] != 0 || counts[event.ToolCompleted] != 1 || counts[event.ToolFailed] != 0 {
		t.Fatalf("guard interaction lifecycle = %v", counts)
	}
	assertEventOrder(
		t, events,
		event.ToolStarted, event.InteractionStarted, event.InteractionCompleted, event.ToolCompleted,
	)
}

func TestPendingGuardInteractionBlocksSnapshotAndRunCancellationJoinsIt(t *testing.T) {
	request, _ := interaction.NewRequest("tool-approval", "confirm", "Allow read?", json.RawMessage(`{}`))
	broker := &scriptedBroker{started: make(chan struct{}), block: true}
	guard := engineGuardFunc(func(ctx context.Context, scope stage.ToolDispatchScope, _ tool.Definition, _ tool.Call, next stage.ToolDispatchNext) (tool.Result, error) {
		if _, err := scope.RequestInteraction(ctx, request); err != nil {
			return tool.Result{}, err
		}
		return next()
	})
	run := startRun(t, newGuardInteractionEngine(t, broker, guard), 1)
	select {
	case <-broker.started:
	case <-time.After(time.Second):
		t.Fatal("guard interaction did not reach the broker")
	}
	if _, err := run.ExportSnapshot(); err == nil {
		t.Fatal("snapshot with pending guard interaction succeeded")
	} else {
		var unsafe *agent.UnsafeSnapshotError
		if !errors.As(err, &unsafe) || unsafe.ActiveInteractions != 1 {
			t.Fatalf("pending snapshot error = %T, %v", err, err)
		}
	}
	run.Cancel()
	events := collect(t, run)
	if err := run.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled guard interaction run = %v", err)
	}
	counts := countKinds(events)
	if counts[event.ToolStarted] != 1 || counts[event.InteractionStarted] != 1 ||
		counts[event.InteractionCancelled] != 1 || counts[event.InteractionCompleted] != 0 ||
		counts[event.InteractionFailed] != 0 || counts[event.ToolFailed] != 1 || counts[event.ToolCompleted] != 0 ||
		counts[event.RunCancelled] != 1 {
		t.Fatalf("cancelled guard interaction lifecycle = %v", counts)
	}
	assertEventOrder(
		t, events,
		event.ToolStarted, event.InteractionStarted, event.InteractionCancelled, event.ToolFailed, event.RunCancelled,
	)
}

func newGuardInteractionEngine(
	t *testing.T,
	broker interaction.Broker,
	guard stage.ToolDispatchGuard,
) *agent.Engine {
	t.Helper()
	call, _ := tool.NewCall("guard-call", "read", json.RawMessage(`{}`))
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{
		{toolEvent(t, call), completed(t)},
		{completed(t)},
	}}
	base, err := stage.NewDispatcher(map[string]tool.Tool{"read": testTool{}})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guard}, nil)
	if err != nil {
		t.Fatal(err)
	}
	options := agent.DefaultEngineOptions()
	options.SnapshotCompatibilityIdentity = testSnapshotCompatibility
	options.WorkspaceFingerprint = testWorkspaceFingerprint
	engine, err := agent.NewEngineWithInteractionBroker(
		provider, dispatcher, broker, &agent.AtomicIDSource{}, time.Now, nil, nil, options,
	)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func assertEventOrder(t *testing.T, events []event.Envelope, wanted ...event.Kind) {
	t.Helper()
	position := -1
	for _, kind := range wanted {
		found := -1
		for index := position + 1; index < len(events); index++ {
			if events[index].Kind() == kind {
				found = index
				break
			}
		}
		if found < 0 {
			t.Fatalf("event %s missing after index %d: %v", kind, position, eventKinds(events))
		}
		position = found
	}
}

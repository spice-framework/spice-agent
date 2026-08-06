package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/stage"
)

type scriptedBroker struct {
	response       interaction.Response
	err            error
	panicAt        bool
	started        chan struct{}
	block          bool
	cancelOnReturn context.CancelFunc
}

func (broker *scriptedBroker) Request(ctx context.Context, _ interaction.Request) (interaction.Response, error) {
	if broker.panicAt {
		panic("broker boom")
	}
	if broker.started != nil {
		close(broker.started)
	}
	if broker.block {
		<-ctx.Done()
		return interaction.Response{}, ctx.Err()
	}
	if broker.cancelOnReturn != nil {
		broker.cancelOnReturn()
	}
	return broker.response, broker.err
}

func TestInteractionLifecycleCompletesThroughInjectedBroker(t *testing.T) {
	request, _ := interaction.NewRequest("interaction-1", "confirm", "Continue?", json.RawMessage(`{"type":"boolean"}`))
	response, _ := interaction.NewResponse(request.ID(), json.RawMessage(`true`))
	broker := &scriptedBroker{response: response}
	engine := newInteractionEngine(t, broker, nil)
	run := startRun(t, engine, 1)
	waitForModelStart(t, run)
	got, err := run.Interact(t.Context(), request)
	if err != nil || got.ID() != request.ID() || string(got.Value()) != "true" {
		t.Fatalf("interaction response = %s, %v", got.ID(), err)
	}
	if _, err = run.Interact(t.Context(), request); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("completed interaction ID reuse = %v", err)
	}
	run.Cancel()
	events := collect(t, run)
	counts := countKinds(events)
	if counts[event.InteractionStarted] != 1 || counts[event.InteractionCompleted] != 1 || counts[event.InteractionFailed] != 0 || counts[event.InteractionCancelled] != 0 {
		t.Fatalf("interaction events = %v", counts)
	}
	for _, envelope := range events {
		if strings.Contains(string(envelope.Data()), "true") {
			t.Fatalf("interaction response leaked into %s payload", envelope.Kind())
		}
	}
}

func TestCancellationAtInteractionSuccessBoundaryFinalizesCancelled(t *testing.T) {
	request, _ := interaction.NewRequest("interaction-1", "confirm", "Continue?", json.RawMessage(`{}`))
	response, _ := interaction.NewResponse(request.ID(), json.RawMessage(`{"canary":"NEVER_EVENT"}`))
	interactionContext, cancelInteraction := context.WithCancel(t.Context())
	broker := &scriptedBroker{response: response, cancelOnReturn: cancelInteraction}
	engine := newInteractionEngine(t, broker, nil)
	run := startRun(t, engine, 1)
	waitForModelStart(t, run)
	if _, err := run.Interact(interactionContext, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("interaction boundary cancellation = %v", err)
	}
	run.Cancel()
	events := collect(t, run)
	counts := countKinds(events)
	if counts[event.InteractionStarted] != 1 || counts[event.InteractionCancelled] != 1 || counts[event.InteractionCompleted] != 0 || counts[event.InteractionFailed] != 0 {
		t.Fatalf("interaction events = %v", counts)
	}
	for _, envelope := range events {
		if strings.Contains(string(envelope.Data()), "NEVER_EVENT") {
			t.Fatalf("interaction response leaked into %s payload", envelope.Kind())
		}
	}
}

func TestInteractionFailuresPanicsAndObserverErrorsFinalizeOnce(t *testing.T) {
	request, _ := interaction.NewRequest("interaction-1", "confirm", "Continue?", json.RawMessage(`{}`))
	for _, test := range []struct {
		name      string
		broker    *scriptedBroker
		observers []event.Observer
		contains  string
	}{
		{"broker failure", &scriptedBroker{err: errors.New("declined")}, nil, "declined"},
		{"broker panic", &scriptedBroker{panicAt: true}, nil, "broker panic"},
		{"observer failure", &scriptedBroker{}, []event.Observer{failingObserver{event.InteractionStarted}}, "observer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := newInteractionEngine(t, test.broker, test.observers)
			run := startRun(t, engine, 1)
			waitForModelStart(t, run)
			if _, err := run.Interact(t.Context(), request); err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("interaction error = %v", err)
			}
			run.Cancel()
			events := collect(t, run)
			counts := countKinds(events)
			if counts[event.InteractionStarted] != 1 || counts[event.InteractionFailed] != 1 || counts[event.InteractionCompleted] != 0 || counts[event.InteractionCancelled] != 0 {
				t.Fatalf("interaction events = %v", counts)
			}
		})
	}
}

func TestInteractionTerminalObserverFailureDoesNotAddSecondTerminal(t *testing.T) {
	request, _ := interaction.NewRequest("interaction-1", "confirm", "Continue?", json.RawMessage(`{}`))
	response, _ := interaction.NewResponse(request.ID(), json.RawMessage(`true`))
	engine := newInteractionEngine(t, &scriptedBroker{response: response}, []event.Observer{failingObserver{event.InteractionCompleted}})
	run := startRun(t, engine, 1)
	waitForModelStart(t, run)
	if _, err := run.Interact(t.Context(), request); err == nil {
		t.Fatal("terminal observer failure did not surface")
	}
	run.Cancel()
	events := collect(t, run)
	counts := countKinds(events)
	if counts[event.InteractionCompleted] != 1 || counts[event.InteractionFailed] != 0 || counts[event.InteractionCancelled] != 0 {
		t.Fatalf("interaction terminals = %v", counts)
	}
}

func TestRunCancellationCancelsInteractionBeforeRunTerminal(t *testing.T) {
	request, _ := interaction.NewRequest("interaction-1", "confirm", "Continue?", json.RawMessage(`{}`))
	broker := &scriptedBroker{started: make(chan struct{}), block: true}
	engine := newInteractionEngine(t, broker, nil)
	run := startRun(t, engine, 1)
	waitForModelStart(t, run)
	interactionDone := make(chan error, 1)
	go func() {
		_, err := run.Interact(t.Context(), request)
		interactionDone <- err
	}()
	select {
	case <-broker.started:
	case <-time.After(time.Second):
		t.Fatal("broker did not start")
	}
	run.Cancel()
	if err := <-interactionDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("interaction cancellation = %v", err)
	}
	events := collect(t, run)
	counts := countKinds(events)
	if counts[event.InteractionCancelled] != 1 || counts[event.RunCancelled] != 1 {
		t.Fatalf("terminal events = %v", counts)
	}
	interactionIndex, runIndex := -1, -1
	for index, envelope := range events {
		switch envelope.Kind() {
		case event.InteractionCancelled:
			interactionIndex = index
		case event.RunCancelled:
			runIndex = index
		}
	}
	if interactionIndex < 0 || runIndex <= interactionIndex {
		t.Fatalf("terminal order = %v", eventKinds(events))
	}
}

func TestInteractionRejectsMismatchedAndDuplicateResponses(t *testing.T) {
	request, _ := interaction.NewRequest("interaction-1", "confirm", "Continue?", json.RawMessage(`{}`))
	wrong, _ := interaction.NewResponse("wrong", json.RawMessage(`true`))
	engine := newInteractionEngine(t, &scriptedBroker{response: wrong}, nil)
	run := startRun(t, engine, 1)
	waitForModelStart(t, run)
	if _, err := run.Interact(t.Context(), request); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched response = %v", err)
	}
	run.Cancel()
	_ = run.Wait(t.Context())

	blockingBroker := &scriptedBroker{started: make(chan struct{}), block: true}
	engine = newInteractionEngine(t, blockingBroker, nil)
	run = startRun(t, engine, 1)
	waitForModelStart(t, run)
	firstDone := make(chan error, 1)
	go func() {
		_, interactionErr := run.Interact(t.Context(), request)
		firstDone <- interactionErr
	}()
	<-blockingBroker.started
	if _, err := run.Interact(t.Context(), request); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("duplicate interaction = %v", err)
	}
	run.Cancel()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first interaction cancellation = %v", err)
	}
	_ = run.Wait(t.Context())
}

func newInteractionEngine(t *testing.T, broker interaction.Broker, observers []event.Observer) *agent.Engine {
	t.Helper()
	dispatcher, err := stage.NewDispatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := agent.NewEngineWithInteractionBroker(
		blockingProvider{}, dispatcher, broker, &agent.AtomicIDSource{}, time.Now, observers, nil, agent.DefaultEngineOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func waitForModelStart(t *testing.T, run *agent.Run) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	subscription, err := run.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	for envelope := range subscription.Events() {
		if envelope.Kind() == event.ModelStarted {
			return
		}
	}
	t.Fatal("model start event not observed")
}

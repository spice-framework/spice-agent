package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

type gatedTool struct {
	started chan struct{}
	release chan struct{}
}

type hugePanicTool struct{}

type contractChangedTool struct {
	schema       string
	capabilities []tool.Capability
}

func (hugePanicTool) Definition() tool.Definition {
	definition, _ := tool.NewDefinition("read", "Read a fixture.", json.RawMessage(`{}`))
	return definition
}

func (hugePanicTool) Execute(context.Context, tool.Call, tool.Reporter) tool.Result {
	panic(strings.Repeat("x", event.MaximumPayloadBytes+1))
}

func (implementation contractChangedTool) Definition() tool.Definition {
	definition, _ := tool.NewDefinition("read", "Read a fixture.", json.RawMessage(implementation.schema), implementation.capabilities...)
	return definition
}

func (contractChangedTool) Execute(_ context.Context, call tool.Call, _ tool.Reporter) tool.Result {
	result, _ := tool.NewResult(call.ID(), json.RawMessage(`null`))
	return result
}

func (implementation *gatedTool) Definition() tool.Definition {
	definition, _ := tool.NewDefinition("read", "Read a fixture.", json.RawMessage(`{}`), tool.CapabilityFilesystemRead)
	return definition
}

func (implementation *gatedTool) Execute(ctx context.Context, call tool.Call, _ tool.Reporter) tool.Result {
	close(implementation.started)
	select {
	case <-ctx.Done():
		result, _ := tool.NewErrorResult(call.ID(), json.RawMessage(`null`), "cancelled")
		return result
	case <-implementation.release:
		result, _ := tool.NewResult(call.ID(), json.RawMessage(`"fixture"`))
		return result
	}
}

func TestSnapshotRoundTripAndResumePreservePlansAndSequences(t *testing.T) {
	call, _ := tool.NewCall("call-1", "read", json.RawMessage(`{}`))
	gate := &gatedTool{started: make(chan struct{}), release: make(chan struct{})}
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{
		{toolEvent(t, call), completed(t)},
		{delta(t, "resumed"), completed(t)},
	}}
	original := newEngine(t, provider, map[string]tool.Tool{"read": gate}, nil, nil)
	run := startRun(t, original, 3)
	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("tool did not start")
	}
	suspended := make(chan error, 1)
	go func() { suspended <- run.Suspend(t.Context()) }()
	waitForSuspendRequest(t, run)
	close(gate.release)
	if err := <-suspended; err != nil {
		t.Fatal(err)
	}
	snapshot, err := run.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version() != agent.SnapshotVersion || snapshot.Status() != agent.LifecycleSuspended || snapshot.CompletedTurns() != 1 || snapshot.LastSequence() == 0 || !containsPrefix(snapshot.StaticPlan(), "tool:read@") || snapshot.DynamicGeneration() != "none" {
		t.Fatalf("snapshot accessors mismatch: status=%s turns=%d sequence=%d", snapshot.Status(), snapshot.CompletedTurns(), snapshot.LastSequence())
	}
	if snapshot.Definition().Name() != "test" || len(snapshot.History()) == 0 {
		t.Fatal("snapshot definition or history accessor mismatch")
	}
	historyCopy := snapshot.History()
	historyCopy[0] = message.Message{}
	if snapshot.History()[0].Validate() != nil {
		t.Fatal("snapshot history accessor exposed mutable storage")
	}
	encoded, err := snapshot.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	again, _ := snapshot.MarshalBinary()
	if !bytes.Equal(encoded, again) {
		t.Fatal("snapshot encoding is nondeterministic")
	}
	decoded, err := agent.ParseSnapshot(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, mustSnapshotBytes(t, decoded)) {
		t.Fatal("snapshot round trip changed bytes")
	}
	if err = run.Resume(); err != nil {
		t.Fatal(err)
	}
	_ = collectAfter(t, run, snapshot.LastSequence())
	if err = run.Wait(t.Context()); err != nil {
		t.Fatalf("original local resume = %v", err)
	}
	if err = run.Resume(); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("second local resume = %v", err)
	}
	for _, changed := range []contractChangedTool{
		{schema: `{"type":"string"}`, capabilities: []tool.Capability{tool.CapabilityFilesystemRead}},
		{schema: `{}`, capabilities: []tool.Capability{tool.CapabilityFilesystemRead, tool.CapabilityNetworkAccess}},
	} {
		changedEngine := newEngine(t, blockingProvider{}, map[string]tool.Tool{"read": changed}, nil, nil)
		if _, err = changedEngine.ResumeSnapshot(t.Context(), decoded); err == nil || !strings.Contains(err.Error(), "static plan") {
			t.Fatalf("changed tool contract resume = %v", err)
		}
	}
	resumedProvider := &scriptedProvider{scripts: [][]model.StreamEvent{{delta(t, "resumed"), completed(t)}}}
	resumedEngine := newEngine(t, resumedProvider, map[string]tool.Tool{"read": testTool{}}, nil, nil)
	resumed, err := resumedEngine.ResumeSnapshot(t.Context(), decoded)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resumed.Subscribe(t.Context(), 0)
	var replayGap *event.OutOfRangeError
	if !errors.As(err, &replayGap) || replayGap.RecoveryAfter < decoded.LastSequence() {
		t.Fatalf("old resume cursor did not report snapshot gap: %#v, %v", replayGap, err)
	}
	events := collectAfter(t, resumed, decoded.LastSequence())
	if err = resumed.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Sequence() != decoded.LastSequence()+1 || events[0].Kind() != event.TurnStarted || events[len(events)-1].Kind() != event.RunCompleted {
		t.Fatalf("resumed events = %v", eventKinds(events))
	}
	for _, envelope := range events {
		if envelope.Kind() == event.RunStarted {
			t.Fatal("resumed run duplicated RunStarted")
		}
	}
	if len(resumedProvider.requests) != 1 || resumedProvider.requests[0].OperationID() != model.OperationID(decoded.RunID()+"/model/2") {
		t.Fatalf("resumed operation ID = %q", resumedProvider.requests[0].OperationID())
	}
}

func TestSnapshotPersistsSeenInteractionIDsAcrossResume(t *testing.T) {
	call, _ := tool.NewCall("call-1", "read", json.RawMessage(`{}`))
	gate := &gatedTool{started: make(chan struct{}), release: make(chan struct{})}
	response, _ := interaction.NewResponse("approval-1", json.RawMessage(`true`))
	broker := &scriptedBroker{response: response}
	original := newEngineWithBrokerAndTools(t, &scriptedProvider{scripts: [][]model.StreamEvent{{toolEvent(t, call), completed(t)}}}, broker, map[string]tool.Tool{"read": gate})
	run := startRun(t, original, 3)
	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("tool did not start")
	}
	request, _ := interaction.NewRequest("approval-1", "confirm", "Continue?", json.RawMessage(`{}`))
	if _, err := run.Interact(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	suspended := make(chan error, 1)
	go func() { suspended <- run.Suspend(t.Context()) }()
	waitForSuspendRequest(t, run)
	close(gate.release)
	if err := <-suspended; err != nil {
		t.Fatal(err)
	}
	snapshot, err := run.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snapshot.InteractionIDs(), []interaction.ID{"approval-1"}) {
		t.Fatalf("snapshot interaction IDs = %v", snapshot.InteractionIDs())
	}
	run.Cancel()
	_ = run.Wait(t.Context())
	resumedEngine := newEngineWithBrokerAndTools(t, blockingProvider{}, broker, map[string]tool.Tool{"read": testTool{}})
	resumed, err := resumedEngine.ResumeSnapshot(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = resumed.Interact(t.Context(), request); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("resumed interaction ID reuse = %v", err)
	}
	resumed.Cancel()
	_ = resumed.Wait(t.Context())
}

func waitForSuspendRequest(t *testing.T, run *agent.Run) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, err := run.ExportSnapshot()
		if failure, ok := errors.AsType[*agent.UnsafeSnapshotError](err); ok && failure.Status == agent.LifecycleStatus("suspending") {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("suspend request was not registered")
}

func TestSnapshotRejectsUnsafeCorruptedAndMismatchedState(t *testing.T) {
	definition, _ := agent.NewDefinition("test", "model", 3)
	history := []message.Message{inputMessage(t)}
	readDefinition := testTool{}.Definition()
	plan := []string{"broker:injected", "provider:injected", "stage:kernel", "tool:read@" + readDefinition.Fingerprint()}
	valid, err := agent.NewSnapshot("run-1", definition, 1, history, plan, "generation-1", 7, agent.LifecycleSuspended)
	if err != nil {
		t.Fatal(err)
	}
	encoded := mustSnapshotBytes(t, valid)
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"version", func(value map[string]any) { value["version"] = "future" }},
		{"active status", func(value map[string]any) { value["status"] = "running" }},
		{"zero sequence", func(value map[string]any) { value["last_sequence"] = float64(0) }},
		{"maximum sequence", func(value map[string]any) { value["last_sequence"] = float64(^uint64(0)) }},
		{"unsorted plan", func(value map[string]any) { value["static_plan"] = []any{"z", "a"} }},
		{"duplicate interactions", func(value map[string]any) { value["seen_interaction_ids"] = []any{"same", "same"} }},
		{"unsorted interactions", func(value map[string]any) { value["seen_interaction_ids"] = []any{"z", "a"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value map[string]any
			if decodeErr := json.Unmarshal(encoded, &value); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			test.mutate(value)
			corrupted, _ := json.Marshal(value)
			if _, parseErr := agent.ParseSnapshot(corrupted); parseErr == nil {
				t.Fatal("corrupted snapshot succeeded")
			}
		})
	}
	withUnknown := append([]byte(nil), encoded[:len(encoded)-1]...)
	withUnknown = append(withUnknown, []byte(`,"unknown":true}`)...)
	if _, err = agent.ParseSnapshot(withUnknown); err == nil {
		t.Fatal("unknown snapshot field succeeded")
	}
	if _, err = agent.ParseSnapshot(append(encoded, encoded...)); err == nil {
		t.Fatal("multiple snapshot values succeeded")
	}
	if _, err = agent.ParseSnapshot(bytes.Repeat([]byte("x"), agent.MaximumSnapshotBytes+1)); err == nil {
		t.Fatal("oversized snapshot succeeded")
	}
	callPart, _ := message.ToolCall("call", "read", json.RawMessage(`{}`))
	assistantID, _ := message.NewID("assistant")
	uncertain, _ := message.New(assistantID, message.RoleAssistant, callPart)
	if _, err = agent.NewSnapshot("run-1", definition, 1, append(history, uncertain), plan, "generation-1", 7, agent.LifecycleSuspended); err == nil {
		t.Fatal("uncertain tool mutation snapshot succeeded")
	}
	dispatcher, _ := stage.NewDispatcher(nil)
	options := agent.DefaultEngineOptions()
	options.DynamicGeneration = "different"
	engine, _ := agent.NewEngineWithOptions(&scriptedProvider{}, dispatcher, &agent.AtomicIDSource{}, time.Now, nil, nil, options)
	if _, err = engine.ResumeSnapshot(t.Context(), valid); err == nil || !strings.Contains(err.Error(), "static plan") {
		t.Fatalf("mismatched plan resume = %v", err)
	}
	dispatcher, _ = stage.NewDispatcher(map[string]tool.Tool{"read": testTool{}})
	engine, _ = agent.NewEngineWithOptions(&scriptedProvider{}, dispatcher, &agent.AtomicIDSource{}, time.Now, nil, nil, options)
	if _, err = engine.ResumeSnapshot(t.Context(), valid); err == nil || !strings.Contains(err.Error(), "dynamic generation") {
		t.Fatalf("mismatched generation resume = %v", err)
	}
}

func newEngineWithBrokerAndTools(t *testing.T, provider model.Provider, broker interaction.Broker, tools map[string]tool.Tool) *agent.Engine {
	t.Helper()
	dispatcher, err := stage.NewDispatcher(tools)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := agent.NewEngineWithInteractionBroker(provider, dispatcher, broker, &agent.AtomicIDSource{}, time.Now, nil, nil, agent.DefaultEngineOptions())
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func TestTerminalSnapshotCannotResumeAndActiveRunCannotExport(t *testing.T) {
	blocking := newEngine(t, blockingProvider{}, nil, nil, nil)
	active := startRun(t, blocking, 1)
	if _, err := active.ExportSnapshot(); err == nil {
		t.Fatal("active snapshot export succeeded")
	}
	active.Cancel()
	_ = active.Wait(t.Context())
	completedRun := startRun(t, newEngine(t, &scriptedProvider{scripts: [][]model.StreamEvent{{completed(t)}}}, nil, nil, nil), 1)
	_ = collect(t, completedRun)
	terminal, err := completedRun.ExportSnapshot()
	if err != nil || terminal.Status() != agent.LifecycleCompleted {
		t.Fatalf("terminal snapshot = %s, %v", terminal.Status(), err)
	}
	engine := newEngine(t, &scriptedProvider{}, nil, nil, nil)
	if _, err = engine.ResumeSnapshot(t.Context(), terminal); err == nil {
		t.Fatal("terminal snapshot resume succeeded")
	}
}

func TestSnapshotRejectsRunWhoseTerminalCouldNotCommit(t *testing.T) {
	call, _ := tool.NewCall("call", "read", json.RawMessage(`{}`))
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{{toolEvent(t, call), completed(t)}}}
	run := startRun(t, newEngine(t, provider, map[string]tool.Tool{"read": hugePanicTool{}}, nil, nil), 1)
	events := collect(t, run)
	if err := run.Wait(t.Context()); err == nil {
		t.Fatal("oversized terminal failure unexpectedly succeeded")
	}
	if countKinds(events)[event.RunFailed] != 0 {
		t.Fatal("precommit run terminal appeared in log")
	}
	if _, err := run.ExportSnapshot(); err == nil {
		t.Fatal("uncommitted terminal exported as safe snapshot")
	}
}

func TestGeneratedMessageIDRejectsHostileHistoryCollision(t *testing.T) {
	part, _ := message.Text("hello")
	id, _ := message.NewID("run-fixed/message/5")
	initial, _ := message.New(id, message.RoleUser, part)
	input, _ := agent.NewInput(initial)
	definition, _ := agent.NewDefinition("test", "model", 2)
	call, _ := tool.NewCall("call", "read", json.RawMessage(`{}`))
	provider := &scriptedProvider{scripts: [][]model.StreamEvent{{toolEvent(t, call), completed(t)}}}
	dispatcher, _ := stage.NewDispatcher(map[string]tool.Tool{"read": testTool{}})
	engine, _ := agent.NewEngine(provider, dispatcher, fixedIDSource{value: "run-fixed"}, time.Now, nil, nil)
	run, err := engine.Start(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, run)
	if err = run.Wait(t.Context()); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("collision run error = %v", err)
	}
	if countKinds(events)[event.ToolStarted] != 0 {
		t.Fatal("tool executed after generated message collision")
	}
}

func FuzzParseSnapshot(f *testing.F) {
	definition, _ := agent.NewDefinition("test", "model", 2)
	part, _ := message.Text("hello")
	id, _ := message.NewID("message")
	value, _ := message.New(id, message.RoleUser, part)
	snapshot, _ := agent.NewSnapshot("run", definition, 1, []message.Message{value}, []string{"provider:test"}, "none", 4, agent.LifecycleSuspended)
	encoded, _ := snapshot.MarshalBinary()
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		parsed, err := agent.ParseSnapshot(data)
		if err == nil && parsed.Validate() != nil {
			t.Fatal("parsed snapshot failed validation")
		}
	})
}

func mustSnapshotBytes(t *testing.T, snapshot agent.Snapshot) []byte {
	t.Helper()
	encoded, err := snapshot.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func collectAfter(t *testing.T, run *agent.Run, after uint64) []event.Envelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	subscription, err := run.Subscribe(ctx, after)
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

func eventKinds(events []event.Envelope) []event.Kind {
	result := make([]event.Kind, len(events))
	for index, envelope := range events {
		result[index] = envelope.Kind()
	}
	return result
}

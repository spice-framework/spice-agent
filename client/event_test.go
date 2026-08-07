package client

import (
	"strings"
	"testing"
	"time"
)

func TestEventDetailsRepresentEveryKernelPayloadShape(t *testing.T) {
	t.Parallel()
	run := mustRun(t, "run-events")
	now := time.Unix(10, 20).UTC()
	runStarted, _ := NewRunStartedDetail("coding")
	turn, _ := NewTurnDetail(1)
	modelStarted, _ := NewModelStartedDetail(1, "run-events/model/1")
	text, _ := NewTextDetail("hello")
	modelCompleted := NewModelCompletedDetail(NewUsage(3, 5))
	modelFailure, _ := NewModelFailure("provider_stream", "stream failed", true, false)
	modelFailed, _ := NewModelFailedDetail(modelFailure)
	toolStarted, _ := NewToolStartedDetail("call-1", "read")
	toolProgress, _ := NewToolProgressDetail("call-1", "half complete")
	completedTool, _ := NewToolTerminal("call-1", "read", "", "", "")
	toolCompleted, _ := NewToolTerminalDetail(completedTool)
	failedTool, _ := NewToolTerminal("call-2", "write", "process failed", ToolOutcomeUncertain, ToolRetryNever)
	toolFailed, _ := NewToolTerminalDetail(failedTool)
	interactionStarted, _ := NewInteractionStartedDetail("prompt-1", "confirmation")
	interactionCompleted, _ := NewInteractionTerminalDetail("prompt-1", "")
	interactionFailed, _ := NewInteractionTerminalDetail("prompt-2", "interaction did not complete")
	status, _ := NewStatusDetail("run failed")

	tests := []struct {
		kind     EventKind
		detail   EventDetail
		terminal bool
	}{
		{kind: EventRunStarted, detail: runStarted},
		{kind: EventRunCompleted, detail: NoEventDetail(), terminal: true},
		{kind: EventRunFailed, detail: status, terminal: true},
		{kind: EventTurnStarted, detail: turn},
		{kind: EventTurnCompleted, detail: turn, terminal: true},
		{kind: EventModelStarted, detail: modelStarted},
		{kind: EventModelDelta, detail: text},
		{kind: EventModelCompleted, detail: modelCompleted, terminal: true},
		{kind: EventModelFailed, detail: modelFailed, terminal: true},
		{kind: EventToolStarted, detail: toolStarted},
		{kind: EventToolProgress, detail: toolProgress},
		{kind: EventToolCompleted, detail: toolCompleted, terminal: true},
		{kind: EventToolFailed, detail: toolFailed, terminal: true},
		{kind: EventInteractionStarted, detail: interactionStarted},
		{kind: EventInteractionCompleted, detail: interactionCompleted, terminal: true},
		{kind: EventInteractionFailed, detail: interactionFailed, terminal: true},
	}
	for index, test := range tests {
		eventValue, err := NewEvent(run, uint64(index+1), now.Add(time.Duration(index)*time.Second), test.kind, test.detail)
		if err != nil {
			t.Fatalf("event %s: %v", test.kind, err)
		}
		if eventValue.Run() != run || eventValue.Sequence() != uint64(index+1) || eventValue.Kind() != test.kind ||
			eventValue.At() != now.Add(time.Duration(index)*time.Second) || eventValue.Detail().Kind() != test.detail.Kind() ||
			eventValue.Terminal() != test.terminal || eventValue.Cursor().AfterSequence() != eventValue.Sequence() {
			t.Fatalf("event %s = %#v", test.kind, eventValue)
		}
	}

	if value, ok := runStarted.Definition(); !ok || value != "coding" {
		t.Fatalf("run detail = %q, %t", value, ok)
	}
	if value, ok := turn.Turn(); !ok || value != 1 {
		t.Fatalf("turn detail = %d, %t", value, ok)
	}
	if gotTurn, operationID, ok := modelStarted.ModelStart(); !ok || gotTurn != 1 || operationID != "run-events/model/1" {
		t.Fatalf("model start = %d, %q, %t", gotTurn, operationID, ok)
	}
	if value, ok := text.Text(); !ok || value != "hello" {
		t.Fatalf("text detail = %q, %t", value, ok)
	}
	if usage, ok := modelCompleted.Usage(); !ok || usage.InputTokens() != 3 || usage.OutputTokens() != 5 {
		t.Fatalf("usage = %#v, %t", usage, ok)
	}
	if failure, ok := modelFailed.ModelFailure(); !ok || failure.Code() != "provider_stream" || failure.Message() != "stream failed" ||
		!failure.Retryable() || failure.BeforeStream() {
		t.Fatalf("model failure = %#v, %t", failure, ok)
	}
	if callID, name, ok := toolStarted.ToolStart(); !ok || callID != "call-1" || name != "read" {
		t.Fatalf("tool start = %q %q %t", callID, name, ok)
	}
	if callID, message, ok := toolProgress.ToolProgress(); !ok || callID != "call-1" || message != "half complete" {
		t.Fatalf("tool progress = %q %q %t", callID, message, ok)
	}
	if terminal, ok := toolFailed.ToolTerminal(); !ok || terminal.CallID() != "call-2" || terminal.Name() != "write" ||
		terminal.Problem() != "process failed" || terminal.Outcome() != ToolOutcomeUncertain || terminal.Retry() != ToolRetryNever {
		t.Fatalf("tool terminal = %#v, %t", terminal, ok)
	}
	if id, kind, ok := interactionStarted.InteractionStart(); !ok || id != "prompt-1" || kind != "confirmation" {
		t.Fatalf("interaction start = %q %q %t", id, kind, ok)
	}
	if id, problem, ok := interactionFailed.InteractionTerminal(); !ok || id != "prompt-2" || problem == "" {
		t.Fatalf("interaction terminal = %q %q %t", id, problem, ok)
	}
	if message, ok := status.Status(); !ok || message != "run failed" {
		t.Fatalf("status = %q %t", message, ok)
	}
}

func TestEventDetailsRejectImpossiblePayloads(t *testing.T) {
	t.Parallel()
	run := mustRun(t, "run-events")
	now := time.Now().UTC()
	text, _ := NewTextDetail("text")
	if _, err := NewEvent(run, 1, now, EventRunStarted, text); err == nil {
		t.Fatal("mismatched detail accepted")
	}
	if _, err := NewEvent(run, 0, now, EventModelDelta, text); err == nil {
		t.Fatal("zero event sequence accepted")
	}
	if _, err := NewEvent(run, 1, time.Time{}, EventModelDelta, text); err == nil {
		t.Fatal("zero event timestamp accepted")
	}
	if _, err := NewTextDetail(strings.Repeat("x", MaximumModelDeltaBytes)); err != nil {
		t.Fatalf("maximum model delta rejected: %v", err)
	}
	if _, err := NewTextDetail(strings.Repeat("x", MaximumModelDeltaBytes+1)); err == nil {
		t.Fatal("oversized event text accepted")
	}
	if _, err := NewModelStartedDetail(0, "operation"); err == nil {
		t.Fatal("zero model turn accepted")
	}
	if _, err := NewModelFailure("", "failure", false, false); err == nil {
		t.Fatal("empty model failure code accepted")
	}
	if _, err := NewModelFailure("provider", strings.Repeat("x", 4096), false, false); err != nil {
		t.Fatalf("maximum kernel model failure rejected: %v", err)
	}
	if _, err := NewModelFailure("provider", strings.Repeat("x", 4097), false, false); err == nil {
		t.Fatal("oversized model failure accepted")
	}
	if _, err := NewStatusDetail(strings.Repeat("x", MaximumTextBytes)); err != nil {
		t.Fatalf("maximum kernel terminal status rejected: %v", err)
	}
	if _, err := NewToolProgressDetail("call", ""); err == nil {
		t.Fatal("empty tool progress accepted")
	}
	if _, err := NewToolTerminal("call", "tool", "failure", ToolOutcomeUncertain, ToolRetryAllowed); err == nil {
		t.Fatal("retryable uncertain outcome accepted")
	}
	failed, _ := NewToolTerminal("call", "tool", "failure", "", "")
	failedDetail, _ := NewToolTerminalDetail(failed)
	if _, err := NewEvent(run, 1, now, EventToolCompleted, failedDetail); err == nil {
		t.Fatal("completed tool with problem accepted")
	}
	completed, _ := NewToolTerminal("call", "tool", "", "", "")
	completedDetail, _ := NewToolTerminalDetail(completed)
	if _, err := NewEvent(run, 1, now, EventToolFailed, completedDetail); err == nil {
		t.Fatal("failed tool without problem accepted")
	}
	completedInteraction, _ := NewInteractionTerminalDetail("id", "")
	if _, err := NewEvent(run, 1, now, EventInteractionFailed, completedInteraction); err == nil {
		t.Fatal("failed interaction without problem accepted")
	}
}

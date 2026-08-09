package agent_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/tool"
)

func TestToolOccurrenceLedgerClosesEveryStartedCallExactlyOnce(t *testing.T) {
	call, _ := tool.NewCall("ledger-call", "read", json.RawMessage(`{}`))
	unknown, _ := tool.NewCall("ledger-unknown", "unknown", json.RawMessage(`{}`))
	for _, test := range []struct {
		name      string
		newEngine func(*testing.T) *agent.Engine
		wantError bool
	}{
		{
			name: "completed",
			newEngine: func(t *testing.T) *agent.Engine {
				t.Helper()
				provider := &scriptedProvider{scripts: [][]model.StreamEvent{
					{toolEvent(t, call), completed(t)}, {completed(t)},
				}}
				return newEngine(t, provider, map[string]tool.Tool{"read": testTool{}}, nil, nil)
			},
		},
		{
			name: "model-visible failure",
			newEngine: func(t *testing.T) *agent.Engine {
				t.Helper()
				provider := &scriptedProvider{scripts: [][]model.StreamEvent{
					{toolEvent(t, call), completed(t)}, {completed(t)},
				}}
				return newEngine(t, provider, map[string]tool.Tool{"read": testTool{problem: "SECRET denied"}}, nil, nil)
			},
		},
		{
			name: "execution failure",
			newEngine: func(t *testing.T) *agent.Engine {
				t.Helper()
				failure, err := tool.NewExecutionError(
					call.ID(), tool.ExecutionDefinitive, tool.RetryAllowed, errors.New("SECRET host failure"),
				)
				if err != nil {
					t.Fatal(err)
				}
				provider := &scriptedProvider{scripts: [][]model.StreamEvent{{toolEvent(t, call), completed(t)}}}
				return newEngine(t, provider, map[string]tool.Tool{"read": testTool{executionErr: failure}}, nil, nil)
			},
			wantError: true,
		},
		{
			name: "unknown model tool",
			newEngine: func(t *testing.T) *agent.Engine {
				t.Helper()
				provider := &scriptedProvider{scripts: [][]model.StreamEvent{{toolEvent(t, unknown), completed(t)}}}
				return newEngine(t, provider, map[string]tool.Tool{"read": testTool{}}, nil, nil)
			},
			wantError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := startRun(t, test.newEngine(t), 2)
			events := collect(t, run)
			waitErr := run.Wait(t.Context())
			if (waitErr != nil) != test.wantError {
				t.Fatalf("run error = %v, want error %t", waitErr, test.wantError)
			}
			assertClosedToolOccurrenceLedger(t, events)
		})
	}
}

func assertClosedToolOccurrenceLedger(t *testing.T, events []event.Envelope) {
	t.Helper()
	open := make(map[tool.CallID]string)
	started := 0
	terminal := 0
	for _, envelope := range events {
		switch envelope.Kind() {
		case event.ToolStarted:
			occurrence, err := agent.DecodeToolStartedOccurrence(envelope.Data())
			if err != nil {
				t.Fatal(err)
			}
			if _, duplicate := open[occurrence.CallID()]; duplicate {
				t.Fatalf("duplicate open tool occurrence %q", occurrence.CallID())
			}
			open[occurrence.CallID()] = occurrence.Name()
			started++
		case event.ToolCompleted, event.ToolFailed:
			if bytes.Contains(envelope.Data(), []byte("SECRET")) {
				t.Fatalf("tool terminal leaked problem data = %s", envelope.Data())
			}
			occurrence, err := agent.DecodeToolTerminalOccurrence(envelope.Kind(), envelope.Data())
			if err != nil {
				t.Fatal(err)
			}
			name, exists := open[occurrence.CallID()]
			if !exists || name != occurrence.Name() {
				t.Fatalf("uncorrelated tool terminal = %#v, open=%v", occurrence, open)
			}
			delete(open, occurrence.CallID())
			terminal++
		}
	}
	if started == 0 || terminal != started || len(open) != 0 {
		t.Fatalf("tool occurrence ledger started=%d terminal=%d open=%v", started, terminal, open)
	}
}

package agent_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/tool"
)

func TestToolTerminalOccurrenceRoundTripsOnlyBoundedNonSecretFacts(t *testing.T) {
	for _, test := range []struct {
		name    string
		kind    event.Kind
		outcome tool.ExecutionState
		retry   tool.RetryDisposition
	}{
		{name: "completed", kind: event.ToolCompleted},
		{name: "failed without host metadata", kind: event.ToolFailed},
		{name: "definitive failure", kind: event.ToolFailed, outcome: tool.ExecutionDefinitive, retry: tool.RetryAllowed},
		{name: "uncertain failure", kind: event.ToolFailed, outcome: tool.ExecutionUncertain, retry: tool.RetryNever},
	} {
		t.Run(test.name, func(t *testing.T) {
			occurrence, err := agent.NewToolTerminalOccurrence(
				test.kind, "call-1", "read", test.outcome, test.retry,
			)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := occurrence.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if len(encoded) > agent.MaximumToolTerminalOccurrenceBytes || bytes.Contains(encoded, []byte("SECRET")) ||
				bytes.Contains(encoded, []byte("error")) || bytes.Contains(encoded, []byte("output")) ||
				bytes.Contains(encoded, []byte("result")) || bytes.Contains(encoded, []byte("path")) {
				t.Fatalf("unsafe terminal occurrence = %s", encoded)
			}
			decoded, err := agent.DecodeToolTerminalOccurrence(test.kind, encoded)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.CallID() != "call-1" || decoded.Name() != "read" || decoded.Kind() != test.kind ||
				decoded.ExecutionState() != test.outcome || decoded.RetryDisposition() != test.retry {
				t.Fatalf("decoded terminal occurrence = %#v", decoded)
			}
		})
	}
}

func TestToolTerminalOccurrenceRejectsCorruptionMissingUnknownDuplicateAndOversize(t *testing.T) {
	valid := validToolTerminalOccurrenceBytes(t, event.ToolFailed, tool.ExecutionDefinitive, tool.RetryAllowed)
	tests := map[string]json.RawMessage{
		"empty":           nil,
		"not object":      json.RawMessage(`[]`),
		"unknown":         bytes.Replace(valid, []byte(`"outcome"`), []byte(`"unknown":true,"outcome"`), 1),
		"duplicate":       bytes.Replace(valid, []byte(`"name":"read"`), []byte(`"name":"read","name":"write"`), 1),
		"missing":         bytes.Replace(valid, []byte(`"name":"read",`), nil, 1),
		"null":            bytes.Replace(valid, []byte(`"name":"read"`), []byte(`"name":null`), 1),
		"trailing":        append(append(json.RawMessage(nil), valid...), []byte(` {}`)...),
		"wrong version":   bytes.Replace(valid, []byte(agent.ToolTerminalOccurrenceVersion), []byte("unknown-version"), 1),
		"invalid kind":    bytes.Replace(valid, []byte(`"kind":"tool.failed"`), []byte(`"kind":"run.failed"`), 1),
		"invalid outcome": bytes.Replace(valid, []byte(`"outcome":"definitive"`), []byte(`"outcome":"maybe"`), 1),
		"invalid retry":   bytes.Replace(valid, []byte(`"retry":"allowed"`), []byte(`"retry":"maybe"`), 1),
		"oversize":        bytes.Repeat([]byte{'x'}, agent.MaximumToolTerminalOccurrenceBytes+1),
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := agent.DecodeToolTerminalOccurrence(event.ToolFailed, payload); err == nil ||
				strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("decode terminal occurrence = %v", err)
			}
		})
	}
	if _, err := agent.DecodeToolTerminalOccurrence(event.ToolCompleted, valid); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Fatalf("terminal event kind mismatch = %v", err)
	}
	if _, err := agent.DecodeToolTerminalOccurrence(event.RunFailed, valid); err == nil ||
		!strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported terminal event kind = %v", err)
	}
}

func TestToolTerminalOccurrenceRejectsInvalidCorrelationAndFailureMetadata(t *testing.T) {
	for _, test := range []struct {
		name    string
		kind    event.Kind
		callID  tool.CallID
		tool    string
		outcome tool.ExecutionState
		retry   tool.RetryDisposition
	}{
		{name: "unsupported kind", kind: event.RunCompleted, callID: "call", tool: "read"},
		{name: "missing call", kind: event.ToolFailed, tool: "read"},
		{name: "missing name", kind: event.ToolFailed, callID: "call"},
		{name: "completed metadata", kind: event.ToolCompleted, callID: "call", tool: "read", outcome: tool.ExecutionDefinitive, retry: tool.RetryNever},
		{name: "outcome only", kind: event.ToolFailed, callID: "call", tool: "read", outcome: tool.ExecutionDefinitive},
		{name: "retry only", kind: event.ToolFailed, callID: "call", tool: "read", retry: tool.RetryNever},
		{name: "uncertain retry", kind: event.ToolFailed, callID: "call", tool: "read", outcome: tool.ExecutionUncertain, retry: tool.RetryAllowed},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := agent.NewToolTerminalOccurrence(test.kind, test.callID, test.tool, test.outcome, test.retry); err == nil {
				t.Fatal("invalid terminal occurrence succeeded")
			}
		})
	}
}

func TestToolTerminalOccurrenceIsImmutableUnderConcurrentEncoding(t *testing.T) {
	occurrence, err := agent.NewToolTerminalOccurrence(
		event.ToolFailed, "call", "read", tool.ExecutionUncertain, tool.RetryNever,
	)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 32
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			encoded, encodeErr := occurrence.Encode()
			if encodeErr != nil {
				t.Error(encodeErr)
				return
			}
			decoded, decodeErr := agent.DecodeToolTerminalOccurrence(event.ToolFailed, encoded)
			if decodeErr != nil || decoded.CallID() != occurrence.CallID() || decoded.Name() != occurrence.Name() {
				t.Errorf("concurrent terminal occurrence = %#v, %v", decoded, decodeErr)
			}
		}()
	}
	wait.Wait()
}

func FuzzDecodeToolTerminalOccurrence(f *testing.F) {
	f.Add(uint8(0), []byte(validToolTerminalOccurrenceBytes(f, event.ToolCompleted, "", "")))
	f.Add(uint8(1), []byte(validToolTerminalOccurrenceBytes(f, event.ToolFailed, tool.ExecutionUncertain, tool.RetryNever)))
	f.Add(uint8(2), []byte(`{"version":"unknown"}`))
	f.Fuzz(func(t *testing.T, selector uint8, payload []byte) {
		kind := event.RunFailed
		switch selector % 3 {
		case 0:
			kind = event.ToolCompleted
		case 1:
			kind = event.ToolFailed
		}
		occurrence, err := agent.DecodeToolTerminalOccurrence(kind, payload)
		if err != nil {
			return
		}
		encoded, err := occurrence.Encode()
		if err != nil || len(encoded) > agent.MaximumToolTerminalOccurrenceBytes {
			t.Fatalf("re-encode terminal occurrence = %q, %v", encoded, err)
		}
		decoded, err := agent.DecodeToolTerminalOccurrence(kind, encoded)
		if err != nil || decoded.CallID() != occurrence.CallID() || decoded.Name() != occurrence.Name() ||
			decoded.Kind() != occurrence.Kind() || decoded.ExecutionState() != occurrence.ExecutionState() ||
			decoded.RetryDisposition() != occurrence.RetryDisposition() {
			t.Fatalf("terminal occurrence correlation changed = %#v, %v", decoded, err)
		}
	})
}

func validToolTerminalOccurrenceBytes(
	t testOrFuzz,
	kind event.Kind,
	outcome tool.ExecutionState,
	retry tool.RetryDisposition,
) json.RawMessage {
	t.Helper()
	occurrence, err := agent.NewToolTerminalOccurrence(kind, "call", "read", outcome, retry)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := occurrence.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

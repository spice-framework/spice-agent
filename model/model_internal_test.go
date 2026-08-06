package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/tool"
)

func TestInternalStreamUnionRejectsInactiveAndCorruptedMembers(t *testing.T) {
	call, _ := tool.NewCall("call", "read", json.RawMessage(`{}`))
	problem, _ := NewProblem("failed", "failed safely", false)
	for _, value := range []StreamEvent{
		{kind: EventTextDelta, text: "x", call: call},
		{kind: EventToolCall, call: call, text: "x"},
		{kind: EventCompleted, problem: problem},
		{kind: EventFailed, problem: problem, usage: NewUsage(1, 1)},
		{kind: EventKind("unknown")},
	} {
		if value.Validate() == nil {
			t.Fatalf("corrupted union %q succeeded", value.kind)
		}
	}
	if _, ok := (StreamEvent{}).Metadata(); ok {
		t.Fatal("inactive metadata accessor succeeded")
	}
	if _, ok := (StreamEvent{}).Problem(); ok {
		t.Fatal("inactive problem accessor succeeded")
	}
}

func TestInternalMetadataAndRequestBounds(t *testing.T) {
	if _, err := NewMetadata("x", json.RawMessage(`"`+strings.Repeat("x", maxMetadataItemBytes)+`"`)); err == nil {
		t.Fatal("oversized metadata succeeded")
	}
	values := make([]Metadata, maxMetadataItems+1)
	if _, err := cloneMetadata(values); err == nil {
		t.Fatal("excess metadata count succeeded")
	}
	if _, err := NewProblem("code", strings.Repeat("x", 4097), false); err == nil {
		t.Fatal("oversized problem succeeded")
	}
	if _, err := TextDelta(strings.Repeat("x", MaximumTextDeltaBytes+1)); err == nil {
		t.Fatal("oversized delta succeeded")
	}
	if _, err := NewRequest("operation", "model", nil, nil); err == nil {
		t.Fatal("empty request succeeded")
	}
	if _, err := NewRequest("operation", "model", make([]message.Message, maxRequestMessages+1), nil); err == nil {
		t.Fatal("excess request messages succeeded")
	}
}

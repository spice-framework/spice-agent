package model_test

import (
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/tool"
)

func TestRequestIsValidatedAndDefensive(t *testing.T) {
	part, _ := message.Text("hello")
	id, _ := message.NewID("m1")
	msg, _ := message.New(id, message.RoleUser, part)
	definition, _ := tool.NewDefinition("read", "Read", json.RawMessage(`{}`), tool.CapabilityFilesystemRead)
	request, err := model.NewRequest("gpt", []message.Message{msg}, []tool.Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	if request.Model() != "gpt" || len(request.Messages()) != 1 || len(request.Tools()) != 1 {
		t.Fatal("request accessors mismatch")
	}
	tools := request.Tools()
	tools[0] = tool.Definition{}
	if request.Tools()[0].Name() != "read" {
		t.Fatal("request tools were mutable")
	}
	if _, err = model.NewRequest("", nil, nil); err == nil {
		t.Fatal("empty request succeeded")
	}
	if _, err = model.NewRequest("gpt", []message.Message{msg}, []tool.Definition{definition, definition}); err == nil {
		t.Fatal("duplicate tool succeeded")
	}
}

func TestStreamEventValidation(t *testing.T) {
	call := tool.Call{ID: "c1", Name: "read", Arguments: json.RawMessage(`{}`)}
	valid := []model.StreamEvent{
		{Kind: model.EventTextDelta, Text: "x"},
		{Kind: model.EventToolCall, Call: call},
		{Kind: model.EventCompleted, Usage: model.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}},
		{Kind: model.EventFailed, Problem: model.Problem{Code: "rate_limit", Message: "slow down", Retryable: true, BeforeStream: true}},
	}
	for _, event := range valid {
		if err := event.Validate(); err != nil {
			t.Fatalf("%s: %v", event.Kind, err)
		}
	}
	invalid := []model.StreamEvent{
		{}, {Kind: model.EventTextDelta}, {Kind: model.EventToolCall},
		{Kind: model.EventCompleted, Text: "x"},
		{Kind: model.EventCompleted, Usage: model.Usage{InputTokens: 1, TotalTokens: 2}},
		{Kind: model.EventFailed, Problem: model.Problem{Code: " bad", Message: "x"}},
	}
	for _, event := range invalid {
		if err := event.Validate(); err == nil {
			t.Fatalf("invalid event %+v succeeded", event)
		}
	}
	if err := model.RequireCompletion(model.StreamEvent{}, io.EOF, false); err == nil {
		t.Fatal("premature EOF succeeded")
	}
	want := errors.New("failure")
	if got := model.RequireCompletion(model.StreamEvent{}, want, false); !errors.Is(got, want) {
		t.Fatalf("error = %v", got)
	}
}

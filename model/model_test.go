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

func TestRequestIsImmutableAndBounded(t *testing.T) {
	part, err := message.Text("hello")
	if err != nil {
		t.Fatal(err)
	}
	id, err := message.NewID("m1")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := message.New(id, message.RoleUser, part)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := tool.NewDefinition("read", "Read", json.RawMessage(`{}`), tool.CapabilityFilesystemRead)
	if err != nil {
		t.Fatal(err)
	}
	request, err := model.NewRequest("op-1", "gpt", []message.Message{msg}, []tool.Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	if request.OperationID() != "op-1" || request.Model() != "gpt" || len(request.Messages()) != 1 || len(request.Tools()) != 1 {
		t.Fatal("request accessors mismatch")
	}
	tools := request.Tools()
	tools[0] = tool.Definition{}
	if request.Tools()[0].Name() != "read" {
		t.Fatal("request tools were mutable")
	}
	if _, err = model.NewRequest("", "gpt", []message.Message{msg}, nil); err == nil {
		t.Fatal("empty operation succeeded")
	}
	if _, err = model.NewRequest("op", "gpt", []message.Message{msg}, []tool.Definition{definition, definition}); err == nil {
		t.Fatal("duplicate tool succeeded")
	}
}

func TestStrictStreamEventConstructors(t *testing.T) {
	call, _ := tool.NewCall("c1", "read", json.RawMessage(`{}`))
	delta, err := model.TextDelta("x")
	if err != nil {
		t.Fatal(err)
	}
	callEvent, err := model.ToolCallEvent(call)
	if err != nil {
		t.Fatal(err)
	}
	usage := model.NewUsage(2, 3)
	completed, err := model.Completed(usage)
	if err != nil {
		t.Fatal(err)
	}
	problem, err := model.NewProblem("rate_limit", "slow down", true)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := model.Failed(problem)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []model.StreamEvent{delta, callEvent, completed, failed} {
		if err := item.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if text, ok := delta.Text(); !ok || text != "x" {
		t.Fatal("delta accessor")
	}
	if got, ok := callEvent.Call(); !ok || got.ID() != call.ID() {
		t.Fatal("call accessor")
	}
	if got, ok := completed.Usage(); !ok || got.TotalTokens() != 5 {
		t.Fatal("usage accessor")
	}
	if got, ok := failed.Problem(); !ok || got.Code() != "rate_limit" || !got.Retryable() {
		t.Fatal("problem accessor")
	}
	if (model.StreamEvent{}).Validate() == nil {
		t.Fatal("zero event succeeded")
	}
	if _, err := model.TextDelta(""); err == nil {
		t.Fatal("empty delta succeeded")
	}
}

func TestMetadataIsNamespacedBoundedAndDefensive(t *testing.T) {
	raw := json.RawMessage(`{"response_id":"resp-1"}`)
	metadata, err := model.NewMetadata("openai.responses", raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[2] = 'X'
	if !json.Valid(metadata.Value()) {
		t.Fatal("metadata retained caller storage")
	}
	value := metadata.Value()
	value[2] = 'X'
	if !json.Valid(metadata.Value()) {
		t.Fatal("metadata accessor exposed storage")
	}
	problem, err := model.NewProblem("busy", "try later", true, metadata)
	if err != nil || len(problem.Metadata()) != 1 {
		t.Fatalf("problem metadata: %v", err)
	}
	completed, err := model.Completed(model.NewUsage(1, 2), metadata)
	if err != nil {
		t.Fatal(err)
	}
	values, ok := completed.Metadata()
	if !ok || len(values) != 1 || values[0].Namespace() != "openai.responses" {
		t.Fatal("completed metadata mismatch")
	}
	if _, err = model.NewMetadata("openai.responses", json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid JSON metadata succeeded")
	}
	if _, err = model.NewProblem("busy", "try later", true, metadata, metadata); err == nil {
		t.Fatal("duplicate metadata namespace succeeded")
	}
}

func TestTypedFailuresPreserveHostObservedRetryState(t *testing.T) {
	problem, _ := model.NewProblem("unavailable", "try later", true)
	providerErr, err := model.NewProviderError(problem, io.ErrUnexpectedEOF)
	if err != nil || providerErr.Error() != "unavailable: try later" || !errors.Is(providerErr, io.ErrUnexpectedEOF) || providerErr.Problem().Code() != "unavailable" {
		t.Fatalf("provider error: %v", err)
	}
	before, err := model.NewOperationError(problem, false, providerErr)
	if err != nil || !before.BeforeStream() || !before.Retryable() {
		t.Fatalf("before stream: %v", err)
	}
	after, err := model.NewOperationError(problem, true, providerErr)
	if err != nil || after.Error() != "unavailable: try later" || !errors.Is(after, providerErr) || after.Problem().Message() != "try later" || after.BeforeStream() || after.Retryable() {
		t.Fatalf("after stream: %v", err)
	}
	if _, constructionErr := model.NewProviderError(model.Problem{}, nil); constructionErr == nil {
		t.Fatal("zero provider problem succeeded")
	}
	if _, constructionErr := model.NewOperationError(model.Problem{}, false, nil); constructionErr == nil {
		t.Fatal("zero operation problem succeeded")
	}
	streamErr, err := model.NewStreamError(problem, io.ErrUnexpectedEOF)
	if err != nil || streamErr.Error() != "unavailable: try later" || !errors.Is(streamErr, io.ErrUnexpectedEOF) || streamErr.Problem().Code() != "unavailable" {
		t.Fatalf("stream error: %v", err)
	}
	if _, constructionErr := model.NewStreamError(model.Problem{}, nil); constructionErr == nil {
		t.Fatal("zero stream problem succeeded")
	}
	if err := model.RequireCompletion(io.EOF, false); err == nil {
		t.Fatal("premature EOF succeeded")
	}
	if err := model.RequireCompletion(io.EOF, true); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal EOF = %v", err)
	}
	if model.NewUsage(2, 3).InputTokens() != 2 || model.NewUsage(2, 3).OutputTokens() != 3 {
		t.Fatal("usage accessors mismatch")
	}
	if (&model.ProviderError{}).Error() != ": " || (*model.ProviderError)(nil).Error() == "" || (*model.StreamError)(nil).Error() == "" || (*model.OperationError)(nil).Error() == "" {
		t.Fatal("typed error nil behavior mismatch")
	}
	if (*model.ProviderError)(nil).Unwrap() != nil || (*model.StreamError)(nil).Unwrap() != nil || (*model.OperationError)(nil).Unwrap() != nil {
		t.Fatal("typed nil unwrap mismatch")
	}
	if (*model.ProviderError)(nil).Problem().Code() != "" || (*model.StreamError)(nil).Problem().Code() != "" || (*model.OperationError)(nil).Problem().Code() != "" {
		t.Fatal("typed nil problem mismatch")
	}
}

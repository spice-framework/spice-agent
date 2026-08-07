package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestStructuredValuesPreserveArbitraryJSONAndTypedScalars(t *testing.T) {
	t.Parallel()
	objectBytes := []byte(`{"type":"object","properties":{"approved":{"type":"boolean"}}}`)
	object, err := ParseStructuredValue(objectBytes)
	if err != nil || object.Kind() != StructuredObject {
		t.Fatalf("object = %#v, err=%v", object, err)
	}
	objectBytes[0] = '['
	first, err := object.EncodeTransfer()
	if err != nil {
		t.Fatal(err)
	}
	first[0] = '['
	second, err := object.EncodeTransfer()
	if err != nil || !bytes.Equal(second, []byte(`{"type":"object","properties":{"approved":{"type":"boolean"}}}`)) {
		t.Fatalf("object exposed bytes: %q, err=%v", second, err)
	}
	text, err := NewStructuredText("answer")
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := text.Text(); !ok || value != "answer" {
		t.Fatalf("structured text = %q, %t", value, ok)
	}
	boolean := NewStructuredBool(true)
	booleanBytes, _ := boolean.EncodeTransfer()
	if value, ok := boolean.Bool(); !ok || !value || !bytes.Equal(booleanBytes, []byte("true")) {
		t.Fatalf("structured bool = %t, %t", value, ok)
	}
	nullValue := NewStructuredNull()
	nullBytes, _ := nullValue.EncodeTransfer()
	if !nullValue.IsNull() || !bytes.Equal(nullBytes, []byte("null")) {
		t.Fatalf("structured null = %#v", nullValue)
	}
	secretObject, err := ParseStructuredValue([]byte(`{"approved":true,"token":"secret-value"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []StructuredValue{nullValue, boolean, secretObject} {
		response, responseErr := NewInteractionResponse("prompt", candidate)
		responseBytes, encodeErr := response.Value().EncodeTransfer()
		candidateBytes, candidateErr := candidate.EncodeTransfer()
		if responseErr != nil || encodeErr != nil || candidateErr != nil || !bytes.Equal(responseBytes, candidateBytes) {
			t.Fatalf("structured response round trip failed for %s: %v", candidate.Kind(), responseErr)
		}
	}
	for encoded, kind := range map[string]StructuredKind{
		"42": StructuredNumber, "[]": StructuredArray, "false": StructuredBool, `"x"`: StructuredText,
	} {
		value, parseErr := ParseStructuredValue([]byte(encoded))
		if parseErr != nil || value.Kind() != kind {
			t.Fatalf("parse %q = %#v, err=%v", encoded, value, parseErr)
		}
	}
	if _, err := ParseStructuredValue(nil); err == nil {
		t.Fatal("empty JSON accepted")
	}
	if _, err := ParseStructuredValue([]byte("{")); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if err := (StructuredValue{kind: StructuredText, encoded: []byte("true")}).Validate(); err == nil {
		t.Fatal("corrupt structured kind accepted")
	}
}

func TestStructuredValueRedactsEveryDefaultDiagnosticSurface(t *testing.T) {
	t.Parallel()
	const canary = "structured-secret-canary"
	secret, err := ParseStructuredValue([]byte(`{"token":"structured-secret-canary"}`))
	if err != nil {
		t.Fatal(err)
	}
	type nested struct {
		Value StructuredValue `json:"value"`
	}
	outputs := []string{
		fmt.Sprintf("%v", secret),
		fmt.Sprintf("%+v", secret),
		fmt.Sprintf("%#v", secret),
		fmt.Sprintf("%v", nested{Value: secret}),
		fmt.Sprintf("%+v", nested{Value: secret}),
		fmt.Sprintf("%#v", nested{Value: secret}),
		fmt.Errorf("diagnostic: %v", secret).Error(),
	}
	directJSON, err := marshalUnknownJSON(secret)
	if err != nil {
		t.Fatal(err)
	}
	nestedJSON, err := json.Marshal(nested{Value: secret})
	if err != nil {
		t.Fatal(err)
	}
	outputs = append(outputs, string(directJSON), string(nestedJSON))
	var log bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&log, nil))
	logger.Info("canary", "direct", secret, "nested", nested{Value: secret})
	outputs = append(outputs, log.String())
	for index, output := range outputs {
		if strings.Contains(output, canary) {
			t.Fatalf("diagnostic surface %d leaked structured application data", index)
		}
	}
	encoded, err := secret.EncodeTransfer()
	if err != nil || !strings.Contains(string(encoded), canary) {
		t.Fatalf("explicit transfer did not preserve exact application data: %v", err)
	}
}

// marshalUnknownJSON models a generic adapter that receives an application
// value without knowing its concrete type.
func marshalUnknownJSON(value any) ([]byte, error) { return json.Marshal(value) }

func TestInteractionUpdatesAreTypedCanonicalAndImmutable(t *testing.T) {
	t.Parallel()
	limits := testLimits(t)
	schema, _ := ParseStructuredValue([]byte(`{"type":"boolean"}`))
	runA := mustRun(t, "run-a")
	runB := mustRun(t, "run-b")
	first, _ := NewPendingInteraction(runB, "prompt-2", "text", "Second prompt", schema)
	second, _ := NewPendingInteraction(runA, "prompt-1", "confirmation", "Proceed?", schema)
	input := []PendingInteraction{first, second}
	snapshot, err := NewInteractionSnapshot(7, input, limits)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = PendingInteraction{}
	values, ok := snapshot.Snapshot()
	if !ok || snapshot.Revision() != 7 || values[0].Run() != runA || values[1].Run() != runB {
		t.Fatalf("snapshot = %#v, %v", snapshot, values)
	}
	values[0] = PendingInteraction{}
	again, ok := snapshot.Snapshot()
	if !ok || again[0].ID() != "prompt-1" || again[0].Schema().Kind() != StructuredObject {
		t.Fatal("interaction snapshot exposed its storage")
	}
	opened, err := NewInteractionChange(InteractionOpened, 8, second)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := opened.Item()
	if !ok || item.ID() != "prompt-1" || item.Prompt() != "Proceed?" {
		t.Fatalf("opened item = %#v", item)
	}
	responseValue := NewStructuredBool(true)
	response, err := NewInteractionResponse("prompt-1", responseValue)
	if err != nil || response.Value().Kind() != StructuredBool {
		t.Fatalf("response = %#v, err=%v", response, err)
	}
	request, err := NewRespondRequest(runA, mustOperation(t, "respond-1"), response)
	if err != nil || request.Run() != runA || request.Operation().String() != "respond-1" || request.Response().ID() != "prompt-1" {
		t.Fatalf("respond request = %#v, err=%v", request, err)
	}
	accepted, err := NewRespondResult(true, false)
	if err != nil || !accepted.Accepted() || accepted.DuplicateOperation() {
		t.Fatalf("respond result = %#v, err=%v", accepted, err)
	}
	options := NewInteractionStreamOptions(true)
	if !options.Tail() {
		t.Fatalf("interaction options = %#v", options)
	}
	if err := options.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestInteractionValuesRejectInvalidBoundaries(t *testing.T) {
	t.Parallel()
	limits := testLimits(t)
	run := mustRun(t, "run")
	schema := NewStructuredNull()
	valid, _ := NewPendingInteraction(run, "prompt", "text", "Prompt", schema)
	if _, err := NewPendingInteraction(run, "prompt", "text", " Prompt", schema); err == nil {
		t.Fatal("untrimmed prompt accepted")
	}
	if _, err := NewPendingInteraction(run, "prompt", "text", "Prompt", StructuredValue{}); err == nil {
		t.Fatal("zero schema accepted")
	}
	if _, err := NewInteractionSnapshot(0, []PendingInteraction{valid, valid}, limits); err == nil {
		t.Fatal("duplicate pending interaction accepted")
	}
	smallLimits, _ := NewLimits(1024, 1, 1, 1024, 1, 1)
	other, _ := NewPendingInteraction(mustRun(t, "other"), "prompt", "text", "Other", schema)
	if _, err := NewInteractionSnapshot(0, []PendingInteraction{valid, other}, smallLimits); err == nil {
		t.Fatal("negotiated interaction count exceeded")
	}
	if _, err := NewInteractionChange(InteractionSnapshot, 1, valid); err == nil {
		t.Fatal("snapshot accepted as change")
	}
	if _, err := NewInteractionResponse("prompt", StructuredValue{}); err == nil {
		t.Fatal("zero structured response accepted")
	}
	if _, err := NewRespondResult(false, false); err == nil {
		t.Fatal("empty response outcome accepted")
	}
}

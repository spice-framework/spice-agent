package client

import (
	"slices"
	"strings"
	"testing"
)

func TestInitializeAndRunValues(t *testing.T) {
	t.Parallel()
	minimum, _ := NewProtocolVersion(1, 0, 0)
	maximum, _ := NewProtocolVersion(1, 1, 0)
	protocol, err := NewProtocolRange(minimum, maximum)
	if err != nil {
		t.Fatal(err)
	}
	supported := []string{"snapshot", "events"}
	required := []string{"events"}
	request, err := NewInitializeRequest(protocol, testBuild(t), supported, required, testLimits(t))
	if err != nil {
		t.Fatal(err)
	}
	supported[0], required[0] = "mutated", "mutated"
	if !slices.Equal(request.SupportedCapabilities(), []string{"events", "snapshot"}) ||
		!slices.Equal(request.RequiredCapabilities(), []string{"events"}) {
		t.Fatalf("initialize capabilities mutated: %v %v", request.SupportedCapabilities(), request.RequiredCapabilities())
	}
	if _, ok := request.Reconnect(); ok {
		t.Fatal("fresh request unexpectedly has reconnect claim")
	}
	if request.Protocol() != protocol || request.Client().Component() != "client" || request.RequestedLimits() != testLimits(t) {
		t.Fatalf("initialize accessors inconsistent: %#v", request)
	}
	if validationErr := request.Validate(); validationErr != nil {
		t.Fatal(validationErr)
	}

	claim, err := NewReconnectClaim("client-1", 5)
	if err != nil {
		t.Fatal(err)
	}
	reconnect, err := NewReconnectRequest(protocol, testBuild(t), []string{"events"}, nil, testLimits(t), claim)
	if err != nil {
		t.Fatal(err)
	}
	gotClaim, ok := reconnect.Reconnect()
	if !ok || gotClaim.ClientID() != "client-1" || gotClaim.ExpectedEpoch() != 5 || gotClaim.NextOwnershipEpoch() != 6 {
		t.Fatalf("reconnect claim = %#v, %t", gotClaim, ok)
	}

	operation := mustOperation(t, "operation-1")
	if operation.String() != "operation-1" {
		t.Fatalf("operation ID = %q", operation.String())
	}
	definition := mustDefinitionRef(t, "coding", "v1")
	input, err := NewInput("message-1", "inspect the workspace")
	if err != nil {
		t.Fatal(err)
	}
	startRequest, err := NewStartRequest(operation, definition, input)
	if err != nil {
		t.Fatal(err)
	}
	if validationErr := startRequest.Validate(); validationErr != nil {
		t.Fatal(validationErr)
	}
	if startRequest.Operation() != operation || startRequest.Definition() != definition || startRequest.Input().Text() != input.Text() {
		t.Fatalf("start request = %#v", startRequest)
	}
	if input.MessageID() != "message-1" || definition.ID() != "coding" {
		t.Fatalf("input or definition ID = %#v %#v", input, definition)
	}
	run := mustRun(t, "run-1")
	start, err := NewStartResult(run, 1, "plan-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if start.Run() != run || start.InitialSequence() != 1 || start.PlanID() != "plan-1" || start.DuplicateOperation() {
		t.Fatalf("start result = %#v", start)
	}
	cursor, err := NewCursor(run, 7)
	if err != nil || cursor.Run() != run || cursor.AfterSequence() != 7 || cursor.Validate() != nil {
		t.Fatalf("cursor = %#v, err=%v", cursor, err)
	}
	streamOptions, err := NewEventStreamOptions(32, true, testLimits(t))
	if err != nil || streamOptions.ReplayLimit() != 32 || !streamOptions.Tail() {
		t.Fatalf("stream options = %#v, err=%v", streamOptions, err)
	}
	if validationErr := streamOptions.Validate(testLimits(t)); validationErr != nil {
		t.Fatal(validationErr)
	}

	cancel, err := NewCancelRequest(run, operation, "user request")
	if err != nil || cancel.Run() != run || cancel.Operation() != operation || cancel.Reason() != "user request" {
		t.Fatalf("cancel request = %#v, err=%v", cancel, err)
	}
	mutation, err := NewRunMutation(run, operation)
	if err != nil || mutation.Run() != run || mutation.Operation() != operation {
		t.Fatalf("run mutation = %#v, err=%v", mutation, err)
	}
	snapshot := mustSnapshot(t, []byte("signed snapshot"))
	importRequest, err := NewImportRequest(operation, snapshot)
	if err != nil || importRequest.Operation() != operation || importRequest.Snapshot().SizeBytes() != len("signed snapshot") {
		t.Fatalf("import request = %#v, err=%v", importRequest, err)
	}
}

func TestRunResultsEncodeExactOutcomes(t *testing.T) {
	t.Parallel()
	run := mustRun(t, "run-result")
	cancelRequested, err := NewCancelResult(true, false, 0)
	if err != nil || !cancelRequested.Requested() || cancelRequested.AlreadyTerminal() || cancelRequested.TerminalSequence() != 0 {
		t.Fatalf("cancel requested = %#v, err=%v", cancelRequested, err)
	}
	terminal, err := NewCancelResult(false, true, 9)
	if err != nil || terminal.Requested() || !terminal.AlreadyTerminal() || terminal.TerminalSequence() != 9 {
		t.Fatalf("cancel terminal = %#v, err=%v", terminal, err)
	}
	suspended, err := NewSuspendResult(true, 10, true)
	if err != nil || !suspended.Suspended() || suspended.BoundarySequence() != 10 || !suspended.DuplicateOperation() {
		t.Fatalf("suspend result = %#v, err=%v", suspended, err)
	}
	resumed, err := NewResumeResult(true, 11, false)
	if err != nil || !resumed.Resumed() || resumed.NextSequence() != 11 || resumed.DuplicateOperation() {
		t.Fatalf("resume result = %#v, err=%v", resumed, err)
	}
	imported, err := NewImportResult(run, 12, true)
	if err != nil || imported.Run() != run || imported.NextSequence() != 12 || !imported.DuplicateOperation() {
		t.Fatalf("import result = %#v, err=%v", imported, err)
	}
}

func TestInitializeAndRunValuesRejectInvalidInputs(t *testing.T) {
	t.Parallel()
	version, _ := NewProtocolVersion(1, 0, 0)
	protocol, _ := NewProtocolRange(version, version)
	limits := testLimits(t)
	build := testBuild(t)
	if _, err := NewInitializeRequest(protocol, build, []string{"events"}, []string{"snapshots"}, limits); err == nil {
		t.Fatal("unsupported required capability accepted")
	}
	if _, err := NewReconnectClaim("client", 0); err == nil {
		t.Fatal("zero reconnect epoch accepted")
	}
	if _, err := NewReconnectClaim("client", ^uint64(0)); err == nil {
		t.Fatal("unincrementable reconnect epoch accepted")
	}
	if _, err := NewOperationID(""); err == nil {
		t.Fatal("empty operation ID accepted")
	}
	if _, err := NewRunRef(" run"); err == nil {
		t.Fatal("untrimmed run ID accepted")
	}
	if _, err := NewInput("message", ""); err == nil {
		t.Fatal("empty input accepted")
	}
	if _, err := NewInput("message", " \n\t "); err == nil {
		t.Fatal("whitespace-only input accepted")
	}
	if _, err := NewInput("message", strings.Repeat("x", MaximumTextBytes+1)); err == nil {
		t.Fatal("oversized input accepted")
	}
	run := mustRun(t, "run")
	operation := mustOperation(t, "operation")
	if _, err := NewStartResult(run, 0, "plan", false); err == nil {
		t.Fatal("zero start sequence accepted")
	}
	if _, err := NewEventStreamOptions(0, false, testLimits(t)); err == nil {
		t.Fatal("zero replay limit accepted")
	}
	if _, err := NewEventStreamOptions(testLimits(t).ReplayEvents()+1, false, testLimits(t)); err == nil {
		t.Fatal("event replay limit above negotiation accepted")
	}
	for _, values := range []struct {
		requested bool
		terminal  bool
		sequence  uint64
	}{
		{requested: false, terminal: false},
		{requested: true, terminal: true, sequence: 1},
		{requested: false, terminal: true},
	} {
		if _, err := NewCancelResult(values.requested, values.terminal, values.sequence); err == nil {
			t.Fatalf("invalid cancel result accepted: %#v", values)
		}
	}
	if _, err := NewSuspendResult(false, 1, false); err == nil {
		t.Fatal("unsuspended result accepted")
	}
	if _, err := NewResumeResult(true, 0, false); err == nil {
		t.Fatal("zero resume sequence accepted")
	}
	if _, err := NewImportResult(run, 0, false); err == nil {
		t.Fatal("zero import sequence accepted")
	}
	if _, err := NewCancelRequest(run, operation, " reason"); err == nil {
		t.Fatal("untrimmed cancel reason accepted")
	}
	if _, err := NewRunMutation(RunRef{}, operation); err == nil {
		t.Fatal("zero run mutation accepted")
	}
	if _, err := NewImportRequest(operation, Snapshot{}); err == nil {
		t.Fatal("zero import snapshot accepted")
	}
}

func mustOperation(t *testing.T, value string) OperationID {
	t.Helper()
	result, err := NewOperationID(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustRun(t *testing.T, value string) RunRef {
	t.Helper()
	result, err := NewRunRef(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustSnapshot(t *testing.T, encoded []byte) Snapshot {
	t.Helper()
	result, err := ParseSnapshot(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

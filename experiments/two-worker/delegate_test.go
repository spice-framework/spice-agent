package twoworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
	"github.com/spice-framework/spice-agent/tool"
)

type scriptedSession struct {
	client.Session
	connection client.Connection
	start      func(context.Context, client.StartRequest) (client.StartResult, error)
	events     func(context.Context, client.Cursor, client.EventStreamOptions) (client.EventStream, error)
	cancel     func(context.Context, client.CancelRequest) (client.CancelResult, error)
}

func (session *scriptedSession) Connection() client.Connection { return session.connection }
func (session *scriptedSession) Start(ctx context.Context, request client.StartRequest) (client.StartResult, error) {
	return session.start(ctx, request)
}

func (session *scriptedSession) Events(ctx context.Context, cursor client.Cursor, options client.EventStreamOptions) (client.EventStream, error) {
	return session.events(ctx, cursor, options)
}

func (session *scriptedSession) Cancel(ctx context.Context, request client.CancelRequest) (client.CancelResult, error) {
	if session.cancel == nil {
		return client.NewCancelResult(true, false, 0)
	}
	return session.cancel(ctx, request)
}

type frameStream struct {
	frames []client.EventFrame
	next   int
	wait   bool
	mu     sync.Mutex
}

func (stream *frameStream) Next(ctx context.Context) (client.EventFrame, error) {
	stream.mu.Lock()
	if stream.next < len(stream.frames) {
		frame := stream.frames[stream.next]
		stream.next++
		stream.mu.Unlock()
		return frame, nil
	}
	wait := stream.wait
	stream.mu.Unlock()
	if wait {
		<-ctx.Done()
		return client.EventFrame{}, ctx.Err()
	}
	return client.EventFrame{}, errors.New("script ended")
}

func (*frameStream) Close() error { return nil }

type reporterFunc func(context.Context, tool.Progress) error

func (reporter reporterFunc) Report(ctx context.Context, progress tool.Progress) error {
	return reporter(ctx, progress)
}

func TestDelegateDefinitionAndSuccessfulRun(t *testing.T) {
	t.Parallel()
	session, reference := successfulSession(t, "worker handled task")
	delegate, err := NewDelegate(session, Options{Definition: reference, MaximumEvents: 16})
	if err != nil {
		t.Fatal(err)
	}
	definition := delegate.Definition()
	if definition.Name() != ToolName || definition.Effect() != tool.EffectMutating || definition.ReplaySafety() != tool.ReplayIdempotent {
		t.Fatalf("definition = %#v", definition)
	}
	capabilities := definition.Capabilities()
	if len(capabilities) != 1 || capabilities[0] != tool.CapabilityNetworkAccess {
		t.Fatalf("capabilities = %v", capabilities)
	}
	call := mustCall(t, "call-success", `{"task":"inspect package"}`)
	progress := 0
	result, err := delegate.Execute(t.Context(), call, reporterFunc(func(_ context.Context, value tool.Progress) error {
		progress++
		if value.CallID() != call.ID() {
			t.Fatalf("progress call = %q", value.CallID())
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if progress != 1 {
		t.Fatalf("progress count = %d", progress)
	}
	var payload resultPayload
	if err = json.Unmarshal(result.Content(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.Text != "worker handled task" || payload.RunID == "" || payload.PlanID != "plan-worker" {
		t.Fatalf("result = %+v", payload)
	}
}

func TestDelegateDeterministicRetryUsesOneRemoteOperation(t *testing.T) {
	t.Parallel()
	connection, reference := testConnection(t)
	var mu sync.Mutex
	var operations []string
	run := mustRun(t, "run-retry")
	session := &scriptedSession{connection: connection}
	session.start = func(_ context.Context, request client.StartRequest) (client.StartResult, error) {
		mu.Lock()
		operations = append(operations, request.Operation().String())
		duplicate := len(operations) > 1
		mu.Unlock()
		return client.NewStartResult(run, 1, "plan-retry", duplicate)
	}
	session.events = func(context.Context, client.Cursor, client.EventStreamOptions) (client.EventStream, error) {
		return &frameStream{frames: terminalFrames(t, run, "done", client.EventRunCompleted)}, nil
	}
	delegate, err := NewDelegate(session, Options{Definition: reference, MaximumEvents: 16})
	if err != nil {
		t.Fatal(err)
	}
	call := mustCall(t, "call-retry", `{"task":"same work"}`)
	for range 2 {
		result, executeErr := delegate.Execute(t.Context(), call, nil)
		if executeErr != nil || result.IsZero() {
			t.Fatalf("retry result = %#v, %v", result, executeErr)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(operations) != 2 || operations[0] != operations[1] {
		t.Fatalf("start operations = %v", operations)
	}
}

func TestDelegatePropagatesCancellationAndUncertainOutcome(t *testing.T) {
	t.Parallel()
	connection, reference := testConnection(t)
	run := mustRun(t, "run-cancel")
	cancelled := make(chan client.CancelRequest, 1)
	session := &scriptedSession{connection: connection}
	session.start = func(context.Context, client.StartRequest) (client.StartResult, error) {
		return client.NewStartResult(run, 1, "plan-cancel", false)
	}
	session.events = func(context.Context, client.Cursor, client.EventStreamOptions) (client.EventStream, error) {
		return &frameStream{wait: true}, nil
	}
	session.cancel = func(_ context.Context, request client.CancelRequest) (client.CancelResult, error) {
		cancelled <- request
		return client.NewCancelResult(true, false, 0)
	}
	delegate, err := NewDelegate(session, Options{Definition: reference, MaximumEvents: 16})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = delegate.Execute(ctx, mustCall(t, "call-cancel", `{"task":"wait"}`), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("execute error = %v", err)
	}
	// Cancellation before Start is definitive and must not create a remote run.
	select {
	case request := <-cancelled:
		t.Fatalf("unexpected remote cancellation %q", request.Operation().String())
	default:
	}

	ctx, cancel = context.WithCancel(t.Context())
	started := make(chan struct{})
	session.events = func(context.Context, client.Cursor, client.EventStreamOptions) (client.EventStream, error) {
		close(started)
		return &frameStream{wait: true}, nil
	}
	done := make(chan error, 1)
	go func() {
		_, executeErr := delegate.Execute(ctx, mustCall(t, "call-active-cancel", `{"task":"wait"}`), nil)
		done <- executeErr
	}()
	<-started
	cancel()
	err = <-done
	failure, ok := errors.AsType[*tool.ExecutionError](err)
	if !ok || failure.State() != tool.ExecutionUncertain || failure.RetryDisposition() != tool.RetryNever || !errors.Is(err, context.Canceled) {
		t.Fatalf("active cancellation error = %T %v", err, err)
	}
	select {
	case request := <-cancelled:
		if request.Run().ID() != run.ID() || request.Operation().String() != "worker.delegate.cancel."+callIdentity("call-active-cancel") {
			t.Fatalf("cancel request = %#v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("remote cancellation was not propagated")
	}
}

func TestDelegateFailureBoundsAndValidation(t *testing.T) {
	t.Parallel()
	connection, reference := testConnection(t)
	run := mustRun(t, "run-failure")
	session := &scriptedSession{connection: connection}
	session.start = func(context.Context, client.StartRequest) (client.StartResult, error) {
		return client.NewStartResult(run, 1, "plan-failure", false)
	}
	session.events = func(context.Context, client.Cursor, client.EventStreamOptions) (client.EventStream, error) {
		return &frameStream{frames: terminalFrames(t, run, "", client.EventRunFailed)}, nil
	}
	delegate, err := NewDelegate(session, Options{Definition: reference, MaximumEvents: 16})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		call    tool.Call
		problem string
	}{
		{name: "unknown field", call: mustCall(t, "bad-field", `{"task":"x","extra":true}`), problem: "invalid_arguments"},
		{name: "whitespace", call: mustCall(t, "bad-space", `{"task":" x"}`), problem: "invalid_arguments"},
		{name: "remote failure", call: mustCall(t, "remote-failure", `{"task":"x"}`), problem: "worker_failed"},
	}
	for _, current := range tests {
		current := current
		t.Run(current.name, func(t *testing.T) {
			t.Parallel()
			result, executeErr := delegate.Execute(t.Context(), current.call, nil)
			if executeErr != nil {
				t.Fatal(executeErr)
			}
			var payload errorPayload
			if err := json.Unmarshal(result.Content(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Code != current.problem {
				t.Fatalf("failure code = %q", payload.Code)
			}
		})
	}

	if _, err = NewDelegate(nil, Options{Definition: reference}); err == nil {
		t.Fatal("nil session accepted")
	}
	if _, err = NewDelegate(session, Options{}); err == nil {
		t.Fatal("zero definition accepted")
	}
	if _, err = NewDelegate(session, Options{Definition: reference, MaximumEvents: connection.Limits().ReplayEvents() + 1}); err == nil {
		t.Fatal("oversized event bound accepted")
	}
	if _, err = NewDelegate(session, Options{Definition: reference, MaximumEvents: 1, MaximumTextBytes: client.MaximumTextBytes + 1}); err == nil {
		t.Fatal("oversized text bound accepted")
	}
}

func TestDelegateIsConcurrent(t *testing.T) {
	t.Parallel()
	session, reference := successfulSession(t, "concurrent")
	delegate, err := NewDelegate(session, Options{Definition: reference, MaximumEvents: 16})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errorsFound := make(chan error, 32)
	for index := range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			call := mustCall(t, tool.CallID(fmt.Sprintf("call-%d", index)), `{"task":"parallel"}`)
			result, executeErr := delegate.Execute(t.Context(), call, nil)
			if executeErr != nil {
				errorsFound <- executeErr
				return
			}
			if result.CallID() != call.ID() {
				errorsFound <- errors.New("result correlation changed")
			}
		}()
	}
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
}

func TestDelegateInfrastructureFailures(t *testing.T) {
	t.Parallel()
	connection, reference := testConnection(t)
	run := mustRun(t, "run-infrastructure")
	start := func(context.Context, client.StartRequest) (client.StartResult, error) {
		return client.NewStartResult(run, 1, "plan-infrastructure", false)
	}
	call := mustCall(t, "call-infrastructure", `{"task":"work"}`)

	var nilDelegate *Delegate
	if _, err := nilDelegate.Execute(t.Context(), call, nil); err == nil {
		t.Fatal("nil delegate executed")
	}
	session := &scriptedSession{connection: connection, start: start}
	session.events = func(context.Context, client.Cursor, client.EventStreamOptions) (client.EventStream, error) {
		return &frameStream{frames: terminalFrames(t, run, "done", client.EventRunCompleted)}, nil
	}
	delegate, err := NewDelegate(session, Options{Definition: reference})
	if err != nil {
		t.Fatal(err)
	}
	if delegate.Definition().Name() != ToolName {
		t.Fatal("default delegate definition is unavailable")
	}
	if (&Delegate{}).Definition().Name() != "" {
		t.Fatal("zero delegate returned a definition")
	}
	if _, err = delegate.Execute(nil, call, nil); err == nil {
		t.Fatal("nil context executed")
	}
	wrongName, _ := tool.NewCall("wrong-name", "other.tool", json.RawMessage(`{"task":"work"}`))
	wrongResult, err := delegate.Execute(t.Context(), wrongName, nil)
	if err != nil {
		t.Fatal(err)
	}
	if problem, ok := wrongResult.Problem(); !ok || problem == "" {
		t.Fatal("wrong tool name was not model-visible failure")
	}

	t.Run("definitive start", func(t *testing.T) {
		session := &scriptedSession{connection: connection}
		session.start = func(context.Context, client.StartRequest) (client.StartResult, error) {
			return client.StartResult{}, errors.New("definitive rejection")
		}
		delegate, constructionErr := NewDelegate(session, Options{Definition: reference, MaximumEvents: 8})
		if constructionErr != nil {
			t.Fatal(constructionErr)
		}
		_, executeErr := delegate.Execute(t.Context(), call, nil)
		failure, ok := errors.AsType[*tool.ExecutionError](executeErr)
		if !ok || failure.State() != tool.ExecutionDefinitive || failure.RetryDisposition() != tool.RetryAllowed {
			t.Fatalf("start error = %T %v", executeErr, executeErr)
		}
	})

	t.Run("uncertain start", func(t *testing.T) {
		session := &scriptedSession{connection: connection}
		session.start = func(_ context.Context, request client.StartRequest) (client.StartResult, error) {
			operation := request.Operation()
			facts, factsErr := client.NewErrorFacts("start outcome uncertain", false, &operation)
			if factsErr != nil {
				return client.StartResult{}, factsErr
			}
			return client.StartResult{}, mustUncertain(t, facts, operation)
		}
		delegate, constructionErr := NewDelegate(session, Options{Definition: reference, MaximumEvents: 8})
		if constructionErr != nil {
			t.Fatal(constructionErr)
		}
		_, executeErr := delegate.Execute(t.Context(), call, nil)
		failure, ok := errors.AsType[*tool.ExecutionError](executeErr)
		if !ok || failure.State() != tool.ExecutionUncertain || failure.RetryDisposition() != tool.RetryNever {
			t.Fatalf("start error = %T %v", executeErr, executeErr)
		}
	})

	t.Run("progress rejection", func(t *testing.T) {
		cancelled := make(chan struct{}, 1)
		session := &scriptedSession{connection: connection, start: start}
		session.cancel = func(context.Context, client.CancelRequest) (client.CancelResult, error) {
			cancelled <- struct{}{}
			return client.NewCancelResult(true, false, 0)
		}
		delegate, constructionErr := NewDelegate(session, Options{Definition: reference, MaximumEvents: 8})
		if constructionErr != nil {
			t.Fatal(constructionErr)
		}
		_, executeErr := delegate.Execute(t.Context(), call, reporterFunc(func(context.Context, tool.Progress) error {
			return errors.New("rejected")
		}))
		if executeErr == nil {
			t.Fatal("progress rejection succeeded")
		}
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Fatal("progress rejection did not cancel remote run")
		}
	})
}

func TestDelegateStreamFailuresAndBounds(t *testing.T) {
	t.Parallel()
	connection, reference := testConnection(t)
	run := mustRun(t, "run-stream")
	start := func(context.Context, client.StartRequest) (client.StartResult, error) {
		return client.NewStartResult(run, 1, "plan-stream", false)
	}
	tests := []struct {
		name       string
		maximum    uint32
		text       int
		events     func(context.Context, client.Cursor, client.EventStreamOptions) (client.EventStream, error)
		modelError bool
	}{
		{name: "open failure", maximum: 8, events: func(context.Context, client.Cursor, client.EventStreamOptions) (client.EventStream, error) {
			return nil, errors.New("open failed")
		}},
		{name: "next failure", maximum: 8, events: func(context.Context, client.Cursor, client.EventStreamOptions) (client.EventStream, error) {
			return &frameStream{}, nil
		}},
		{name: "event bound", maximum: 1, events: func(context.Context, client.Cursor, client.EventStreamOptions) (client.EventStream, error) {
			return &frameStream{frames: terminalFrames(t, run, "too many", client.EventRunCompleted)}, nil
		}},
		{name: "text bound", maximum: 8, text: 1, events: func(context.Context, client.Cursor, client.EventStreamOptions) (client.EventStream, error) {
			return &frameStream{frames: terminalFrames(t, run, "too long", client.EventRunCompleted)}, nil
		}},
		{name: "remote cancelled", maximum: 8, modelError: true, events: func(context.Context, client.Cursor, client.EventStreamOptions) (client.EventStream, error) {
			return &frameStream{frames: terminalFrames(t, run, "", client.EventRunCancelled)}, nil
		}},
	}
	for _, current := range tests {
		current := current
		t.Run(current.name, func(t *testing.T) {
			t.Parallel()
			session := &scriptedSession{connection: connection, start: start, events: current.events}
			delegate, err := NewDelegate(session, Options{
				Definition: reference, MaximumEvents: current.maximum, MaximumTextBytes: current.text,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, executeErr := delegate.Execute(t.Context(), mustCall(t, tool.CallID("stream-"+current.name), `{"task":"work"}`), nil)
			if current.modelError {
				if executeErr != nil {
					t.Fatal(executeErr)
				}
				if _, ok := result.Problem(); !ok {
					t.Fatal("remote cancellation was not model-visible")
				}
				return
			}
			failure, ok := errors.AsType[*tool.ExecutionError](executeErr)
			if !ok || failure.State() != tool.ExecutionUncertain || failure.RetryDisposition() != tool.RetryNever {
				t.Fatalf("stream error = %T %v", executeErr, executeErr)
			}
		})
	}
}

func TestResultFailureHelpersFailClosed(t *testing.T) {
	t.Parallel()
	if _, err := success("", make(chan int)); err == nil {
		t.Fatal("unencodable success payload accepted")
	}
	if _, err := modelFailure("", "failure", "bounded failure"); err == nil {
		t.Fatal("uncorrelated model failure accepted")
	}
	result, err := executionFailure("", tool.ExecutionDefinitive, tool.RetryAllowed, "bounded failure", errors.New("cause"))
	if err == nil || !result.IsZero() {
		t.Fatalf("fallback execution failure = %#v, %v", result, err)
	}
}

func mustUncertain(t testing.TB, facts client.ErrorFacts, operation client.OperationID) error {
	t.Helper()
	failure, err := client.NewUncertainOperationError(facts, operation, "start")
	if err != nil {
		t.Fatal(err)
	}
	return failure
}

func successfulSession(t testing.TB, text string) (*scriptedSession, client.DefinitionRef) {
	t.Helper()
	connection, reference := testConnection(t)
	session := &scriptedSession{connection: connection}
	session.start = func(_ context.Context, request client.StartRequest) (client.StartResult, error) {
		run := mustRun(t, "run-"+callIdentity(tool.CallID(request.Operation().String())))
		return client.NewStartResult(run, 1, "plan-worker", false)
	}
	session.events = func(_ context.Context, cursor client.Cursor, _ client.EventStreamOptions) (client.EventStream, error) {
		return &frameStream{frames: terminalFrames(t, cursor.Run(), text, client.EventRunCompleted)}, nil
	}
	return session, reference
}

func testConnection(t testing.TB) (client.Connection, client.DefinitionRef) {
	t.Helper()
	limits, err := client.NewLimits(2<<20, 256, 128, 2<<20, 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	protocol, _ := client.NewProtocolVersion(1, 3, 0)
	build, _ := client.NewBuild("two-worker-test", "experimental", "test", "go1.26.5")
	reference, _ := client.NewDefinitionRef("worker", "revision-1")
	definition, _ := client.NewDefinition(reference, "scripted", 1)
	catalog, _ := client.NewCatalog("test", []client.Definition{definition}, limits)
	health, _ := client.NewHealth(client.HealthReady, nil, 0, limits)
	connection, err := client.NewConnection(client.ConnectionSpec{
		Protocol: protocol, Server: build, Limits: limits, Health: health,
		ClientID: "two-worker-test", OwnershipEpoch: 1, Catalog: catalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	return connection, reference
}

func terminalFrames(t testing.TB, run client.RunRef, text string, terminal client.EventKind) []client.EventFrame {
	t.Helper()
	sequence := uint64(1)
	frames := make([]client.EventFrame, 0, 3)
	started, _ := client.NewRunStartedDetail("worker")
	frames = append(frames, mustFrame(t, run, sequence, client.EventRunStarted, started))
	sequence++
	if text != "" {
		detail, _ := client.NewTextDetail(text)
		frames = append(frames, mustFrame(t, run, sequence, client.EventModelDelta, detail))
		sequence++
	}
	detail := client.NoEventDetail()
	if terminal != client.EventRunCompleted {
		detail, _ = client.NewStatusDetail("worker terminal")
	}
	frames = append(frames, mustFrame(t, run, sequence, terminal, detail))
	return frames
}

func mustFrame(t testing.TB, run client.RunRef, sequence uint64, kind client.EventKind, detail client.EventDetail) client.EventFrame {
	t.Helper()
	event, err := client.NewEvent(run, sequence, time.Unix(int64(sequence), 0), kind, detail)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := client.NewEventFrame(event)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func mustCall(t testing.TB, id tool.CallID, arguments string) tool.Call {
	t.Helper()
	call, err := tool.NewCall(id, ToolName, json.RawMessage(arguments))
	if err != nil {
		t.Fatal(err)
	}
	return call
}

func mustRun(t testing.TB, id string) client.RunRef {
	t.Helper()
	run, err := client.NewRunRef(id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

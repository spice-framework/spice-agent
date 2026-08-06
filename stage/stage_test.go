package stage_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

type uppercaseStage struct{}

func (uppercaseStage) Process(_ context.Context, input string) (string, error) {
	return strings.ToUpper(input), nil
}

func TestTypedStageKeepsInputAndOutputInGoTypeSystem(t *testing.T) {
	t.Parallel()
	var implementation stage.Stage[string, string] = uppercaseStage{}
	output, err := implementation.Process(t.Context(), "spice")
	if err != nil || output != "SPICE" {
		t.Fatalf("Process = %q, %v", output, err)
	}
}

type fakeTool struct {
	name                   string
	resultID               tool.CallID
	progressID             tool.CallID
	definition             tool.Definition
	executionErr           error
	resultWithExecutionErr bool
}

func (fake fakeTool) Definition() tool.Definition {
	if fake.definition.Name() != "" {
		return fake.definition
	}
	value, _ := tool.NewDefinition(fake.name, "test", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe)
	return value
}

func (fake fakeTool) Execute(ctx context.Context, call tool.Call, reporter tool.Reporter) (tool.Result, error) {
	if fake.progressID != "" {
		progress, _ := tool.NewProgress(fake.progressID, "work")
		_ = reporter.Report(ctx, progress)
	}
	id := fake.resultID
	if id == "" {
		id = call.ID()
	}
	result, _ := tool.NewResult(id, json.RawMessage(`"ok"`))
	if fake.executionErr != nil {
		if fake.resultWithExecutionErr {
			return result, fake.executionErr
		}
		return tool.Result{}, fake.executionErr
	}
	return result, nil
}

func TestDispatcherPreservesAndValidatesTypedExecutionFailures(t *testing.T) {
	t.Parallel()
	call, _ := tool.NewCall("c", "a", json.RawMessage(`{}`))
	cancelled, _ := tool.NewExecutionError(call.ID(), tool.ExecutionDefinitive, tool.RetryAllowed, context.Canceled)
	dispatcher, _ := stage.NewDispatcher(map[string]tool.Tool{"a": fakeTool{name: "a", executionErr: cancelled}})
	if _, err := dispatcher.Dispatch(t.Context(), call, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("typed cancellation = %v", err)
	} else {
		var typed *tool.ExecutionError
		if !errors.As(err, &typed) || typed != cancelled {
			t.Fatalf("execution error was not preserved: %T, %v", err, err)
		}
	}

	uncertain, _ := tool.NewExecutionError(call.ID(), tool.ExecutionUncertain, tool.RetryNever, errors.New("commit acknowledgement lost"))
	readOnly, _ := stage.NewDispatcher(map[string]tool.Tool{"a": fakeTool{name: "a", executionErr: uncertain}})
	if _, err := readOnly.Dispatch(t.Context(), call, nil); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only uncertain execution = %v", err)
	}

	mutatingDefinition, _ := tool.NewDefinition("a", "test", json.RawMessage(`{}`), tool.EffectMutating, tool.ReplayUnsafe)
	mutating, _ := stage.NewDispatcher(map[string]tool.Tool{"a": fakeTool{name: "a", definition: mutatingDefinition, executionErr: uncertain}})
	if _, err := mutating.Dispatch(t.Context(), call, nil); !errors.Is(err, uncertain) {
		t.Fatalf("uncertain mutation was not preserved: %T, %v", err, err)
	}

	retryable, _ := tool.NewExecutionError(call.ID(), tool.ExecutionDefinitive, tool.RetryAllowed, errors.New("worker unavailable"))
	replayUnsafe, _ := stage.NewDispatcher(map[string]tool.Tool{"a": fakeTool{name: "a", definition: mutatingDefinition, executionErr: retryable}})
	if _, err := replayUnsafe.Dispatch(t.Context(), call, nil); err == nil || !strings.Contains(err.Error(), "replay-unsafe") {
		t.Fatalf("unsafe retry = %v", err)
	}
	idempotentDefinition, _ := tool.NewDefinition("a", "test", json.RawMessage(`{}`), tool.EffectMutating, tool.ReplayIdempotent)
	idempotent, _ := stage.NewDispatcher(map[string]tool.Tool{"a": fakeTool{name: "a", definition: idempotentDefinition, executionErr: retryable}})
	if _, err := idempotent.Dispatch(t.Context(), call, nil); !errors.Is(err, retryable) {
		t.Fatalf("idempotent retryable failure was not preserved: %T, %v", err, err)
	}
}

func TestDispatcherRejectsUntypedMismatchedAndAmbiguousExecutionFailures(t *testing.T) {
	t.Parallel()
	call, _ := tool.NewCall("c", "a", json.RawMessage(`{}`))
	wrong, _ := tool.NewExecutionError("other", tool.ExecutionDefinitive, tool.RetryNever, errors.New("failure"))
	valid, _ := tool.NewExecutionError(call.ID(), tool.ExecutionDefinitive, tool.RetryNever, errors.New("failure"))
	second, _ := tool.NewExecutionError(call.ID(), tool.ExecutionDefinitive, tool.RetryNever, errors.New("second failure"))
	for name, implementation := range map[string]tool.Tool{
		"untyped":                   fakeTool{name: "a", executionErr: errors.New("plain failure")},
		"wrapped typed":             fakeTool{name: "a", executionErr: fmt.Errorf("wrapped: %w", valid)},
		"joined sibling":            fakeTool{name: "a", executionErr: errors.Join(valid, errors.New("sibling failure"))},
		"multiple typed":            fakeTool{name: "a", executionErr: errors.Join(valid, second)},
		"wrong correlation":         fakeTool{name: "a", executionErr: wrong},
		"result and error":          fakeTool{name: "a", executionErr: wrong, resultWithExecutionErr: true},
		"oversized wrapped sibling": fakeTool{name: "a", executionErr: fmt.Errorf("%s: %w", strings.Repeat("SECRET", tool.MaximumExecutionErrorBytes), valid)},
	} {
		dispatcher, err := stage.NewDispatcher(map[string]tool.Tool{"a": implementation})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = dispatcher.Dispatch(t.Context(), call, nil); err == nil {
			t.Errorf("%s execution failure succeeded", name)
		} else if len(err.Error()) > 256 || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%s leaked unbounded rejected error: %q", name, err)
		}
	}
}

type collectingReporter struct{ progress []tool.Progress }

func (reporter *collectingReporter) Report(_ context.Context, progress tool.Progress) error {
	reporter.progress = append(reporter.progress, progress)
	return nil
}

type errorReporter struct{}

func (errorReporter) Report(context.Context, tool.Progress) error { return errors.New("report failed") }

type cancellingTool struct {
	cancel context.CancelFunc
}

func (implementation cancellingTool) Definition() tool.Definition {
	definition, _ := tool.NewDefinition("cancel", "cancel", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe)
	return definition
}

func (implementation cancellingTool) Execute(_ context.Context, call tool.Call, _ tool.Reporter) (tool.Result, error) {
	implementation.cancel()
	result, _ := tool.NewResult(call.ID(), json.RawMessage(`null`))
	return result, nil
}

func TestDispatcherSnapshotsDefinitionsAndEnforcesCorrelation(t *testing.T) {
	definition, _ := tool.NewDefinition("a", "test", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe, tool.CapabilityFilesystemRead)
	dispatcher, err := stage.NewDispatcher(map[string]tool.Tool{"a": fakeTool{name: "a", definition: definition}})
	if err != nil {
		t.Fatal(err)
	}
	got, found := dispatcher.Definition("a")
	if !found || got.Capabilities()[0] != tool.CapabilityFilesystemRead {
		t.Fatal("definition snapshot missing")
	}
	definitions := dispatcher.Definitions()
	if len(definitions) != 1 || definitions[0].Name() != "a" {
		t.Fatalf("definitions = %v", definitions)
	}
	var nilDispatcher *stage.Dispatcher
	if len(nilDispatcher.Definitions()) != 0 {
		t.Fatal("nil dispatcher definitions were nonempty")
	}
	if _, found = nilDispatcher.Definition("a"); found {
		t.Fatal("nil dispatcher resolved definition")
	}
	call, _ := tool.NewCall("c", "a", json.RawMessage(`{}`))
	reporter := &collectingReporter{}
	result, err := dispatcher.Dispatch(t.Context(), call, reporter)
	if err != nil || result.CallID() != call.ID() {
		t.Fatalf("dispatch: %v", err)
	}
	forged, _ := stage.NewDispatcher(map[string]tool.Tool{"a": fakeTool{name: "a", resultID: "other"}})
	if _, err = forged.Dispatch(t.Context(), call, reporter); err == nil {
		t.Fatal("forged result correlation succeeded")
	}
	badProgress, _ := stage.NewDispatcher(map[string]tool.Tool{"a": fakeTool{name: "a", progressID: "other"}})
	// The fake deliberately ignores Report's error; the dispatcher still fails.
	if _, err = badProgress.Dispatch(t.Context(), call, reporter); err == nil {
		t.Fatal("forged progress correlation succeeded")
	}
}

func TestDispatcherRejectsCancellationAndInvalidInputs(t *testing.T) {
	dispatcher, _ := stage.NewDispatcher(map[string]tool.Tool{"a": fakeTool{name: "a"}})
	call, _ := tool.NewCall("c", "a", json.RawMessage(`{}`))
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := dispatcher.Dispatch(cancelled, call, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled dispatch = %v", err)
	}
	var nilContext context.Context
	if _, err := dispatcher.Dispatch(nilContext, call, nil); err == nil {
		t.Fatal("nil context succeeded")
	}
	missing, _ := tool.NewCall("c", "missing", json.RawMessage(`{}`))
	if _, err := dispatcher.Dispatch(t.Context(), missing, nil); err == nil {
		t.Fatal("missing tool succeeded")
	}
	if _, err := stage.NewDispatcher(map[string]tool.Tool{"nil": nil}); err == nil {
		t.Fatal("nil tool succeeded")
	}
	if _, err := stage.NewDispatcher(map[string]tool.Tool{"bean": fakeTool{name: "other"}}); err == nil {
		t.Fatal("mismatched name succeeded")
	}
	progressTool, _ := stage.NewDispatcher(map[string]tool.Tool{"a": fakeTool{name: "a", progressID: "c"}})
	if _, err := progressTool.Dispatch(t.Context(), call, errorReporter{}); err == nil {
		t.Fatal("delegate progress error succeeded")
	}
	postContext, postCancel := context.WithCancel(t.Context())
	postDispatcher, _ := stage.NewDispatcher(map[string]tool.Tool{"cancel": cancellingTool{cancel: postCancel}})
	postCall, _ := tool.NewCall("c", "cancel", json.RawMessage(`{}`))
	if _, err := postDispatcher.Dispatch(postContext, postCall, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-execute cancellation = %v", err)
	}
}

package stage_test

import (
	"context"
	"encoding/json"
	"errors"
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
	name       string
	resultID   tool.CallID
	progressID tool.CallID
	definition tool.Definition
}

func (fake fakeTool) Definition() tool.Definition {
	if fake.definition.Name() != "" {
		return fake.definition
	}
	value, _ := tool.NewDefinition(fake.name, "test", json.RawMessage(`{}`))
	return value
}

func (fake fakeTool) Execute(ctx context.Context, call tool.Call, reporter tool.Reporter) tool.Result {
	if fake.progressID != "" {
		progress, _ := tool.NewProgress(fake.progressID, "work")
		_ = reporter.Report(ctx, progress)
	}
	id := fake.resultID
	if id == "" {
		id = call.ID()
	}
	result, _ := tool.NewResult(id, json.RawMessage(`"ok"`))
	return result
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
	definition, _ := tool.NewDefinition("cancel", "cancel", json.RawMessage(`{}`))
	return definition
}

func (implementation cancellingTool) Execute(_ context.Context, call tool.Call, _ tool.Reporter) tool.Result {
	implementation.cancel()
	result, _ := tool.NewResult(call.ID(), json.RawMessage(`null`))
	return result
}

func TestDispatcherSnapshotsDefinitionsAndEnforcesCorrelation(t *testing.T) {
	definition, _ := tool.NewDefinition("a", "test", json.RawMessage(`{}`), tool.CapabilityFilesystemRead)
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

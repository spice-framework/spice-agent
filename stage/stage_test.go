package stage_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

type fakeTool struct{ name string }

func (fake fakeTool) Definition() tool.Definition {
	definition, _ := tool.NewDefinition(fake.name, "test", json.RawMessage(`{}`))
	return definition
}

func (fake fakeTool) Execute(_ context.Context, call tool.Call, _ tool.Reporter) tool.Result {
	return tool.Result{CallID: call.ID, Content: json.RawMessage(`"ok"`)}
}

func TestDispatcherUsesCanonicalBeanNames(t *testing.T) {
	dispatcher, err := stage.NewDispatcher(map[string]tool.Tool{"z": fakeTool{"z"}, "a": fakeTool{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	definitions := dispatcher.Definitions()
	if definitions[0].Name() != "a" || definitions[1].Name() != "z" {
		t.Fatalf("definition order = %s, %s", definitions[0].Name(), definitions[1].Name())
	}
	call := tool.Call{ID: "c", Name: "a", Arguments: json.RawMessage(`{}`)}
	if result := dispatcher.Dispatch(context.Background(), call, nil); result.Error != "" {
		t.Fatalf("dispatch result = %+v", result)
	}
	missing := call
	missing.Name = "missing"
	if result := dispatcher.Dispatch(context.Background(), missing, nil); result.Error == "" {
		t.Fatal("missing tool succeeded")
	}
}

func TestDispatcherRejectsInvalidBeans(t *testing.T) {
	if _, err := stage.NewDispatcher(map[string]tool.Tool{"nil": nil}); err == nil {
		t.Fatal("nil tool succeeded")
	}
	if _, err := stage.NewDispatcher(map[string]tool.Tool{"bean": fakeTool{"other"}}); err == nil {
		t.Fatal("mismatched name succeeded")
	}
	dispatcher, err := stage.NewDispatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	if result := dispatcher.Dispatch(context.Background(), tool.Call{}, nil); result.Error == "" {
		t.Fatal("invalid call succeeded")
	}
}

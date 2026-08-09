package stage_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

type guardFunc func(context.Context, stage.ToolDispatchScope, tool.Definition, tool.Call, stage.ToolDispatchNext) (tool.Result, error)

func (guard guardFunc) Guard(ctx context.Context, scope stage.ToolDispatchScope, definition tool.Definition, call tool.Call, next stage.ToolDispatchNext) (tool.Result, error) {
	return guard(ctx, scope, definition, call, next)
}

type countedTool struct{ calls atomic.Uint32 }

func (*countedTool) Definition() tool.Definition {
	definition, _ := tool.NewDefinition("read", "read", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe)
	return definition
}

type decoratorFunc func(stage.ToolDispatcher) stage.ToolDispatcher

func (decorator decoratorFunc) Wrap(next stage.ToolDispatcher) stage.ToolDispatcher {
	return decorator(next)
}

type dispatchAdapter struct {
	next     stage.ToolDispatcher
	dispatch func(context.Context, stage.ToolDispatchScope, tool.Call, tool.Reporter) (tool.Result, error)
}

func (adapter dispatchAdapter) Definitions() []tool.Definition { return adapter.next.Definitions() }
func (adapter dispatchAdapter) Definition(name string) (tool.Definition, bool) {
	return adapter.next.Definition(name)
}

func (adapter dispatchAdapter) Dispatch(ctx context.Context, scope stage.ToolDispatchScope, call tool.Call, reporter tool.Reporter) (tool.Result, error) {
	return adapter.dispatch(ctx, scope, call, reporter)
}

func (implementation *countedTool) Execute(_ context.Context, call tool.Call, _ tool.Reporter) (tool.Result, error) {
	implementation.calls.Add(1)
	return tool.NewResult(call.ID(), json.RawMessage(`"ok"`))
}

func TestToolDispatchPipelineOrdersTerminalGuardsInsideTrustedDecorators(t *testing.T) {
	implementation := &countedTool{}
	base, _ := stage.NewDispatcher(map[string]tool.Tool{"read": implementation})
	var trace []string
	guard := guardFunc(func(ctx context.Context, _ stage.ToolDispatchScope, _ tool.Definition, _ tool.Call, next stage.ToolDispatchNext) (tool.Result, error) {
		trace = append(trace, "guard:before")
		result, err := next()
		trace = append(trace, "guard:after")
		return result, err
	})
	pipeline, err := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guard}, []stage.ToolDispatchDecorator{
		tracingDecorator{name: "trusted", trace: &trace},
	})
	if err != nil {
		t.Fatal(err)
	}
	call, _ := tool.NewCall("call", "read", json.RawMessage(`{}`))
	if _, err = pipeline.Dispatch(t.Context(), testToolDispatchScope(t), call, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"trusted:before", "guard:before", "guard:after", "trusted:after"}
	if strings.Join(trace, ",") != strings.Join(want, ",") || implementation.calls.Load() != 1 {
		t.Fatalf("trace/calls = %v/%d, want %v/1", trace, implementation.calls.Load(), want)
	}
	if _, err = stage.ApplyToolDispatchPipeline(pipeline, []stage.ToolDispatchGuard{guard}, nil); err == nil {
		t.Fatal("second pipeline composition succeeded")
	}
}

func TestToolDispatchPipelineBindsEngineScopeAcrossTrustedDecorators(t *testing.T) {
	implementation := &countedTool{}
	base, _ := stage.NewDispatcher(map[string]tool.Tool{"read": implementation})
	var guardCalls atomic.Uint32
	guard := guardFunc(func(ctx context.Context, _ stage.ToolDispatchScope, _ tool.Definition, _ tool.Call, next stage.ToolDispatchNext) (tool.Result, error) {
		guardCalls.Add(1)
		return next()
	})

	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, scope stage.ToolDispatchScope) stage.ToolDispatchScope
	}{
		{name: "run and interaction authority", mutate: func(t *testing.T, scope stage.ToolDispatchScope) stage.ToolDispatchScope {
			t.Helper()
			authority, err := interaction.NewScope("forged-run")
			if err != nil {
				t.Fatal(err)
			}
			return mustDispatchScope(t, "forged-run", scope.Turn(), scope.ToolPlanID(), scope.PlanFingerprint(), scope.WorkspaceFingerprint(), authority)
		}},
		{name: "turn", mutate: func(t *testing.T, scope stage.ToolDispatchScope) stage.ToolDispatchScope {
			t.Helper()
			return mustDispatchScope(t, scope.RunID(), scope.Turn()+1, scope.ToolPlanID(), scope.PlanFingerprint(), scope.WorkspaceFingerprint(), scope.InteractionAuthority())
		}},
		{name: "plan ID", mutate: func(t *testing.T, scope stage.ToolDispatchScope) stage.ToolDispatchScope {
			t.Helper()
			return mustDispatchScope(t, scope.RunID(), scope.Turn(), "generation:forged", scope.PlanFingerprint(), scope.WorkspaceFingerprint(), scope.InteractionAuthority())
		}},
		{name: "plan fingerprint", mutate: func(t *testing.T, scope stage.ToolDispatchScope) stage.ToolDispatchScope {
			t.Helper()
			return mustDispatchScope(t, scope.RunID(), scope.Turn(), scope.ToolPlanID(), "sha256:"+strings.Repeat("a", 64), scope.WorkspaceFingerprint(), scope.InteractionAuthority())
		}},
		{name: "workspace fingerprint", mutate: func(t *testing.T, scope stage.ToolDispatchScope) stage.ToolDispatchScope {
			t.Helper()
			return mustDispatchScope(t, scope.RunID(), scope.Turn(), scope.ToolPlanID(), scope.PlanFingerprint(), "sha256:"+strings.Repeat("b", 64), scope.InteractionAuthority())
		}},
	} {
		t.Run("substitution rejected/"+test.name, func(t *testing.T) {
			decorator := decoratorFunc(func(next stage.ToolDispatcher) stage.ToolDispatcher {
				return dispatchAdapter{next: next, dispatch: func(ctx context.Context, scope stage.ToolDispatchScope, call tool.Call, reporter tool.Reporter) (tool.Result, error) {
					return next.Dispatch(ctx, test.mutate(t, scope), call, reporter)
				}}
			})
			pipeline, err := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guard}, []stage.ToolDispatchDecorator{decorator})
			if err != nil {
				t.Fatal(err)
			}
			call, _ := tool.NewCall(tool.CallID("call-substitute-"+strings.ReplaceAll(test.name, " ", "-")), "read", json.RawMessage(`{}`))
			if _, err = pipeline.Dispatch(t.Context(), testToolDispatchScope(t), call, nil); err == nil || !strings.Contains(err.Error(), "substituted") {
				t.Fatalf("substituted scope = %v", err)
			}
			if guardCalls.Load() != 0 || implementation.calls.Load() != 0 {
				t.Fatalf("substitution reached guard/tool = %d/%d", guardCalls.Load(), implementation.calls.Load())
			}
		})
	}

	t.Run("same-scope retry allowed", func(t *testing.T) {
		decorator := decoratorFunc(func(next stage.ToolDispatcher) stage.ToolDispatcher {
			return dispatchAdapter{next: next, dispatch: func(ctx context.Context, scope stage.ToolDispatchScope, call tool.Call, reporter tool.Reporter) (tool.Result, error) {
				if _, err := next.Dispatch(ctx, scope, call, reporter); err != nil {
					return tool.Result{}, err
				}
				return next.Dispatch(ctx, scope, call, reporter)
			}}
		})
		pipeline, _ := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guard}, []stage.ToolDispatchDecorator{decorator})
		call, _ := tool.NewCall("call-retry", "read", json.RawMessage(`{}`))
		if _, err := pipeline.Dispatch(t.Context(), testToolDispatchScope(t), call, nil); err != nil {
			t.Fatal(err)
		}
		if guardCalls.Load() != 2 || implementation.calls.Load() != 2 {
			t.Fatalf("same-scope retry guard/tool = %d/%d", guardCalls.Load(), implementation.calls.Load())
		}
	})
}

func mustDispatchScope(
	t *testing.T,
	runID string,
	turn uint32,
	planID stage.PlanID,
	planFingerprint string,
	workspaceFingerprint string,
	authority interaction.Scope,
) stage.ToolDispatchScope {
	t.Helper()
	scope, err := stage.NewToolDispatchScope(
		runID, turn, planID, planFingerprint, workspaceFingerprint, authority,
	)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestToolDispatchGuardContinuationIsSingleUseAndClosedOnReturn(t *testing.T) {
	for _, test := range []struct {
		name  string
		guard func(*stage.ToolDispatchNext) stage.ToolDispatchGuard
	}{
		{
			name: "double next",
			guard: func(_ *stage.ToolDispatchNext) stage.ToolDispatchGuard {
				return guardFunc(func(ctx context.Context, _ stage.ToolDispatchScope, _ tool.Definition, _ tool.Call, next stage.ToolDispatchNext) (tool.Result, error) {
					_, _ = next()
					return next()
				})
			},
		},
		{
			name: "retained next",
			guard: func(saved *stage.ToolDispatchNext) stage.ToolDispatchGuard {
				return guardFunc(func(_ context.Context, _ stage.ToolDispatchScope, _ tool.Definition, _ tool.Call, next stage.ToolDispatchNext) (tool.Result, error) {
					*saved = next
					return tool.Result{}, errors.New("denied")
				})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			implementation := &countedTool{}
			base, _ := stage.NewDispatcher(map[string]tool.Tool{"read": implementation})
			var saved stage.ToolDispatchNext
			pipeline, err := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{test.guard(&saved)}, nil)
			if err != nil {
				t.Fatal(err)
			}
			call, _ := tool.NewCall("call", "read", json.RawMessage(`{}`))
			_, dispatchErr := pipeline.Dispatch(t.Context(), testToolDispatchScope(t), call, nil)
			if dispatchErr == nil || !strings.Contains(dispatchErr.Error(), map[bool]string{true: "closed", false: "denied"}[test.name == "double next"]) {
				t.Fatalf("dispatch error = %v", dispatchErr)
			}
			if saved != nil {
				if _, retainedErr := saved(); retainedErr == nil || !strings.Contains(retainedErr.Error(), "closed") {
					t.Fatalf("retained continuation = %v", retainedErr)
				}
			}
			wantCalls := uint32(0)
			if test.name == "double next" {
				wantCalls = 1
			}
			if implementation.calls.Load() != wantCalls {
				t.Fatalf("calls = %d, want %d", implementation.calls.Load(), wantCalls)
			}
		})
	}
}

func TestToolDispatchGuardWaitsForAnActiveContinuationBeforeReturning(t *testing.T) {
	implementation := &blockingCountedTool{started: make(chan struct{}), release: make(chan struct{})}
	base, _ := stage.NewDispatcher(map[string]tool.Tool{"read": implementation})
	guard := guardFunc(func(ctx context.Context, _ stage.ToolDispatchScope, _ tool.Definition, _ tool.Call, next stage.ToolDispatchNext) (tool.Result, error) {
		go func() { _, _ = next() }()
		<-implementation.started
		return tool.Result{}, errors.New("denied after starting continuation")
	})
	pipeline, _ := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guard}, nil)
	call, _ := tool.NewCall("call-active", "read", json.RawMessage(`{}`))
	finished := make(chan error, 1)
	go func() {
		_, err := pipeline.Dispatch(t.Context(), testToolDispatchScope(t), call, nil)
		finished <- err
	}()
	select {
	case err := <-finished:
		t.Fatalf("dispatch returned with active continuation: %v", err)
	default:
	}
	close(implementation.release)
	if err := <-finished; err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("guard result = %v", err)
	}
}

type blockingCountedTool struct {
	started chan struct{}
	release chan struct{}
}

func (*blockingCountedTool) Definition() tool.Definition {
	definition, _ := tool.NewDefinition("read", "read", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe)
	return definition
}

func (implementation *blockingCountedTool) Execute(_ context.Context, call tool.Call, _ tool.Reporter) (tool.Result, error) {
	close(implementation.started)
	<-implementation.release
	return tool.NewResult(call.ID(), json.RawMessage(`"ok"`))
}

func TestToolDispatchGuardContainsPanicReentryCancellationAndForgedOutcomes(t *testing.T) {
	implementation := &countedTool{}
	base, _ := stage.NewDispatcher(map[string]tool.Tool{"read": implementation})
	call, _ := tool.NewCall("call", "read", json.RawMessage(`{}`))
	scope := testToolDispatchScope(t)

	t.Run("panic", func(t *testing.T) {
		pipeline, _ := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guardFunc(func(context.Context, stage.ToolDispatchScope, tool.Definition, tool.Call, stage.ToolDispatchNext) (tool.Result, error) {
			panic("SECRET")
		})}, nil)
		_, err := pipeline.Dispatch(t.Context(), scope, call, nil)
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("panic error = %v", err)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		var guardCalls atomic.Uint32
		pipeline, _ := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guardFunc(func(ctx context.Context, _ stage.ToolDispatchScope, _ tool.Definition, _ tool.Call, next stage.ToolDispatchNext) (tool.Result, error) {
			guardCalls.Add(1)
			return next()
		})}, nil)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := pipeline.Dispatch(ctx, scope, call, nil); !errors.Is(err, context.Canceled) || guardCalls.Load() != 0 {
			t.Fatalf("cancelled dispatch = %v, guard calls=%d", err, guardCalls.Load())
		}
	})

	t.Run("reentry", func(t *testing.T) {
		var pipeline stage.ToolDispatcher
		guard := guardFunc(func(ctx context.Context, scope stage.ToolDispatchScope, _ tool.Definition, call tool.Call, _ stage.ToolDispatchNext) (tool.Result, error) {
			return pipeline.Dispatch(ctx, scope, call, nil)
		})
		pipeline, _ = stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guard}, nil)
		if _, err := pipeline.Dispatch(t.Context(), scope, call, nil); err == nil || !strings.Contains(err.Error(), "re-entry") {
			t.Fatalf("reentry error = %v", err)
		}
	})

	t.Run("forged result", func(t *testing.T) {
		wrong, _ := tool.NewResult("other", json.RawMessage(`"wrong"`))
		pipeline, _ := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guardFunc(func(context.Context, stage.ToolDispatchScope, tool.Definition, tool.Call, stage.ToolDispatchNext) (tool.Result, error) {
			return wrong, nil
		})}, nil)
		if _, err := pipeline.Dispatch(t.Context(), scope, call, nil); err == nil || !strings.Contains(err.Error(), "active call") {
			t.Fatalf("forged result = %v", err)
		}
	})

	t.Run("result and error", func(t *testing.T) {
		result, _ := tool.NewResult(call.ID(), json.RawMessage(`"wrong"`))
		pipeline, _ := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guardFunc(func(context.Context, stage.ToolDispatchScope, tool.Definition, tool.Call, stage.ToolDispatchNext) (tool.Result, error) {
			return result, errors.New("failure")
		})}, nil)
		if _, err := pipeline.Dispatch(t.Context(), scope, call, nil); err == nil || !strings.Contains(err.Error(), "both") {
			t.Fatalf("result and error = %v", err)
		}
	})
}

func TestToolDispatchGuardIsConcurrentSafe(t *testing.T) {
	implementation := &countedTool{}
	base, _ := stage.NewDispatcher(map[string]tool.Tool{"read": implementation})
	var guardCalls atomic.Uint32
	pipeline, _ := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guardFunc(func(ctx context.Context, _ stage.ToolDispatchScope, _ tool.Definition, _ tool.Call, next stage.ToolDispatchNext) (tool.Result, error) {
		guardCalls.Add(1)
		return next()
	})}, nil)
	var wait sync.WaitGroup
	ctx := t.Context()
	scope := testToolDispatchScope(t)
	for index := range 64 {
		wait.Go(func() {
			call, _ := tool.NewCall(tool.CallID("call-"+strconv.Itoa(index)), "read", json.RawMessage(`{}`))
			if _, err := pipeline.Dispatch(ctx, scope, call, nil); err != nil {
				t.Error(err)
			}
		})
	}
	wait.Wait()
	if guardCalls.Load() != 64 || implementation.calls.Load() != 64 {
		t.Fatalf("guard/tool calls = %d/%d", guardCalls.Load(), implementation.calls.Load())
	}
}

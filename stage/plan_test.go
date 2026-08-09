package stage_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

func TestPlanIDAndLeaseContracts(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", " plan", "two words", "line\nbreak", strings.Repeat("x", 257)} {
		if _, err := stage.NewPlanID(value); err == nil {
			t.Errorf("plan ID %q succeeded", value)
		}
	}
	id, err := stage.NewPlanID("generation:one")
	if err != nil || id.String() != "generation:one" || id.Validate() != nil {
		t.Fatalf("plan ID = %q, %v", id, err)
	}
	dispatcher, _ := stage.NewDispatcher(map[string]tool.Tool{"a": fakeTool{name: "a"}})
	var releases atomic.Int32
	lease, err := stage.NewToolPlanLease(id, dispatcher, func() error {
		releases.Add(1)
		return nil
	})
	if err != nil || lease.Validate() != nil || lease.ToolPlanID() != id || lease.Dispatcher() == nil {
		t.Fatalf("lease = %#v, %v", lease, err)
	}
	definitions := lease.Definitions()
	definitions[0] = tool.Definition{}
	if got := lease.Definitions(); len(got) != 1 || got[0].Name() != "a" {
		t.Fatal("lease definitions were mutable")
	}
	if err = lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err = lease.Release(); err != nil || releases.Load() != 1 {
		t.Fatalf("idempotent release = %d, %v", releases.Load(), err)
	}
	if _, err = stage.NewToolPlanLease("", dispatcher, func() error { return nil }); err == nil {
		t.Fatal("invalid lease ID succeeded")
	}
	if _, err = stage.NewToolPlanLease(id, nil, func() error { return nil }); err == nil {
		t.Fatal("nil lease dispatcher succeeded")
	}
	if _, err = stage.NewToolPlanLease(id, dispatcher, nil); err == nil {
		t.Fatal("nil lease release succeeded")
	}
	if (*stage.ToolPlanLease)(nil).Validate() == nil || (*stage.ToolPlanLease)(nil).Dispatcher() != nil ||
		len((*stage.ToolPlanLease)(nil).Definitions()) != 0 || (*stage.ToolPlanLease)(nil).ToolPlanID() != "" ||
		(*stage.ToolPlanLease)(nil).Release() != nil {
		t.Fatal("nil lease is not defensive")
	}
}

func TestLeaseReleaseFailureIsBoundedObservableAndIdempotent(t *testing.T) {
	t.Parallel()
	id, _ := stage.NewPlanID("generation:release")
	dispatcher, _ := stage.NewDispatcher(nil)
	var calls atomic.Int32
	lease, _ := stage.NewToolPlanLease(id, dispatcher, func() error {
		calls.Add(1)
		panic(strings.Repeat("secret", tool.MaximumExecutionErrorBytes))
	})
	first := lease.Release()
	second := lease.Release()
	if first == nil || second == nil || first.Error() != second.Error() || calls.Load() != 1 ||
		strings.Contains(first.Error(), "secret") || len(first.Error()) > tool.MaximumExecutionErrorBytes {
		t.Fatalf("panic release = %d, %v, %v", calls.Load(), first, second)
	}

	lease, _ = stage.NewToolPlanLease(id, dispatcher, func() error {
		return errors.New(strings.Repeat("x", tool.MaximumExecutionErrorBytes*2))
	})
	if err := lease.Release(); err == nil || len(err.Error()) > tool.MaximumExecutionErrorBytes {
		t.Fatalf("bounded release error = %v", err)
	}
}

func TestLeaseReleaseContextBoundsBlockingCallbackWithOneOutcome(t *testing.T) {
	t.Parallel()
	id, _ := stage.NewPlanID("generation:blocking")
	dispatcher, _ := stage.NewDispatcher(nil)
	block := make(chan struct{})
	started := make(chan struct{})
	var calls atomic.Int32
	lease, _ := stage.NewToolPlanLease(id, dispatcher, func() error {
		calls.Add(1)
		close(started)
		<-block
		return nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	first := lease.ReleaseContext(ctx)
	if first == nil || !strings.Contains(first.Error(), "did not complete") || calls.Load() != 1 {
		t.Fatalf("bounded release = %d, %v", calls.Load(), first)
	}
	<-started
	close(block)
	second := lease.Release()
	if second == nil || second.Error() != first.Error() || calls.Load() != 1 {
		t.Fatalf("idempotent bounded release = %d, %v, %v", calls.Load(), first, second)
	}
}

func TestStaticToolPlanSourceLeasesExactGeneration(t *testing.T) {
	t.Parallel()
	dispatcher, _ := stage.NewDispatcher(map[string]tool.Tool{"a": fakeTool{name: "a"}})
	source, err := stage.NewStaticToolPlanSource(dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	first, err := source.LeaseCurrent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := source.LeaseGeneration(t.Context(), first.ToolPlanID())
	if err != nil || first == second || second.ToolPlanID() != first.ToolPlanID() {
		t.Fatalf("static leases = %#v, %#v, %v", first, second, err)
	}
	missing, _ := stage.NewPlanID("generation:missing")
	if _, err = source.LeaseGeneration(t.Context(), missing); err == nil {
		t.Fatal("missing static generation succeeded")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = source.LeaseCurrent(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled lease = %v", err)
	}
	_ = first.Release()
	_ = second.Release()
}

type mutableDispatcher struct {
	definitions []tool.Definition
	calls       atomic.Int32
}

func (dispatcher *mutableDispatcher) Definitions() []tool.Definition {
	return append([]tool.Definition(nil), dispatcher.definitions...)
}

func (dispatcher *mutableDispatcher) Definition(name string) (tool.Definition, bool) {
	for _, definition := range dispatcher.definitions {
		if definition.Name() == name {
			return definition, true
		}
	}
	return tool.Definition{}, false
}

func (dispatcher *mutableDispatcher) Dispatch(_ context.Context, _ stage.ToolDispatchScope, call tool.Call, _ tool.Reporter) (tool.Result, error) {
	dispatcher.calls.Add(1)
	return tool.NewResult(call.ID(), json.RawMessage(`"mutable"`))
}

func TestLeaseDispatcherRejectsNamesOutsideImmutableSnapshot(t *testing.T) {
	t.Parallel()
	a, _ := tool.NewDefinition("a", "a", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe)
	b, _ := tool.NewDefinition("b", "b", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe)
	base := &mutableDispatcher{definitions: []tool.Definition{a}}
	id, _ := stage.NewPlanID("generation:immutable")
	lease, err := stage.NewToolPlanLease(id, base, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil {
		t.Fatal("tool plan lease was nil")
	}
	leasedDispatcher := lease.Dispatcher()
	if leasedDispatcher == nil {
		t.Fatal("leased tool dispatcher was nil")
	}
	base.definitions = []tool.Definition{b}
	if definitions := leasedDispatcher.Definitions(); len(definitions) != 1 || definitions[0].Name() != "a" {
		t.Fatalf("leased definitions changed = %v", definitions)
	}
	bCall, _ := tool.NewCall("call-b", "b", json.RawMessage(`{}`))
	if _, err = leasedDispatcher.Dispatch(t.Context(), testToolDispatchScopeForPlan(t, id), bCall, nil); err == nil || base.calls.Load() != 0 {
		t.Fatalf("undeclared mutable tool dispatched: calls=%d err=%v", base.calls.Load(), err)
	}
	aCall, _ := tool.NewCall("call-a", "a", json.RawMessage(`{}`))
	if _, err = leasedDispatcher.Dispatch(t.Context(), testToolDispatchScopeForPlan(t, id), aCall, nil); err != nil || base.calls.Load() != 1 {
		t.Fatalf("snapshotted tool did not dispatch: calls=%d err=%v", base.calls.Load(), err)
	}
	if _, err = leasedDispatcher.Dispatch(t.Context(), testToolDispatchScope(t), aCall, nil); err == nil || base.calls.Load() != 1 {
		t.Fatalf("mismatched dispatch plan = %v, calls=%d", err, base.calls.Load())
	}
	_ = lease.Release()
}

func TestSnapshotToolDispatcherFreezesDefinitionsWithoutComposingPolicy(t *testing.T) {
	t.Parallel()
	a, _ := tool.NewDefinition("a", "a", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe)
	b, _ := tool.NewDefinition("b", "b", json.RawMessage(`{}`), tool.EffectReadOnly, tool.ReplaySafe)
	base := &mutableDispatcher{definitions: []tool.Definition{a}}
	snapshot, err := stage.SnapshotToolDispatcher(base)
	if err != nil {
		t.Fatal(err)
	}
	base.definitions = []tool.Definition{b}
	if definitions := snapshot.Definitions(); len(definitions) != 1 || definitions[0].Name() != "a" {
		t.Fatalf("snapshot definitions changed = %v", definitions)
	}
	bCall, _ := tool.NewCall("call-b", "b", json.RawMessage(`{}`))
	if _, err = snapshot.Dispatch(t.Context(), testToolDispatchScope(t), bCall, nil); err == nil || base.calls.Load() != 0 {
		t.Fatalf("undeclared mutable tool dispatched: calls=%d err=%v", base.calls.Load(), err)
	}
	aCall, _ := tool.NewCall("call-a", "a", json.RawMessage(`{}`))
	if _, err = snapshot.Dispatch(t.Context(), testToolDispatchScope(t), aCall, nil); err != nil || base.calls.Load() != 1 {
		t.Fatalf("snapshotted tool did not dispatch: calls=%d err=%v", base.calls.Load(), err)
	}
	if _, err = stage.SnapshotToolDispatcher(nil); err == nil {
		t.Fatal("nil snapshot dispatcher succeeded")
	}
}

type tracingDecorator struct {
	name  string
	trace *[]string
}

func (decorator tracingDecorator) Wrap(next stage.ToolDispatcher) stage.ToolDispatcher {
	return tracingDispatcher{name: decorator.name, trace: decorator.trace, next: next}
}

type tracingDispatcher struct {
	name  string
	trace *[]string
	next  stage.ToolDispatcher
}

func (dispatcher tracingDispatcher) Definitions() []tool.Definition {
	return dispatcher.next.Definitions()
}

func (dispatcher tracingDispatcher) Definition(name string) (tool.Definition, bool) {
	return dispatcher.next.Definition(name)
}

func (dispatcher tracingDispatcher) Dispatch(ctx context.Context, scope stage.ToolDispatchScope, call tool.Call, reporter tool.Reporter) (tool.Result, error) {
	*dispatcher.trace = append(*dispatcher.trace, dispatcher.name+":before")
	result, err := dispatcher.next.Dispatch(ctx, scope, call, reporter)
	*dispatcher.trace = append(*dispatcher.trace, dispatcher.name+":after")
	return result, err
}

type panicDecorator struct{}

func (panicDecorator) Wrap(stage.ToolDispatcher) stage.ToolDispatcher { panic("secret panic") }

type nilDecorator struct{}

func (nilDecorator) Wrap(stage.ToolDispatcher) stage.ToolDispatcher { return nil }

type replacingDecorator struct{ replacement stage.ToolDispatcher }

func (decorator replacingDecorator) Wrap(stage.ToolDispatcher) stage.ToolDispatcher {
	return decorator.replacement
}

func TestApplyToolDispatchDecoratorsOrdersAndValidates(t *testing.T) {
	t.Parallel()
	base, _ := stage.NewDispatcher(map[string]tool.Tool{"a": fakeTool{name: "a"}})
	var trace []string
	decorated, err := stage.ApplyToolDispatchDecorators(base, []stage.ToolDispatchDecorator{
		tracingDecorator{name: "first", trace: &trace},
		tracingDecorator{name: "second", trace: &trace},
	})
	if err != nil {
		t.Fatal(err)
	}
	call, _ := tool.NewCall("call", "a", json.RawMessage(`{}`))
	if _, err = decorated.Dispatch(t.Context(), testToolDispatchScope(t), call, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"first:before", "second:before", "second:after", "first:after"}
	if !reflect.DeepEqual(trace, want) {
		t.Fatalf("decorator order = %v", trace)
	}
	if _, err = stage.ApplyToolDispatchDecorators(nil, nil); err == nil {
		t.Fatal("nil base succeeded")
	}
	for name, decorators := range map[string][]stage.ToolDispatchDecorator{
		"nil":        {nil},
		"panic":      {panicDecorator{}},
		"nil return": {nilDecorator{}},
	} {
		if _, err = stage.ApplyToolDispatchDecorators(base, decorators); err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("%s decorator = %v", name, err)
		}
	}
	empty, _ := stage.NewDispatcher(nil)
	if _, err = stage.ApplyToolDispatchDecorators(base, []stage.ToolDispatchDecorator{replacingDecorator{replacement: empty}}); err == nil {
		t.Fatal("definition-changing decorator succeeded")
	}
}

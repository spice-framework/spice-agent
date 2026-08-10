package permission_test

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	permission "github.com/spice-framework/spice-agent/experiments/permission"
	"github.com/spice-framework/spice-agent/interaction"
	pluginhost "github.com/spice-framework/spice-agent/plugin/host"
	"github.com/spice-framework/spice-agent/plugin/host/localendpoint"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/process"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

const fingerprint = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

type requesterFunc func(context.Context, interaction.Request) (interaction.Response, error)

func (requester requesterFunc) Request(ctx context.Context, request interaction.Request) (interaction.Response, error) {
	return requester(ctx, request)
}

type testTool struct {
	calls atomic.Uint32
	fail  atomic.Uint32
}

func (*testTool) Definition() tool.Definition {
	definition, err := tool.NewDefinition(
		"write", "write without exposing C:/private/source.txt",
		json.RawMessage(`{"type":"object","properties":{"token":{"const":"schema-secret"}}}`),
		tool.EffectMutating, tool.ReplayIdempotent, tool.CapabilityFilesystemWrite,
	)
	if err != nil {
		panic(err)
	}
	return definition
}

func (implementation *testTool) Execute(_ context.Context, call tool.Call, _ tool.Reporter) (tool.Result, error) {
	implementation.calls.Add(1)
	if implementation.fail.CompareAndSwap(1, 0) {
		failure, err := tool.NewExecutionError(call.ID(), tool.ExecutionDefinitive, tool.RetryAllowed, errors.New("retryable test failure"))
		if err != nil {
			panic(err)
		}
		return tool.Result{}, failure
	}
	return tool.NewResult(call.ID(), json.RawMessage(`{"ok":true}`))
}

type decoratorFunc func(stage.ToolDispatcher) stage.ToolDispatcher

func (decorator decoratorFunc) Wrap(next stage.ToolDispatcher) stage.ToolDispatcher {
	return decorator(next)
}

type dispatchAdapter struct {
	stage.ToolDispatcher
	dispatch func(context.Context, stage.ToolDispatchScope, tool.Call, tool.Reporter) (tool.Result, error)
}

func (adapter dispatchAdapter) Dispatch(ctx context.Context, scope stage.ToolDispatchScope, call tool.Call, reporter tool.Reporter) (tool.Result, error) {
	return adapter.dispatch(ctx, scope, call, reporter)
}

func TestGuardAllowsDeniesAndKeepsDurableFactsSecretSafe(t *testing.T) {
	t.Parallel()
	implementation := &testTool{}
	var encoded []byte
	guard := mustGuard(t, permission.PolicyFunc(func(_ context.Context, facts permission.Facts) (permission.Decision, error) {
		var err error
		encoded, err = json.Marshal(facts)
		if err != nil {
			t.Fatal(err)
		}
		if facts.ToolName() != "write" || facts.Effect() != tool.EffectMutating ||
			facts.ReplaySafety() != tool.ReplayIdempotent ||
			len(facts.Capabilities()) != 1 || facts.Capabilities()[0] != tool.CapabilityFilesystemWrite {
			t.Fatalf("unexpected facts: %s", encoded)
		}
		if !strings.HasPrefix(facts.RunDigest(), "sha256:") ||
			!strings.HasPrefix(facts.CallDigest(), "sha256:") ||
			!strings.HasPrefix(facts.PlanDigest(), "sha256:") ||
			!strings.HasPrefix(facts.ToolDigest(), "sha256:") ||
			facts.DefinitionDigest() == "" || facts.PlanFingerprint() != fingerprint ||
			facts.WorkspaceFingerprint() != fingerprint {
			t.Fatalf("unexpected identity facts: %s", encoded)
		}
		capabilities := facts.Capabilities()
		capabilities[0] = tool.CapabilityNetworkAccess
		if facts.Capabilities()[0] != tool.CapabilityFilesystemWrite {
			t.Fatal("facts capabilities were mutable")
		}
		return permission.DecisionAllow, nil
	}), permission.Options{})
	result, err := dispatch(t, t.Context(), implementation, guard, standardRequester(true), "call-secret-token")
	if err != nil || result.CallID() != "call-secret-token" || implementation.calls.Load() != 1 {
		t.Fatalf("allow result/calls = %#v, %v / %d", result, err, implementation.calls.Load())
	}
	for _, secret := range []string{
		"run-C:/private/workspace", "call-secret-token", "generation:C:/private/plan", "C:/private/source.txt",
		"schema-secret", "argument-secret",
	} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("durable facts exposed %q: %s", secret, encoded)
		}
	}

	denied := mustGuard(t, permission.PolicyFunc(func(context.Context, permission.Facts) (permission.Decision, error) {
		return permission.DecisionDeny, nil
	}), permission.Options{})
	_, err = dispatch(t, t.Context(), implementation, denied, standardRequester(true), "call-denied")
	if !errors.Is(err, permission.ErrDenied) || implementation.calls.Load() != 1 {
		t.Fatalf("deny error/calls = %v / %d", err, implementation.calls.Load())
	}
}

func TestGuardPromptApprovalDenialAndExplicitFailureDefaults(t *testing.T) {
	t.Parallel()
	prompt := permission.PolicyFunc(func(context.Context, permission.Facts) (permission.Decision, error) {
		return permission.DecisionPrompt, nil
	})
	tests := []struct {
		name       string
		requester  interaction.Requester
		options    permission.Options
		wantCalls  uint32
		wantDenied bool
	}{
		{name: "approved", requester: standardRequester(true), wantCalls: 1},
		{name: "declined", requester: standardRequester(false), wantDenied: true},
		{name: "unavailable fails closed", requester: interaction.UnavailableRequester{}, wantDenied: true},
		{name: "malformed fails closed", requester: valueRequester(json.RawMessage(`"yes"`)), wantDenied: true},
		{name: "explicit allow default", requester: interaction.UnavailableRequester{}, options: permission.Options{PromptFailure: permission.DecisionAllow}, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			implementation := &testTool{}
			_, err := dispatch(t, t.Context(), implementation, mustGuard(t, prompt, test.options), test.requester, "call-prompt")
			if test.wantDenied != errors.Is(err, permission.ErrDenied) || implementation.calls.Load() != test.wantCalls {
				t.Fatalf("prompt error/calls = %v / %d", err, implementation.calls.Load())
			}
		})
	}
}

func TestGuardCancellationAndPanicFailWithoutExecutingOrLeaking(t *testing.T) {
	t.Parallel()
	t.Run("policy cancellation", func(t *testing.T) {
		implementation := &testTool{}
		ctx, cancel := context.WithCancel(t.Context())
		guard := mustGuard(t, permission.PolicyFunc(func(ctx context.Context, _ permission.Facts) (permission.Decision, error) {
			cancel()
			return "", ctx.Err()
		}), permission.Options{})
		_, err := dispatch(t, ctx, implementation, guard, standardRequester(true), "call-cancel-policy")
		if !errors.Is(err, context.Canceled) || implementation.calls.Load() != 0 {
			t.Fatalf("policy cancellation = %v / %d", err, implementation.calls.Load())
		}
	})
	t.Run("prompt cancellation overrides allow default", func(t *testing.T) {
		implementation := &testTool{}
		ctx, cancel := context.WithCancel(t.Context())
		requester := requesterFunc(func(ctx context.Context, _ interaction.Request) (interaction.Response, error) {
			cancel()
			<-ctx.Done()
			return interaction.Response{}, ctx.Err()
		})
		guard := mustGuard(t, permission.PolicyFunc(func(context.Context, permission.Facts) (permission.Decision, error) {
			return permission.DecisionPrompt, nil
		}), permission.Options{PromptFailure: permission.DecisionAllow})
		_, err := dispatch(t, ctx, implementation, guard, requester, "call-cancel-prompt")
		if !errors.Is(err, context.Canceled) || implementation.calls.Load() != 0 {
			t.Fatalf("prompt cancellation = %v / %d", err, implementation.calls.Load())
		}
	})
	t.Run("policy panic", func(t *testing.T) {
		implementation := &testTool{}
		guard := mustGuard(t, permission.PolicyFunc(func(context.Context, permission.Facts) (permission.Decision, error) {
			panic("C:/private/panic-secret")
		}), permission.Options{})
		_, err := dispatch(t, t.Context(), implementation, guard, standardRequester(true), "call-panic")
		if !errors.Is(err, permission.ErrPolicyFailed) || strings.Contains(err.Error(), "panic-secret") || implementation.calls.Load() != 0 {
			t.Fatalf("panic failure = %v / %d", err, implementation.calls.Load())
		}
	})
}

func TestEveryRetryAndRuntimeHostGenerationCrossesGuard(t *testing.T) {
	t.Parallel()
	implementation := &testTool{}
	implementation.fail.Store(1)
	var decisions atomic.Uint32
	guard := mustGuard(t, permission.PolicyFunc(func(context.Context, permission.Facts) (permission.Decision, error) {
		decisions.Add(1)
		return permission.DecisionAllow, nil
	}), permission.Options{})
	base, err := stage.NewDispatcher(map[string]tool.Tool{"write": implementation})
	if err != nil {
		t.Fatal(err)
	}
	retry := decoratorFunc(func(next stage.ToolDispatcher) stage.ToolDispatcher {
		return dispatchAdapter{ToolDispatcher: next, dispatch: func(ctx context.Context, scope stage.ToolDispatchScope, call tool.Call, reporter tool.Reporter) (tool.Result, error) {
			result, dispatchErr := next.Dispatch(ctx, scope, call, reporter)
			if dispatchErr == nil {
				return result, nil
			}
			return next.Dispatch(ctx, scope, call, reporter)
		}}
	})
	pipeline, err := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guard}, []stage.ToolDispatchDecorator{retry})
	if err != nil {
		t.Fatal(err)
	}
	call := mustCall(t, "call-retry")
	if _, err = pipeline.Dispatch(t.Context(), mustScope(t, "generation:retry", standardRequester(true)), call, nil); err != nil {
		t.Fatal(err)
	}
	if decisions.Load() != 2 || implementation.calls.Load() != 2 {
		t.Fatalf("retry decisions/calls = %d/%d, want 2/2", decisions.Load(), implementation.calls.Load())
	}

	compiled := &testTool{}
	compiledDispatcher, err := stage.NewDispatcher(map[string]tool.Tool{"write": compiled})
	if err != nil {
		t.Fatal(err)
	}
	host, err := pluginhost.NewHost(pluginhost.HostConfig{
		HostIdentity: &pluginv1.BuildIdentity{Component: "permission-conformance", Version: "v0", Commit: "test", Runtime: runtime.Version()},
		Compiled:     compiledDispatcher, Guards: []stage.ToolDispatchGuard{guard},
		Processes: process.LauncherFunc(func(context.Context, process.Spec) (process.Process, error) {
			return nil, errors.New("empty activation must not launch a process")
		}),
		Endpoints: localendpoint.NewFactory(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := host.Close(context.Background()); closeErr != nil {
			t.Error(closeErr)
		}
	})
	initial, err := host.LeaseCurrent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = initial.Release() }()
	if _, err = initial.Dispatcher().Dispatch(t.Context(), mustScope(t, initial.ToolPlanID(), standardRequester(true)), mustCall(t, "call-compiled"), nil); err != nil {
		t.Fatal(err)
	}
	empty, err := pluginhost.NewSet(nil)
	if err != nil {
		t.Fatal(err)
	}
	activatedID, err := host.Activate(t.Context(), empty)
	if err != nil || activatedID == initial.ToolPlanID() {
		t.Fatalf("activated generation = %q, %v", activatedID, err)
	}
	activated, err := host.LeaseCurrent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = activated.Release() }()
	if _, err = activated.Dispatcher().Dispatch(t.Context(), mustScope(t, activated.ToolPlanID(), standardRequester(true)), mustCall(t, "call-activated"), nil); err != nil {
		t.Fatal(err)
	}
	if decisions.Load() != 4 || compiled.calls.Load() != 2 {
		t.Fatalf("generation decisions/calls = %d/%d, want 4/2", decisions.Load(), compiled.calls.Load())
	}
}

func TestGuardIsConcurrentSafe(t *testing.T) {
	t.Parallel()
	implementation := &testTool{}
	var decisions atomic.Uint32
	guard := mustGuard(t, permission.PolicyFunc(func(context.Context, permission.Facts) (permission.Decision, error) {
		decisions.Add(1)
		return permission.DecisionAllow, nil
	}), permission.Options{})
	base, _ := stage.NewDispatcher(map[string]tool.Tool{"write": implementation})
	pipeline, _ := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guard}, nil)
	scope := mustScope(t, "generation:concurrent", standardRequester(true))
	var group sync.WaitGroup
	for index := range 64 {
		group.Add(1)
		go func() {
			defer group.Done()
			call, _ := tool.NewCall(tool.CallID(fmtCall(index)), "write", json.RawMessage(`{"value":"argument-secret"}`))
			if _, err := pipeline.Dispatch(t.Context(), scope, call, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if decisions.Load() != 64 || implementation.calls.Load() != 64 {
		t.Fatalf("concurrent decisions/calls = %d/%d", decisions.Load(), implementation.calls.Load())
	}
}

func TestGuardConstructionRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	if guard, err := permission.NewGuard(nil, permission.Options{}); err == nil || guard != nil {
		t.Fatalf("nil policy = %#v, %v", guard, err)
	}
	policy := permission.PolicyFunc(func(context.Context, permission.Facts) (permission.Decision, error) {
		return permission.DecisionAllow, nil
	})
	if guard, err := permission.NewGuard(policy, permission.Options{PromptFailure: permission.DecisionPrompt}); err == nil || guard != nil {
		t.Fatalf("prompt default = %#v, %v", guard, err)
	}
}

func dispatch(t *testing.T, ctx context.Context, implementation *testTool, guard stage.ToolDispatchGuard, requester interaction.Requester, callID tool.CallID) (tool.Result, error) {
	t.Helper()
	base, err := stage.NewDispatcher(map[string]tool.Tool{"write": implementation})
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guard}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pipeline.Dispatch(ctx, mustScope(t, "generation:C:/private/plan", requester), mustCall(t, callID), nil)
}

func mustGuard(t testing.TB, policy permission.Policy, options permission.Options) *permission.Guard {
	t.Helper()
	guard, err := permission.NewGuard(policy, options)
	if err != nil {
		t.Fatal(err)
	}
	return guard
}

func mustScope(t testing.TB, planID stage.PlanID, requester interaction.Requester) stage.ToolDispatchScope {
	t.Helper()
	authority, err := interaction.NewScope("run-C:/private/workspace")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := stage.NewToolDispatchScope(authority.RunID(), 1, planID, fingerprint, fingerprint, authority, requester)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func mustCall(t testing.TB, id tool.CallID) tool.Call {
	t.Helper()
	call, err := tool.NewCall(id, "write", json.RawMessage(`{"path":"C:/private/source.txt","token":"argument-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	return call
}

func standardRequester(approved bool) interaction.Requester {
	encoded, _ := json.Marshal(approved)
	return valueRequester(encoded)
}

func valueRequester(value json.RawMessage) interaction.Requester {
	return requesterFunc(func(_ context.Context, request interaction.Request) (interaction.Response, error) {
		if request.Kind() != "tool_permission" || !json.Valid(request.Schema()) || strings.Contains(request.Prompt(), "private") {
			return interaction.Response{}, errors.New("unsafe prompt")
		}
		return interaction.NewResponse(request.ID(), value)
	})
}

func fmtCall(index int) string {
	const digits = "0123456789"
	if index < 10 {
		return "call-concurrent-" + string(digits[index])
	}
	return "call-concurrent-" + string(digits[index/10]) + string(digits[index%10])
}

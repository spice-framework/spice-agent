package permission_test

import (
	"context"
	"testing"

	permission "github.com/spice-framework/spice-agent/experiments/permission"
	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

func BenchmarkGuardAllow(b *testing.B) {
	benchmarkGuard(b, permission.DecisionAllow, standardRequester(true))
}

func BenchmarkGuardDeny(b *testing.B) {
	benchmarkGuard(b, permission.DecisionDeny, standardRequester(true))
}

func BenchmarkGuardPrompt(b *testing.B) {
	benchmarkGuard(b, permission.DecisionPrompt, standardRequester(true))
}

func benchmarkGuard(b *testing.B, decision permission.Decision, requester interaction.Requester) {
	b.Helper()
	implementation := &testTool{}
	guard := mustGuard(b, permission.PolicyFunc(func(context.Context, permission.Facts) (permission.Decision, error) {
		return decision, nil
	}), permission.Options{})
	base, err := stage.NewDispatcher(map[string]tool.Tool{"write": implementation})
	if err != nil {
		b.Fatal(err)
	}
	pipeline, err := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guard}, nil)
	if err != nil {
		b.Fatal(err)
	}
	scope := mustScope(b, "generation:benchmark", requester)
	call := mustCall(b, "call-benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, _ = pipeline.Dispatch(context.Background(), scope, call, nil)
	}
}

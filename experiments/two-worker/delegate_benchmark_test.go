package twoworker

import (
	"context"
	"testing"

	"github.com/spice-framework/spice-agent/client"
	"github.com/spice-framework/spice-agent/tool"
)

func BenchmarkDelegateCompletedRun(b *testing.B) {
	session, reference := successfulSession(b, "worker handled benchmark")
	delegate, err := NewDelegate(session, Options{Definition: reference, MaximumEvents: 16})
	if err != nil {
		b.Fatal(err)
	}
	call := mustCall(b, "benchmark-complete", `{"task":"benchmark"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, executeErr := delegate.Execute(context.Background(), call, nil)
		if executeErr != nil || result.IsZero() {
			b.Fatalf("execute = %#v, %v", result, executeErr)
		}
	}
}

func BenchmarkDelegateCancellation(b *testing.B) {
	connection, reference := testConnection(b)
	run := mustRun(b, "run-benchmark-cancel")
	session := &scriptedSession{connection: connection}
	session.start = func(context.Context, client.StartRequest) (client.StartResult, error) {
		return client.NewStartResult(run, 1, "plan-benchmark", false)
	}
	session.events = func(context.Context, client.Cursor, client.EventStreamOptions) (client.EventStream, error) {
		return &frameStream{wait: true}, nil
	}
	delegate, err := NewDelegate(session, Options{Definition: reference, MaximumEvents: 16})
	if err != nil {
		b.Fatal(err)
	}
	call := mustCall(b, "benchmark-cancel", `{"task":"benchmark"}`)
	reporter := reporterFunc(func(_ context.Context, _ tool.Progress) error { return nil })
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ctx, cancel := context.WithCancel(context.Background())
		activeReporter := reporterFunc(func(reportContext context.Context, progress tool.Progress) error {
			cancel()
			return reporter.Report(reportContext, progress)
		})
		_, executeErr := delegate.Execute(ctx, call, activeReporter)
		cancel()
		if executeErr == nil {
			b.Fatal("canceled delegation succeeded")
		}
	}
}

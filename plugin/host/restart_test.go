package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

type fakeRecoveryClock struct {
	waits    chan time.Duration
	releases chan struct{}
}

func newFakeRecoveryClock() *fakeRecoveryClock {
	return &fakeRecoveryClock{waits: make(chan time.Duration, 16), releases: make(chan struct{}, 16)}
}

func (clock *fakeRecoveryClock) wait(ctx context.Context, duration time.Duration) error {
	select {
	case clock.waits <- duration:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-clock.releases:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (clock *fakeRecoveryClock) next(t *testing.T) time.Duration {
	t.Helper()
	select {
	case duration := <-clock.waits:
		return duration
	case <-time.After(time.Second):
		t.Fatal("recovery backoff did not begin")
		return 0
	}
}

func (clock *fakeRecoveryClock) release() { clock.releases <- struct{}{} }

type synchronizedStarter struct {
	mu sync.Mutex
	fn func(int, context.Context, Executable) (generationCandidate, error)
	n  int
}

func (starter *synchronizedStarter) start(
	ctx context.Context,
	executable Executable,
) (generationCandidate, error) {
	starter.mu.Lock()
	starter.n++
	call := starter.n
	starter.mu.Unlock()
	return starter.fn(call, ctx, executable)
}

func (starter *synchronizedStarter) calls() int {
	starter.mu.Lock()
	defer starter.mu.Unlock()
	return starter.n
}

func testRestartPolicy(t *testing.T, attempts uint32) RestartPolicy {
	t.Helper()
	policy, err := NewRestartPolicy(attempts, 10*time.Millisecond, 40*time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func newRecoveryTestHost(
	t *testing.T,
	policy RestartPolicy,
	clock recoveryClock,
	starter generationStarter,
) *Host {
	t.Helper()
	dispatcher, err := stage.NewDispatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := newHost(HostConfig{Compiled: dispatcher, Restart: policy}, nil, starter)
	if err != nil {
		t.Fatal(err)
	}
	host.clock = clock
	return host
}

func TestRestartPolicyValidationAndDefaults(t *testing.T) {
	t.Parallel()
	if (RestartPolicy{}).Enabled() {
		t.Fatal("zero restart policy is enabled")
	}
	if err := (RestartPolicy{}).Validate(); err != nil {
		t.Fatal(err)
	}
	policy := DefaultRestartPolicy()
	if !policy.Enabled() || policy.MaximumAttempts() != 3 || policy.InitialBackoff() != 250*time.Millisecond ||
		policy.MaximumBackoff() != time.Second || policy.AttemptTimeout() != 30*time.Second {
		t.Fatalf("default restart policy = %#v", policy)
	}
	for name, input := range map[string]struct {
		attempts                  uint32
		initial, maximum, timeout time.Duration
	}{
		"missing attempts":      {0, time.Second, time.Second, time.Second},
		"too many attempts":     {MaximumRestartAttempts + 1, time.Second, time.Second, time.Second},
		"missing initial":       {1, 0, time.Second, time.Second},
		"maximum below initial": {1, time.Second, time.Millisecond, time.Second},
		"missing timeout":       {1, time.Second, time.Second, 0},
		"initial too large":     {1, MaximumOperationTimeout + 1, MaximumOperationTimeout + 1, time.Second},
		"maximum too large":     {1, time.Second, MaximumOperationTimeout + 1, time.Second},
		"timeout too large":     {1, time.Second, time.Second, MaximumOperationTimeout + 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewRestartPolicy(input.attempts, input.initial, input.maximum, input.timeout); err == nil {
				t.Fatal("invalid restart policy succeeded")
			}
		})
	}
}

func TestHostRecoversCompleteSetWithDistinctGeneration(t *testing.T) {
	t.Parallel()
	clock := newFakeRecoveryClock()
	firstA := newFakeHostCandidate("first-a", map[string]tool.Tool{"runtime_a": newHostTestTool(t, "runtime_a", "first-a")})
	firstB := newFakeHostCandidate("first-b", map[string]tool.Tool{"runtime_b": newHostTestTool(t, "runtime_b", "first-b")})
	secondA := newFakeHostCandidate("second-a", map[string]tool.Tool{"runtime_a": newHostTestTool(t, "runtime_a", "second-a")})
	secondB := newFakeHostCandidate("second-b", map[string]tool.Tool{"runtime_b": newHostTestTool(t, "runtime_b", "second-b")})
	values := []generationCandidate{firstA, firstB, secondA, secondB}
	starter := &synchronizedStarter{fn: func(call int, _ context.Context, _ Executable) (generationCandidate, error) {
		return values[call-1], nil
	}}
	host := newRecoveryTestHost(t, testRestartPolicy(t, 3), clock, starter)
	desired, err := NewSet([]Executable{
		executableNamed(t, "runtime-a", "manifest.runtime-a"),
		executableNamed(t, "runtime-b", "manifest.runtime-b"),
	})
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := host.Activate(t.Context(), desired)
	if err != nil {
		t.Fatal(err)
	}
	oldLease, err := host.LeaseCurrent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	firstA.crash(errors.New("seeded private crash"))
	eventually(t, func() bool {
		lease, leaseErr := host.LeaseCurrent(t.Context())
		if leaseErr != nil {
			return false
		}
		defer func() { _ = lease.Release() }()
		return lease.ToolPlanID() != oldID &&
			dispatchHostTool(t, lease.Dispatcher(), "runtime_a") == `"second-a"` &&
			dispatchHostTool(t, lease.Dispatcher(), "runtime_b") == `"second-b"`
	})
	if oldLease.ToolPlanID() != oldID || dispatchHostTool(t, oldLease.Dispatcher(), "runtime_b") != `"first-b"` {
		t.Fatal("recovery changed the retained old generation")
	}
	_ = oldLease.Release()
	if got := host.Health(); got.State() != HealthStateReady || got.RestartAttempts() != 0 {
		t.Fatalf("recovered health = %v", got)
	}
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHostDisabledAndExhaustedRecoveryFailClosed(t *testing.T) {
	t.Parallel()
	t.Run("disabled", func(t *testing.T) {
		t.Parallel()
		candidate := newFakeHostCandidate("disabled", nil)
		starter := &synchronizedStarter{fn: func(_ int, _ context.Context, _ Executable) (generationCandidate, error) {
			return candidate, nil
		}}
		host := newRecoveryTestHost(t, RestartPolicy{}, systemRecoveryClock{}, starter)
		if _, err := host.Activate(t.Context(), oneExecutableSet(t, "disabled")); err != nil {
			t.Fatal(err)
		}
		candidate.crash(errors.New("private disabled crash"))
		eventually(t, func() bool { return host.Health().State() == HealthStateUnavailable })
		if starter.calls() != 1 {
			t.Fatalf("disabled restart calls = %d", starter.calls())
		}
		if err := host.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("exhausted", func(t *testing.T) {
		t.Parallel()
		clock := newFakeRecoveryClock()
		candidate := newFakeHostCandidate("exhausted", nil)
		starter := &synchronizedStarter{fn: func(call int, _ context.Context, _ Executable) (generationCandidate, error) {
			if call == 1 {
				return candidate, nil
			}
			return nil, errors.New("private staged failure")
		}}
		host := newRecoveryTestHost(t, testRestartPolicy(t, 3), clock, starter)
		if _, err := host.Activate(t.Context(), oneExecutableSet(t, "exhausted")); err != nil {
			t.Fatal(err)
		}
		candidate.crash(errors.New("private active crash"))
		for _, expected := range []time.Duration{10 * time.Millisecond, 20 * time.Millisecond} {
			if got := clock.next(t); got != expected {
				t.Fatalf("recovery backoff = %s, want %s", got, expected)
			}
			clock.release()
		}
		eventually(t, func() bool {
			health := host.Health()
			return health.State() == HealthStateUnavailable && health.RestartAttempts() == 3 &&
				slices.Contains(health.Issues(), HealthIssueRestartExhausted)
		})
		if starter.calls() != 4 {
			t.Fatalf("bounded restart calls = %d", starter.calls())
		}
		if err := host.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestHostExplicitActivationSupersedesStaleRecovery(t *testing.T) {
	t.Parallel()
	clock := newFakeRecoveryClock()
	old := newFakeHostCandidate("old", nil)
	newCandidate := newFakeHostCandidate("new", map[string]tool.Tool{"new": newHostTestTool(t, "new", "new")})
	recoveryStarted := make(chan struct{})
	var recoveryOnce sync.Once
	starter := &synchronizedStarter{fn: func(call int, ctx context.Context, executable Executable) (generationCandidate, error) {
		if call == 2 {
			recoveryOnce.Do(func() { close(recoveryStarted) })
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if executable.ID() == "old" {
			return old, nil
		}
		return newCandidate, nil
	}}
	host := newRecoveryTestHost(t, testRestartPolicy(t, 3), clock, starter)
	if _, err := host.Activate(t.Context(), oneExecutableSet(t, "old")); err != nil {
		t.Fatal(err)
	}
	old.crash(errors.New("private old crash"))
	<-recoveryStarted
	if _, err := host.LeaseCurrent(t.Context()); err == nil {
		t.Fatal("crashed generation admitted a lease while recovery was staging")
	}
	newID, err := host.Activate(t.Context(), oneExecutableSet(t, "new"))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := host.LeaseCurrent(t.Context())
	if err != nil || lease.ToolPlanID() != newID || dispatchHostTool(t, lease.Dispatcher(), "new") != `"new"` {
		t.Fatalf("explicit generation was superseded: %#v, %v", lease, err)
	}
	_ = lease.Release()
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHostFailedExplicitActivationRearmsPriorDesiredRecovery(t *testing.T) {
	t.Parallel()
	clock := newFakeRecoveryClock()
	first := newFakeHostCandidate("first", nil)
	recovered := newFakeHostCandidate("recovered", map[string]tool.Tool{"runtime": newHostTestTool(t, "runtime", "recovered")})
	recoveryStarted := make(chan struct{})
	starter := &synchronizedStarter{fn: func(call int, ctx context.Context, executable Executable) (generationCandidate, error) {
		switch {
		case call == 1:
			return first, nil
		case call == 2:
			close(recoveryStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		case executable.ID() == "replacement":
			return nil, errors.New("private explicit failure")
		default:
			return recovered, nil
		}
	}}
	host := newRecoveryTestHost(t, testRestartPolicy(t, 3), clock, starter)
	oldID, err := host.Activate(t.Context(), oneExecutableSet(t, "original"))
	if err != nil {
		t.Fatal(err)
	}
	first.crash(errors.New("private crash"))
	<-recoveryStarted
	if _, err = host.Activate(t.Context(), oneExecutableSet(t, "replacement")); err == nil {
		t.Fatal("failed explicit activation succeeded")
	}
	eventually(t, func() bool {
		lease, leaseErr := host.LeaseCurrent(t.Context())
		if leaseErr != nil {
			return false
		}
		defer func() { _ = lease.Release() }()
		return lease.ToolPlanID() != oldID && dispatchHostTool(t, lease.Dispatcher(), "runtime") == `"recovered"`
	})
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHostRecoveryAttemptsResetForNewCrashEpisode(t *testing.T) {
	t.Parallel()
	clock := newFakeRecoveryClock()
	first := newFakeHostCandidate("first", nil)
	second := newFakeHostCandidate("second", nil)
	third := newFakeHostCandidate("third", nil)
	values := []generationCandidate{first, second, third}
	starter := &synchronizedStarter{fn: func(call int, _ context.Context, _ Executable) (generationCandidate, error) {
		return values[call-1], nil
	}}
	host := newRecoveryTestHost(t, testRestartPolicy(t, 1), clock, starter)
	if _, err := host.Activate(t.Context(), oneExecutableSet(t, "runtime")); err != nil {
		t.Fatal(err)
	}
	first.crash(errors.New("first crash"))
	eventually(t, func() bool { return host.Health().State() == HealthStateReady && starter.calls() == 2 })
	second.crash(errors.New("second crash"))
	eventually(t, func() bool { return host.Health().State() == HealthStateReady && starter.calls() == 3 })
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRetiredGenerationCrashDoesNotStartRecovery(t *testing.T) {
	t.Parallel()
	clock := newFakeRecoveryClock()
	first := newFakeHostCandidate("first", nil)
	second := newFakeHostCandidate("second", nil)
	starter := &synchronizedStarter{fn: func(call int, _ context.Context, _ Executable) (generationCandidate, error) {
		if call == 1 {
			return first, nil
		}
		return second, nil
	}}
	host := newRecoveryTestHost(t, testRestartPolicy(t, 3), clock, starter)
	firstID, err := host.Activate(t.Context(), oneExecutableSet(t, "first"))
	if err != nil {
		t.Fatal(err)
	}
	retainer, err := host.LeaseGeneration(t.Context(), firstID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.Activate(t.Context(), oneExecutableSet(t, "second")); err != nil {
		t.Fatal(err)
	}
	first.crash(errors.New("retired crash"))
	eventually(t, func() bool {
		host.mu.Lock()
		defer host.mu.Unlock()
		generation := host.available[firstID]
		return generation != nil && generation.unhealthy != nil
	})
	if starter.calls() != 2 {
		t.Fatalf("retired crash started %d candidates", starter.calls())
	}
	_ = retainer.Release()
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

type countingMutatingTool struct {
	mu         sync.Mutex
	definition tool.Definition
	calls      int
}

func newCountingMutatingTool(t *testing.T) *countingMutatingTool {
	t.Helper()
	definition, err := tool.NewDefinition(
		"mutate", "mutating recovery test", json.RawMessage(`{"type":"object"}`),
		tool.EffectMutating, tool.ReplayUnsafe,
	)
	if err != nil {
		t.Fatal(err)
	}
	return &countingMutatingTool{definition: definition}
}

func (implementation *countingMutatingTool) Definition() tool.Definition {
	return implementation.definition.Clone()
}

func (implementation *countingMutatingTool) Execute(
	_ context.Context,
	call tool.Call,
	_ tool.Reporter,
) (tool.Result, error) {
	implementation.mu.Lock()
	implementation.calls++
	implementation.mu.Unlock()
	return tool.NewResult(call.ID(), json.RawMessage(`"mutated"`))
}

func (implementation *countingMutatingTool) count() int {
	implementation.mu.Lock()
	defer implementation.mu.Unlock()
	return implementation.calls
}

func TestHostRecoveryNeverReplaysMutatingCalls(t *testing.T) {
	t.Parallel()
	clock := newFakeRecoveryClock()
	mutation := newCountingMutatingTool(t)
	first := newFakeHostCandidate("first", map[string]tool.Tool{"mutate": mutation})
	second := newFakeHostCandidate("second", map[string]tool.Tool{"mutate": mutation})
	starter := &synchronizedStarter{fn: func(call int, _ context.Context, _ Executable) (generationCandidate, error) {
		if call == 1 {
			return first, nil
		}
		return second, nil
	}}
	host := newRecoveryTestHost(t, testRestartPolicy(t, 1), clock, starter)
	if _, err := host.Activate(t.Context(), oneExecutableSet(t, "mutating")); err != nil {
		t.Fatal(err)
	}
	lease, err := host.LeaseCurrent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := dispatchHostTool(t, lease.Dispatcher(), "mutate"); got != `"mutated"` {
		t.Fatalf("mutating result = %s", got)
	}
	first.crash(errors.New("crash after mutation"))
	eventually(t, func() bool { return host.Health().State() == HealthStateReady })
	if mutation.count() != 1 {
		t.Fatalf("recovery replayed mutating call %d times", mutation.count())
	}
	_ = lease.Release()
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHostRecoveryRetainsClonedDesiredSet(t *testing.T) {
	t.Parallel()
	first := newFakeHostCandidate("first", nil)
	second := newFakeHostCandidate("second", nil)
	startedIDs := make(chan string, 2)
	starter := &synchronizedStarter{fn: func(call int, _ context.Context, executable Executable) (generationCandidate, error) {
		startedIDs <- executable.ID()
		if call == 1 {
			return first, nil
		}
		return second, nil
	}}
	host := newRecoveryTestHost(t, testRestartPolicy(t, 1), newFakeRecoveryClock(), starter)
	desired := oneExecutableSet(t, "original")
	if _, err := host.Activate(t.Context(), desired); err != nil {
		t.Fatal(err)
	}
	desired.executables[0] = executableNamed(t, "caller-mutated", "manifest.caller-mutated")
	first.crash(errors.New("private crash"))
	eventually(t, func() bool { return host.Health().State() == HealthStateReady && starter.calls() == 2 })
	if firstID, secondID := <-startedIDs, <-startedIDs; firstID != "original" || secondID != "original" {
		t.Fatalf("retained desired ids = %q, %q", firstID, secondID)
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHostRecoveryAttemptDeadlineIsBounded(t *testing.T) {
	t.Parallel()
	policy, err := NewRestartPolicy(1, time.Millisecond, time.Millisecond, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	active := newFakeHostCandidate("active", nil)
	attemptStarted := make(chan struct{})
	starter := &synchronizedStarter{fn: func(call int, ctx context.Context, _ Executable) (generationCandidate, error) {
		if call == 1 {
			return active, nil
		}
		close(attemptStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	host := newRecoveryTestHost(t, policy, systemRecoveryClock{}, starter)
	if _, err = host.Activate(t.Context(), oneExecutableSet(t, "deadline")); err != nil {
		t.Fatal(err)
	}
	active.crash(errors.New("private crash"))
	<-attemptStarted
	eventually(t, func() bool {
		health := host.Health()
		return health.State() == HealthStateUnavailable && health.RestartAttempts() == 1
	})
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHostCloseCancelsRecoveryBackoff(t *testing.T) {
	t.Parallel()
	clock := newFakeRecoveryClock()
	active := newFakeHostCandidate("active", nil)
	starter := &synchronizedStarter{fn: func(call int, _ context.Context, _ Executable) (generationCandidate, error) {
		if call == 1 {
			return active, nil
		}
		return nil, errors.New("private restart failure")
	}}
	host := newRecoveryTestHost(t, testRestartPolicy(t, 3), clock, starter)
	if _, err := host.Activate(t.Context(), oneExecutableSet(t, "backoff")); err != nil {
		t.Fatal(err)
	}
	active.crash(errors.New("private crash"))
	if got := clock.next(t); got != 10*time.Millisecond {
		t.Fatalf("second-attempt backoff = %s", got)
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if starter.calls() != 2 {
		t.Fatalf("close admitted another recovery attempt: %d", starter.calls())
	}
}

func TestHostCleanupFailureHasOnlyFixedHealthIssue(t *testing.T) {
	t.Parallel()
	candidate := newFakeHostCandidate("cleanup-secret", nil)
	candidate.closeErrors = []error{errors.New("private cleanup one"), errors.New("private cleanup two")}
	starter := &synchronizedStarter{fn: func(_ int, _ context.Context, _ Executable) (generationCandidate, error) {
		return candidate, nil
	}}
	host := newRecoveryTestHost(t, RestartPolicy{}, systemRecoveryClock{}, starter)
	if _, err := host.Activate(t.Context(), oneExecutableSet(t, "cleanup")); err != nil {
		t.Fatal(err)
	}
	empty, err := NewSet(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.Activate(t.Context(), empty); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		health := host.Health()
		return health.State() == HealthStateDegraded &&
			len(health.Issues()) == 1 && health.Issues()[0] == HealthIssueCleanupFailed
	})
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHostConcurrentHealthLeaseCrashActivateAndClose(t *testing.T) {
	t.Parallel()
	active := newFakeHostCandidate("active", nil)
	staged := newFakeHostCandidate("staged", nil)
	replacement := newFakeHostCandidate("replacement", nil)
	recoveryStarted := make(chan struct{})
	starter := &synchronizedStarter{fn: func(call int, ctx context.Context, executable Executable) (generationCandidate, error) {
		switch {
		case call == 1:
			return active, nil
		case executable.ID() == "active":
			close(recoveryStarted)
			<-ctx.Done()
			return staged, ctx.Err()
		default:
			return replacement, nil
		}
	}}
	host := newRecoveryTestHost(t, testRestartPolicy(t, 3), newFakeRecoveryClock(), starter)
	if _, err := host.Activate(t.Context(), oneExecutableSet(t, "active")); err != nil {
		t.Fatal(err)
	}
	active.crash(errors.New("private crash"))
	<-recoveryStarted
	start := make(chan struct{})
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			<-start
			for range 100 {
				health := host.Health()
				_ = health.Validate()
				if lease, leaseErr := host.LeaseCurrent(context.Background()); leaseErr == nil {
					_ = lease.Release()
				}
			}
		})
	}
	close(start)
	if _, err := host.Activate(t.Context(), oneExecutableSet(t, "replacement")); err != nil {
		t.Fatal(err)
	}
	readers.Wait()
	results := make(chan error, 2)
	go func() { results <- host.Close(context.Background()) }()
	go func() { results <- host.Close(context.Background()) }()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

func TestHostCloseCancelsAndJoinsRecovery(t *testing.T) {
	t.Parallel()
	clock := newFakeRecoveryClock()
	candidate := newFakeHostCandidate("active", nil)
	staged := newFakeHostCandidate("staged", nil)
	recoveryStarted := make(chan struct{})
	starter := &synchronizedStarter{fn: func(call int, ctx context.Context, _ Executable) (generationCandidate, error) {
		if call == 1 {
			return candidate, nil
		}
		close(recoveryStarted)
		<-ctx.Done()
		return staged, ctx.Err()
	}}
	host := newRecoveryTestHost(t, testRestartPolicy(t, 3), clock, starter)
	if _, err := host.Activate(t.Context(), oneExecutableSet(t, "runtime")); err != nil {
		t.Fatal(err)
	}
	candidate.crash(errors.New("crash before close"))
	<-recoveryStarted
	closeContext, closeCancel := context.WithTimeout(t.Context(), time.Second)
	defer closeCancel()
	if err := host.Close(closeContext); err != nil {
		host.mu.Lock()
		t.Errorf("close state: activations=%d explicit=%d owned=%d controller=%t", host.activations, host.explicitActivations, len(host.owned), channelClosed(host.recoveryDone))
		for generation := range host.owned {
			t.Errorf("generation: refs=%d cleaning=%t cleaned=%t try=%d err=%v", generation.refs, generation.cleaning, generation.cleaned, generation.cleanupTry, generation.cleanupErr)
		}
		host.mu.Unlock()
		t.Fatal(err)
	}
	health := host.Health()
	if health.State() != HealthStateStopped || health.ActiveLeases() != 0 || health.RetainedGenerations() != 0 {
		t.Fatalf("closed recovery health = %v", health)
	}
	if starter.calls() != 2 {
		t.Fatalf("recovery launched after close: %d calls", starter.calls())
	}
}

func TestHostHealthIsImmutableValidAndSecretSafe(t *testing.T) {
	t.Parallel()
	const secret = "seeded-super-secret"
	candidate := newFakeHostCandidate(secret, nil)
	host := newRecoveryTestHost(t, RestartPolicy{}, systemRecoveryClock{}, fakeHostStarter{
		fn: func(context.Context, Executable) (generationCandidate, error) { return candidate, nil },
	})
	if _, err := host.Activate(t.Context(), oneExecutableSet(t, "health")); err != nil {
		t.Fatal(err)
	}
	candidate.crash(errors.New(secret))
	eventually(t, func() bool { return host.Health().State() == HealthStateUnavailable })
	health := host.Health()
	if err := health.Validate(); err != nil {
		t.Fatal(err)
	}
	issues := health.Issues()
	issues[0] = HealthIssueCleanupFailed
	if host.Health().Issues()[0] != HealthIssueCurrentGenerationStopped {
		t.Fatal("health issues were mutable")
	}
	encoded, err := json.Marshal(health)
	if err != nil {
		t.Fatal(err)
	}
	outputs := []string{
		health.String(), health.GoString(), fmt.Sprintf("%v", health), fmt.Sprintf("%+v", health),
		fmt.Sprintf("%#v", health), fmt.Sprintf("%s", health), fmt.Sprintf("%q", health), string(encoded),
	}
	for _, output := range outputs {
		if strings.Contains(output, secret) {
			t.Fatalf("health disclosed secret: %s", output)
		}
	}
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

package agent_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
)

type sequenceIdentitySource struct {
	mu     sync.Mutex
	values []string
}

func (source *sequenceIdentitySource) Next(string) (string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	value := source.values[0]
	source.values = source.values[1:]
	return value, nil
}

type gatedIdentityStream struct {
	release   <-chan struct{}
	completed model.StreamEvent
	delivered bool
}

func (stream *gatedIdentityStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	if stream.delivered {
		return model.StreamEvent{}, io.EOF
	}
	select {
	case <-ctx.Done():
		return model.StreamEvent{}, ctx.Err()
	case <-stream.release:
		stream.delivered = true
		return stream.completed, nil
	}
}

func (*gatedIdentityStream) Close() error { return nil }

type terminalThenGatedProvider struct {
	mu        sync.Mutex
	completed model.StreamEvent
	release   <-chan struct{}
	calls     int
}

func (provider *terminalThenGatedProvider) Stream(
	context.Context,
	model.Request,
) (model.Stream, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.calls++
	switch provider.calls {
	case 1:
		return &scriptedStream{events: []model.StreamEvent{provider.completed}}, nil
	case 2:
		return &gatedIdentityStream{release: provider.release, completed: provider.completed}, nil
	default:
		return nil, errors.New("unexpected identity provider call")
	}
}

func identityEngine(
	t *testing.T,
	provider model.Provider,
	ids agent.IDSource,
	limits agent.RunIdentityLimits,
) *agent.Engine {
	t.Helper()
	dispatcher, err := stage.NewDispatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	options := agent.DefaultEngineOptions()
	options.RunIdentityLimits = limits
	engine, err := agent.NewEngineWithOptions(provider, dispatcher, ids, time.Now, nil, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestRunIdentityLimitsAreExactAndBounded(t *testing.T) {
	defaults := agent.DefaultRunIdentityLimits()
	if defaults.Entries() != 65_536 || defaults.Bytes() != 16<<20 || defaults.Validate() != nil {
		t.Fatalf("default limits = %#v", defaults)
	}
	for _, test := range []struct {
		entries uint32
		bytes   uint64
	}{
		{0, 129},
		{1_048_577, 129},
		{1, 128},
		{1, 256<<20 + 1},
	} {
		if _, err := agent.NewRunIdentityLimits(test.entries, test.bytes); err == nil {
			t.Fatalf("invalid limits (%d, %d) succeeded", test.entries, test.bytes)
		}
	}
	if _, err := agent.NewRunIdentityLimits(1_048_576, 256<<20); err != nil {
		t.Fatalf("hard limits rejected: %v", err)
	}
}

func TestRunIdentityPreparationReservesAndAbortReclaimsExactCapacity(t *testing.T) {
	limits, _ := agent.NewRunIdentityLimits(1, 1<<20)
	ids := &sequenceIdentitySource{values: []string{"identity-1", "identity-2", "identity-3"}}
	engine := identityEngine(t, blockingProvider{}, ids, limits)
	definition, _ := agent.NewDefinition("identity", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))

	prepared, err := engine.PrepareStart(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	stats := engine.RunIdentityStats()
	if stats.Reserved() != 1 || stats.Active() != 0 || stats.Tombstones() != 0 ||
		stats.Entries() != 1 || stats.Bytes() != 128+uint64(len(prepared.RunID())) {
		t.Fatalf("reserved stats = %#v", stats)
	}
	if _, err = engine.PrepareStart(t.Context(), definition, input); !errors.Is(err, agent.ErrRunIdentityCapacity) {
		t.Fatalf("capacity error = %v", err)
	}
	capacity, ok := errors.AsType[*agent.RunIdentityCapacityError](err)
	if !ok || capacity.Resource() != "entries" || capacity.Limit() != 1 || capacity.Observed() != 2 ||
		capacity.Stats().Reserved() != 1 {
		t.Fatalf("typed capacity = %#v", capacity)
	}
	if err = prepared.Abort(); err != nil {
		t.Fatal(err)
	}
	if stats = engine.RunIdentityStats(); stats.Entries() != 0 || stats.Bytes() != 0 {
		t.Fatalf("aborted stats = %#v", stats)
	}
	prepared, err = engine.PrepareStart(t.Context(), definition, input)
	if err != nil {
		t.Fatalf("reservation after abort: %v", err)
	}
	if err = prepared.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunIdentityByteCapacityUsesFixedChargePlusID(t *testing.T) {
	limits, _ := agent.NewRunIdentityLimits(2, 134)
	ids := &sequenceIdentitySource{values: []string{"run-aa", "run-bb"}}
	engine := identityEngine(t, blockingProvider{}, ids, limits)
	definition, _ := agent.NewDefinition("identity", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))
	prepared, err := engine.PrepareStart(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := prepared.Close(); closeErr != nil {
			t.Errorf("close byte-capacity preparation: %v", closeErr)
		}
	})
	if _, err = engine.PrepareStart(t.Context(), definition, input); !errors.Is(err, agent.ErrRunIdentityCapacity) {
		t.Fatalf("byte capacity error = %v", err)
	}
	capacity, ok := errors.AsType[*agent.RunIdentityCapacityError](err)
	if !ok || capacity.Resource() != "bytes" || capacity.Limit() != 134 || capacity.Observed() != 268 {
		t.Fatalf("typed byte capacity = %#v", capacity)
	}
}

func TestRunIdentityTerminalRetirementIsExactTokenFencedAndIdempotent(t *testing.T) {
	completedEvent, _ := model.Completed(model.NewUsage(1, 1))
	releaseSecond := make(chan struct{})
	provider := &terminalThenGatedProvider{completed: completedEvent, release: releaseSecond}
	limits, _ := agent.NewRunIdentityLimits(1, 1<<20)
	engine := identityEngine(t, provider, fixedIDSource{value: "same-run"}, limits)
	definition, _ := agent.NewDefinition("identity", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))

	first, err := engine.Start(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if stats := engine.RunIdentityStats(); stats.Tombstones() != 1 || stats.Active() != 0 {
		t.Fatalf("terminal stats = %#v", stats)
	}
	if _, err = engine.Start(t.Context(), definition, input); err == nil ||
		errors.Is(err, agent.ErrRunIdentityCapacity) || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate did not precede saturated capacity: %v", err)
	}
	firstRetirement := first.TerminalIdentityRetirement()
	if firstRetirement == nil || firstRetirement.String() != "agent run identity retirement" {
		t.Fatalf("retirement = %#v", firstRetirement)
	}
	if err = firstRetirement.Retire(); err != nil {
		t.Fatal(err)
	}
	if err = firstRetirement.Retire(); err != nil {
		t.Fatalf("repeated retirement: %v", err)
	}

	second, err := engine.Start(t.Context(), definition, input)
	if err != nil {
		t.Fatalf("start after retirement: %v", err)
	}
	if err = firstRetirement.Retire(); err != nil {
		t.Fatalf("old handle idempotence: %v", err)
	}
	if stats := engine.RunIdentityStats(); stats.Active() != 1 || stats.Entries() != 1 {
		t.Fatalf("old token altered new generation: %#v", stats)
	}
	if _, err = engine.Start(t.Context(), definition, input); err == nil {
		t.Fatal("old retirement removed the active generation")
	}
	close(releaseSecond)
	if err = second.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = second.TerminalIdentityRetirement().Retire(); err != nil {
		t.Fatal(err)
	}
	if stats := engine.RunIdentityStats(); stats.Entries() != 0 || stats.Bytes() != 0 {
		t.Fatalf("retired stats = %#v", stats)
	}
}

func TestRunIdentityConcurrentSameIDPreparationHasOneExactWinner(t *testing.T) {
	limits, _ := agent.NewRunIdentityLimits(1, 1<<20)
	engine := identityEngine(t, blockingProvider{}, fixedIDSource{value: "contended-run"}, limits)
	definition, _ := agent.NewDefinition("identity", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))
	start := make(chan struct{})
	results := make(chan struct {
		prepared *agent.PreparedStart
		err      error
	}, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Go(func() {
			<-start
			prepared, err := engine.PrepareStart(t.Context(), definition, input)
			results <- struct {
				prepared *agent.PreparedStart
				err      error
			}{prepared, err}
		})
	}
	close(start)
	wait.Wait()
	close(results)
	winners := 0
	duplicates := 0
	for result := range results {
		if result.err == nil {
			winners++
			if err := result.prepared.Abort(); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if errors.Is(result.err, agent.ErrRunIdentityCapacity) || !strings.Contains(result.err.Error(), "duplicate") {
			t.Fatalf("loser error = %v", result.err)
		}
		duplicates++
	}
	if winners != 1 || duplicates != 1 || engine.RunIdentityStats().Entries() != 0 {
		t.Fatalf("winners=%d duplicates=%d stats=%#v", winners, duplicates, engine.RunIdentityStats())
	}
}

func TestRunIdentitySnapshotPreparationAbortPreservesByteIdenticalRetry(t *testing.T) {
	engine, source, snapshot, planID := preparedResumeFixture(t, "identity-snapshot", blockingProvider{}, nil)
	want, err := snapshot.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := engine.PrepareResumeSnapshot(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if stats := engine.RunIdentityStats(); stats.Reserved() != 1 || stats.Active() != 0 {
		t.Fatalf("snapshot reservation = %#v", stats)
	}
	if err = prepared.Abort(); err != nil {
		t.Fatal(err)
	}
	got, err := snapshot.MarshalBinary()
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("snapshot changed across abort: equal=%v err=%v", bytes.Equal(got, want), err)
	}
	retry, err := engine.PrepareResumeSnapshot(t.Context(), snapshot)
	if err != nil || retry.RunID() != snapshot.RunID() {
		t.Fatalf("byte-identical retry = %#v, %v", retry, err)
	}
	if err = retry.Close(); err != nil || source.releaseCount(planID) != 2 || engine.RunIdentityStats().Entries() != 0 {
		t.Fatalf("retry cleanup = %v releases=%d stats=%#v", err, source.releaseCount(planID), engine.RunIdentityStats())
	}
}

func TestRunIdentityRetirementRejectsActiveAndIsRaceIdempotent(t *testing.T) {
	limits, _ := agent.NewRunIdentityLimits(1, 1<<20)
	engine := identityEngine(t, blockingProvider{}, &agent.AtomicIDSource{}, limits)
	run := startRun(t, engine, 1)
	retirement := run.TerminalIdentityRetirement()
	if err := retirement.Retire(); err == nil {
		t.Fatal("active retirement succeeded")
	}
	run.Cancel()
	_ = run.Wait(t.Context())

	var wait sync.WaitGroup
	errorsSeen := make(chan error, 64)
	for range 64 {
		wait.Go(func() { errorsSeen <- retirement.Retire() })
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Errorf("concurrent retirement: %v", err)
		}
	}
	if stats := engine.RunIdentityStats(); stats.Entries() != 0 {
		t.Fatalf("race retirement stats = %#v", stats)
	}
}

func TestRunIdentityInertRegistrationActivatesOrReclaimsReservation(t *testing.T) {
	limits, _ := agent.NewRunIdentityLimits(1, 1<<20)
	engine := identityEngine(t, blockingProvider{}, fixedIDSource{value: "inert-run"}, limits)
	definition, _ := agent.NewDefinition("identity", "model", 1)
	input, _ := agent.NewInput(inputMessage(t))
	prepared, err := engine.PrepareStart(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := prepared.CommitPaused(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stats := engine.RunIdentityStats(); stats.Reserved() != 1 || stats.Active() != 0 {
		t.Fatalf("inert registration stats = %#v", stats)
	}
	if err = paused.Abort(t.Context()); err != nil {
		t.Fatal(err)
	}
	if stats := engine.RunIdentityStats(); stats.Entries() != 0 {
		t.Fatalf("inert abort stats = %#v", stats)
	}

	prepared, err = engine.PrepareStart(t.Context(), definition, input)
	if err != nil {
		t.Fatalf("reuse after inert abort: %v", err)
	}
	paused, err = prepared.CommitPaused(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	run, err := paused.Activate()
	if err != nil {
		t.Fatal(err)
	}
	if stats := engine.RunIdentityStats(); stats.Active() != 1 || stats.Reserved() != 0 {
		t.Fatalf("activated stats = %#v", stats)
	}
	run.Cancel()
	_ = run.Wait(t.Context())
	if err = run.TerminalIdentityRetirement().Retire(); err != nil {
		t.Fatal(err)
	}
}

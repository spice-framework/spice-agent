package daemon_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/daemon"
)

func TestLedgerOwnerCanDefinitivelyAbandonThenRetry(t *testing.T) {
	t.Parallel()
	ledger, err := daemon.NewLedger(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	digest := daemon.CanonicalDigest([]byte("input"))
	cause := errors.New("safe retryable precommit failure")
	var calls atomic.Int32
	outcome, duplicate, err := ledger.Do(t.Context(), "client", "operation", "start", digest, func(context.Context) (daemon.Outcome, error) {
		calls.Add(1)
		return daemon.Outcome{}, daemon.AbandonOperation(cause)
	})
	if duplicate || outcome.Kind() != "" || !errors.Is(err, daemon.ErrOperationAbandoned) || !errors.Is(err, cause) {
		t.Fatalf("abandoned owner = kind %q, duplicate %v, error %v", outcome.Kind(), duplicate, err)
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatal("abandonment Error text exposed its caller-owned cause")
	}

	want, err := daemon.NewOutcome(daemon.OutcomeSuccess, []byte("committed"))
	if err != nil {
		t.Fatal(err)
	}
	outcome, duplicate, err = ledger.Do(t.Context(), "client", "operation", "start", digest, func(context.Context) (daemon.Outcome, error) {
		calls.Add(1)
		return want, nil
	})
	if duplicate || err != nil || outcome.Kind() != daemon.OutcomeSuccess || string(outcome.Payload()) != "committed" || calls.Load() != 2 {
		t.Fatalf("retry = kind %q, duplicate %v, error %v, calls %d", outcome.Kind(), duplicate, err, calls.Load())
	}
}

func TestLedgerWaitingDuplicatesRetryExactlyOnceAfterAbandonment(t *testing.T) {
	t.Parallel()
	ledger, _ := daemon.NewLedger(1, 1)
	digest := daemon.CanonicalDigest([]byte("input"))
	ownerStarted := make(chan struct{})
	releaseOwner := make(chan struct{})
	ownerResult := make(chan ledgerAbandonResult, 1)
	go func() {
		outcome, duplicate, err := ledger.Do(t.Context(), "client", "operation", "start", digest, func(context.Context) (daemon.Outcome, error) {
			close(ownerStarted)
			<-releaseOwner
			return daemon.Outcome{}, daemon.AbandonOperation(errors.New("safe owner cause"))
		})
		ownerResult <- ledgerAbandonResult{outcome: outcome, duplicate: duplicate, err: err}
	}()
	<-ownerStarted

	const waiterCount = 8
	waiterContexts := make([]*observedContext, waiterCount)
	waiterResults := make(chan ledgerAbandonResult, waiterCount)
	var retryCalls atomic.Int32
	for index := range waiterCount {
		waiterContexts[index] = newObservedContext(context.Background())
		go func(waiterContext context.Context) {
			outcome, duplicate, err := ledger.Do(waiterContext, "client", "operation", "start", digest, func(context.Context) (daemon.Outcome, error) {
				retryCalls.Add(1)
				value, valueErr := daemon.NewOutcome(daemon.OutcomeSuccess, []byte("retried"))
				return value, valueErr
			})
			waiterResults <- ledgerAbandonResult{outcome: outcome, duplicate: duplicate, err: err}
		}(waiterContexts[index])
	}
	for _, waiterContext := range waiterContexts {
		<-waiterContext.observed
	}
	close(releaseOwner)

	owner := <-ownerResult
	if owner.duplicate || !errors.Is(owner.err, daemon.ErrOperationAbandoned) {
		t.Fatalf("owner abandonment = %#v", owner)
	}
	retryOwners := 0
	for range waiterCount {
		waiter := <-waiterResults
		if waiter.err != nil || string(waiter.outcome.Payload()) != "retried" {
			t.Fatalf("waiter retry = %#v", waiter)
		}
		if !waiter.duplicate {
			retryOwners++
		}
	}
	if retryOwners != 1 || retryCalls.Load() != 1 {
		t.Fatalf("retry owners = %d, executor calls = %d; want exactly one of each", retryOwners, retryCalls.Load())
	}
	replayed, duplicate, err := ledger.Do(t.Context(), "client", "operation", "start", digest, panicExecutor)
	if !duplicate || err != nil || string(replayed.Payload()) != "retried" || retryCalls.Load() != 1 {
		t.Fatalf("committed retry replay = %q, duplicate %v, error %v, calls %d", replayed.Payload(), duplicate, err, retryCalls.Load())
	}
}

func TestLedgerCancelledDuplicateDoesNotRetryAbandonedWork(t *testing.T) {
	t.Parallel()
	ledger, _ := daemon.NewLedger(1, 1)
	digest := daemon.CanonicalDigest([]byte("input"))
	ownerStarted := make(chan struct{})
	releaseOwner := make(chan struct{})
	ownerDone := make(chan error, 1)
	go func() {
		_, _, err := ledger.Do(t.Context(), "client", "operation", "start", digest, func(context.Context) (daemon.Outcome, error) {
			close(ownerStarted)
			<-releaseOwner
			return daemon.Outcome{}, daemon.AbandonOperation(errors.New("safe owner cause"))
		})
		ownerDone <- err
	}()
	<-ownerStarted

	waiterBase, cancelWaiter := context.WithCancel(context.Background())
	waiterContext := newObservedContext(waiterBase)
	waiterDone := make(chan ledgerAbandonResult, 1)
	var retryCalls atomic.Int32
	go func() {
		outcome, duplicate, err := ledger.Do(waiterContext, "client", "operation", "start", digest, func(context.Context) (daemon.Outcome, error) {
			retryCalls.Add(1)
			return daemon.NewOutcome(daemon.OutcomeSuccess, []byte("unexpected"))
		})
		waiterDone <- ledgerAbandonResult{outcome: outcome, duplicate: duplicate, err: err}
	}()
	<-waiterContext.observed
	cancelWaiter()
	waiter := <-waiterDone
	if !waiter.duplicate || !errors.Is(waiter.err, context.Canceled) || retryCalls.Load() != 0 {
		t.Fatalf("cancelled waiter = %#v, calls %d", waiter, retryCalls.Load())
	}
	close(releaseOwner)
	if err := <-ownerDone; !errors.Is(err, daemon.ErrOperationAbandoned) {
		t.Fatalf("owner abandonment = %v", err)
	}
}

func TestLedgerAbandonmentReclaimsCapacityAndFailsClosed(t *testing.T) {
	t.Parallel()
	ledger, _ := daemon.NewLedger(1, 1)
	firstDigest := daemon.CanonicalDigest([]byte("first"))
	ownerStarted := make(chan struct{})
	releaseOwner := make(chan struct{})
	ownerDone := make(chan error, 1)
	go func() {
		_, _, err := ledger.Do(t.Context(), "first-client", "operation", "start", firstDigest, func(context.Context) (daemon.Outcome, error) {
			close(ownerStarted)
			<-releaseOwner
			return daemon.Outcome{}, daemon.AbandonOperation(errors.New("safe owner cause"))
		})
		ownerDone <- err
	}()
	<-ownerStarted
	if _, _, err := ledger.Do(
		t.Context(), "first-client", "operation", "start", daemon.CanonicalDigest([]byte("conflict")), panicExecutor,
	); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflicting pending input = %v", err)
	}
	close(releaseOwner)
	if err := <-ownerDone; !errors.Is(err, daemon.ErrOperationAbandoned) {
		t.Fatalf("owner abandonment = %v", err)
	}
	other, duplicate, err := ledger.Do(
		t.Context(), "second-client", "operation", "start", firstDigest,
		func(context.Context) (daemon.Outcome, error) {
			return daemon.NewOutcome(daemon.OutcomeSuccess, []byte("capacity reclaimed"))
		},
	)
	if duplicate || err != nil || string(other.Payload()) != "capacity reclaimed" {
		t.Fatalf("reclaimed client capacity = %q, duplicate %v, error %v", other.Payload(), duplicate, err)
	}

	ambiguous, _ := daemon.NewOutcome(daemon.OutcomeSuccess, []byte("ambiguous"))
	tests := []struct {
		name    string
		outcome daemon.Outcome
		err     error
	}{
		{name: "arbitrary error", err: errors.New("executor detail")},
		{name: "nil abandon cause", err: daemon.AbandonOperation(nil)},
		{name: "wrapped abandonment", err: fmt.Errorf("wrapped: %w", daemon.AbandonOperation(errors.New("safe")))},
		{name: "abandonment with result", outcome: ambiguous, err: daemon.AbandonOperation(errors.New("safe"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			failureLedger, _ := daemon.NewLedger(1, 1)
			outcome, duplicate, operationErr := failureLedger.Do(
				t.Context(), "client", "operation", "start", daemon.CanonicalDigest([]byte(test.name)),
				func(context.Context) (daemon.Outcome, error) { return test.outcome, test.err },
			)
			if duplicate || !errors.Is(operationErr, daemon.ErrOperationExecutor) || outcome.Kind() != daemon.OutcomeUncertain {
				t.Fatalf("fail-closed owner = kind %q, duplicate %v, error %v", outcome.Kind(), duplicate, operationErr)
			}
			replayed, replayDuplicate, replayErr := failureLedger.Do(
				t.Context(), "client", "operation", "start", daemon.CanonicalDigest([]byte(test.name)), panicExecutor,
			)
			if !replayDuplicate || !errors.Is(replayErr, daemon.ErrOperationExecutor) || replayed.Kind() != daemon.OutcomeUncertain {
				t.Fatalf("fail-closed replay = kind %q, duplicate %v, error %v", replayed.Kind(), replayDuplicate, replayErr)
			}
		})
	}
}

type ledgerAbandonResult struct {
	outcome   daemon.Outcome
	duplicate bool
	err       error
}

type observedContext struct {
	deadline func() (time.Time, bool)
	done     func() <-chan struct{}
	err      func() error
	value    func(any) any
	observed chan struct{}
	once     sync.Once
}

func newObservedContext(parent context.Context) *observedContext {
	return &observedContext{
		deadline: parent.Deadline,
		done:     parent.Done,
		err:      parent.Err,
		value:    parent.Value,
		observed: make(chan struct{}),
	}
}

func (ctx *observedContext) Deadline() (time.Time, bool) { return ctx.deadline() }

func (ctx *observedContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.done()
}

func (ctx *observedContext) Err() error        { return ctx.err() }
func (ctx *observedContext) Value(key any) any { return ctx.value(key) }

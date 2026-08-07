package event

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestNonnegativeUint64FailsClosed(t *testing.T) {
	t.Parallel()
	if got := nonnegativeUint64(-1); got != 0 {
		t.Fatalf("negative conversion = %d", got)
	}
	if got := nonnegativeUint64(math.MaxInt); got != uint64(math.MaxInt) {
		t.Fatalf("maximum conversion = %d", got)
	}
}

func TestExhaustionFactsDoNotOverflowPlatformIntegers(t *testing.T) {
	t.Parallel()
	first, err := Reconstruct("run", 1, time.Unix(1, 0).UTC(), ModelDelta, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Reconstruct("run", 2, time.Unix(2, 0).UTC(), ModelDelta, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("replay page uses subtraction before addition", func(t *testing.T) {
		t.Parallel()
		log := &Log{
			limits: LogLimits{SubscriberMaxEvents: 2, SubscriberMaxBytes: math.MaxInt},
			entries: []logEntry{
				{envelope: first, bytes: math.MaxInt - 10},
				{envelope: second, bytes: 20},
			},
			lastSequence: 2,
		}
		entries, page, replayErr := log.replayPageLocked(ReplayRequest{
			MaxEvents: 2,
			MaxBytes:  math.MaxInt,
		})
		if replayErr != nil || len(entries) != 1 || !page.HasMore || page.PageLastSequence != 1 {
			t.Fatalf("overflow-bound replay = %#v, %#v, %v", entries, page, replayErr)
		}
	})

	t.Run("replay exhaustion preserves an observation above max int", func(t *testing.T) {
		t.Parallel()
		log := &Log{
			limits:  LogLimits{SubscriberMaxEvents: math.MaxInt, SubscriberMaxBytes: math.MaxInt},
			entries: []logEntry{{envelope: second, bytes: 20}},
		}
		failure, replayErr := log.replayExhaustionLocked(1, []logEntry{{
			envelope: first,
			bytes:    math.MaxInt - 10,
		}})
		wantObserved := uint64(math.MaxInt) + 10
		if replayErr != nil || failure.Resource() != "event_replay_bytes" ||
			failure.Limit() != uint64(math.MaxInt) || failure.Observed() != wantObserved {
			t.Fatalf("overflow-bound exhaustion = %#v, %v; want observed %d", failure, replayErr, wantObserved)
		}
	})

	t.Run("subscription exhaustion preserves an observation above max int", func(t *testing.T) {
		t.Parallel()
		subscription := newSubscription(t.Context(), 0, LogLimits{
			SubscriberMaxEvents: 1,
			SubscriberMaxBytes:  math.MaxInt,
		}, nil)
		subscription.queuedBytes = math.MaxInt - 10
		if !subscription.offer(logEntry{envelope: first, bytes: 20}) {
			t.Fatal("overflow-bound subscription accepted")
		}
		failure, ok := errors.AsType[*ResourceExhaustedError](subscription.err)
		wantObserved := uint64(math.MaxInt) + 10
		if !ok || failure.Resource() != "event_subscription_bytes" ||
			failure.Limit() != uint64(math.MaxInt) || failure.Observed() != wantObserved {
			t.Fatalf("overflow-bound subscription = %#v; want observed %d", failure, wantObserved)
		}
	})
}

func TestDeliveryCommitReconcilesConcurrentCancellationAndExhaustion(t *testing.T) {
	t.Parallel()
	first, err := Reconstruct("run", 1, time.Unix(1, 0).UTC(), ModelDelta, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Reconstruct("run", 2, time.Unix(2, 0).UTC(), ModelDelta, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("cancellation after receive", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		limits := DefaultLogLimits()
		subscription := newSubscription(
			ctx, 0, limits, []logEntry{{envelope: first, bytes: first.EncodedSize()}},
		)
		committed := make(chan struct{})
		continueDelivery := make(chan struct{})
		go subscription.deliverWithAfterSend(func() {
			close(committed)
			<-continueDelivery
		})
		if envelope := <-subscription.Events(); envelope.Sequence() != 1 {
			t.Fatalf("delivered sequence = %d", envelope.Sequence())
		}
		<-committed
		cancel()
		waitForSubscriptionTerminal(t, subscription)
		close(continueDelivery)
		if waitErr := subscription.Wait(t.Context()); !errors.Is(waitErr, context.Canceled) {
			t.Fatalf("cancellation = %v", waitErr)
		}
		if subscription.LastDelivered() != 1 {
			t.Fatalf("cancellation cursor = %d", subscription.LastDelivered())
		}
		if _, open := <-subscription.Events(); open {
			t.Fatal("canceled subscription delivered a duplicate")
		}
	})

	t.Run("exhaustion after receive", func(t *testing.T) {
		t.Parallel()
		limits := DefaultLogLimits()
		limits.SubscriberMaxEvents = 1
		subscription := newSubscription(
			t.Context(), 0, limits,
			[]logEntry{{envelope: first, bytes: first.EncodedSize()}},
		)
		committed := make(chan struct{})
		continueDelivery := make(chan struct{})
		go subscription.deliverWithAfterSend(func() {
			close(committed)
			<-continueDelivery
		})
		if envelope := <-subscription.Events(); envelope.Sequence() != 1 {
			t.Fatalf("delivered sequence = %d", envelope.Sequence())
		}
		<-committed
		if disconnected := subscription.offer(logEntry{envelope: second, bytes: second.EncodedSize()}); !disconnected {
			t.Fatal("full in-flight queue did not disconnect")
		}
		close(continueDelivery)
		waitErr := subscription.Wait(t.Context())
		var exhausted *ResourceExhaustedError
		if !errors.As(waitErr, &exhausted) || exhausted.LastDelivered != 1 ||
			subscription.LastDelivered() != 1 {
			t.Fatalf("exhaustion cursor = %#v, %d, %v", exhausted, subscription.LastDelivered(), waitErr)
		}
		if _, open := <-subscription.Events(); open {
			t.Fatal("exhausted subscription delivered a duplicate")
		}
	})
}

func waitForSubscriptionTerminal(t *testing.T, subscription *Subscription) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		subscription.mu.Lock()
		terminal := subscription.terminal
		subscription.mu.Unlock()
		if terminal {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("subscription did not become terminal")
}

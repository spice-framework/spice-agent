package event

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
			ctx, 0, limits, []logEntry{{envelope: first, bytes: first.EncodedSize()}}, nil,
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
			[]logEntry{{envelope: first, bytes: first.EncodedSize()}}, nil,
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

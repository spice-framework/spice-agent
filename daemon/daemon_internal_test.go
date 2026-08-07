package daemon

import (
	"context"
	"errors"
	"math"
	"runtime"
	"testing"

	"github.com/spice-framework/spice-agent/interaction"
)

func TestSessionEpochOverflowDoesNotChangeOwnership(t *testing.T) {
	store, err := NewSessionStore(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	state, exists := store.sessions[session.ClientID()]
	if !exists || state == nil {
		store.mu.Unlock()
		t.Fatal("fresh session state was not stored")
		return
	}
	state.epoch = math.MaxUint64
	store.mu.Unlock()
	if _, err = store.Reconnect(session.ClientID(), math.MaxUint64); err == nil {
		t.Fatal("epoch overflow succeeded")
	}
	if err = store.Check(session.ClientID(), math.MaxUint64); err != nil {
		t.Fatalf("overflow changed ownership: %v", err)
	}
}

func TestPendingOpenReservesEveryCloseRevision(t *testing.T) {
	broker, err := NewPendingBroker(2, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	broker.revision = math.MaxUint64 - 3
	scope, _ := interaction.NewScope("run")
	first, _ := interaction.NewRequest("first", "confirm", "Continue?", []byte(`{}`))
	firstContext, cancelFirst := context.WithCancel(t.Context())
	firstDone := make(chan error, 1)
	go func() {
		_, requestErr := broker.Request(firstContext, scope, first)
		firstDone <- requestErr
	}()
	eventuallyInternal(t, func() bool {
		broker.mu.Lock()
		defer broker.mu.Unlock()
		return len(broker.pending) == 1
	})
	second, _ := interaction.NewRequest("second", "confirm", "Continue?", []byte(`{}`))
	if _, err = broker.Request(t.Context(), scope, second); err == nil {
		t.Fatal("open consumed a revision reserved for pending close")
	}
	cancelFirst()
	if err = <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first request = %v", err)
	}
	if broker.revision != math.MaxUint64-1 {
		t.Fatalf("terminal revision = %d", broker.revision)
	}
}

func TestPendingExhaustionThenContextCancellationPreservesTerminalCause(t *testing.T) {
	broker, err := NewPendingBroker(1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	subscription, err := broker.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	broker.mu.Lock()
	delete(broker.watchers, subscription.id)
	broker.mu.Unlock()
	subscription.watcher.finishImmediate(nil, true)
	cancel()
	// Exercise the already-removed path deterministically as well as the
	// asynchronous context callback. It must finish expected, never a nil map
	// result, and must not overwrite the first terminal classification.
	broker.detachWatcher(subscription.id, subscription.watcher, context.Canceled, false)
	err = subscription.Wait(t.Context())
	exhausted, ok := errors.AsType[*ObserverExhaustedError](err)
	if !ok || exhausted.LastDelivered != subscription.Snapshot().Revision {
		t.Fatalf("terminal cause = %#v", err)
	}
}

func eventuallyInternal(t *testing.T, condition func() bool) {
	t.Helper()
	for range 10000 {
		if condition() {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("condition was not satisfied")
}

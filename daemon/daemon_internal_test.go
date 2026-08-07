package daemon

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

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
	hub, err := NewPendingHub(PendingLimits{
		Clients: 1, Runs: 1, RunsPerClient: 1,
		Pending: 2, PendingPerClient: 2, PendingBytes: maximumPendingBytes, PendingBytesPerClient: maximumPendingBytes,
		Observers: 1, ObserversPerClient: 1,
		ObserverQueueEntries: 2, ObserverQueueBytes: maximumObserverQueuedBytes,
		ReservedQueueEntries: 2, ReservedQueueEntriesPerClient: 2,
		ReservedQueueBytes: maximumObserverQueuedBytes, ReservedQueueBytesPerClient: maximumObserverQueuedBytes,
		QueuedEntries: 2, QueuedEntriesPerClient: 2,
		QueuedBytes: maximumObserverQueuedBytes, QueuedBytesPerClient: maximumObserverQueuedBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if hub == nil {
		t.Fatal("pending hub is nil")
	}
	scope, _ := interaction.NewScope("run")
	if _, err = hub.BindRun("client", scope); err != nil {
		t.Fatal(err)
	}
	partition := hub.partitions["client"]
	if partition == nil {
		t.Fatal("client partition is nil")
	}
	partition.revision = math.MaxUint64 - 3
	first, _ := interaction.NewRequest("first", "confirm", "Continue?", []byte(`{}`))
	firstContext, cancelFirst := context.WithCancel(t.Context())
	firstDone := make(chan error, 1)
	go func() {
		_, requestErr := hub.Request(firstContext, scope, first)
		firstDone <- requestErr
	}()
	eventuallyInternal(t, func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return len(partition.pending) == 1
	})
	second, _ := interaction.NewRequest("second", "confirm", "Continue?", []byte(`{}`))
	if _, err = hub.Request(t.Context(), scope, second); err == nil {
		t.Fatal("open consumed a revision reserved for pending close")
	}
	cancelFirst()
	if err = <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first request = %v", err)
	}
	if partition.revision != math.MaxUint64-1 {
		t.Fatalf("terminal revision = %d", partition.revision)
	}
}

func TestPendingExhaustionThenContextCancellationPreservesTerminalCause(t *testing.T) {
	hub, err := NewPendingHub(PendingLimits{
		Clients: 1, Runs: 1, RunsPerClient: 1,
		Pending: 1, PendingPerClient: 1, PendingBytes: maximumPendingBytes, PendingBytesPerClient: maximumPendingBytes,
		Observers: 1, ObserversPerClient: 1,
		ObserverQueueEntries: 1, ObserverQueueBytes: maximumObserverQueuedBytes,
		ReservedQueueEntries: 1, ReservedQueueEntriesPerClient: 1,
		ReservedQueueBytes: maximumObserverQueuedBytes, ReservedQueueBytesPerClient: maximumObserverQueuedBytes,
		QueuedEntries: 1, QueuedEntriesPerClient: 1,
		QueuedBytes: maximumObserverQueuedBytes, QueuedBytesPerClient: maximumObserverQueuedBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if hub == nil {
		t.Fatal("pending hub is nil")
	}
	ctx, cancel := context.WithCancel(t.Context())
	subscription, err := hub.Subscribe(ctx, "client")
	if err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	hub.finishWatcherLocked(subscription.watcher, nil, true)
	hub.mu.Unlock()
	cancel()
	hub.detachWatcher(subscription.watcher, context.Canceled, false)
	err = subscription.Wait(t.Context())
	exhausted, ok := errors.AsType[*ObserverExhaustedError](err)
	if !ok || exhausted.LastDelivered != subscription.Snapshot().Revision {
		t.Fatalf("terminal cause = %#v", err)
	}
}

func TestPendingCloseAdvancesPartitionRevisionsAndClearsAccounting(t *testing.T) {
	limits := PendingLimits{
		Clients: 2, Runs: 3, RunsPerClient: 2,
		Pending: 3, PendingPerClient: 2, PendingBytes: maximumPendingBytes, PendingBytesPerClient: maximumPendingBytes,
		Observers: 2, ObserversPerClient: 1,
		ObserverQueueEntries: 4, ObserverQueueBytes: maximumObserverQueuedBytes,
		ReservedQueueEntries: 8, ReservedQueueEntriesPerClient: 4,
		ReservedQueueBytes: 2 * maximumObserverQueuedBytes, ReservedQueueBytesPerClient: maximumObserverQueuedBytes,
		QueuedEntries: 8, QueuedEntriesPerClient: 4,
		QueuedBytes: 2 * maximumObserverQueuedBytes, QueuedBytesPerClient: maximumObserverQueuedBytes,
	}
	hub, err := NewPendingHub(limits)
	if err != nil {
		t.Fatal(err)
	}
	if hub == nil {
		t.Fatal("pending hub is nil")
	}
	firstScope, _ := interaction.NewScope("first")
	secondScope, _ := interaction.NewScope("second")
	thirdScope, _ := interaction.NewScope("third")
	firstBinding, _ := hub.BindRun("alpha", firstScope)
	secondBinding, _ := hub.BindRun("alpha", secondScope)
	thirdBinding, _ := hub.BindRun("beta", thirdScope)
	if _, err = hub.Subscribe(t.Context(), "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err = hub.Subscribe(t.Context(), "beta"); err != nil {
		t.Fatal(err)
	}
	results := make([]<-chan error, 0, 3)
	for index, scope := range []interaction.Scope{firstScope, secondScope, thirdScope} {
		request, _ := interaction.NewRequest(interaction.ID(string(rune('a'+index))), "confirm", "Continue?", []byte(`{}`))
		result := make(chan error, 1)
		go func() {
			_, requestErr := hub.Request(t.Context(), scope, request)
			result <- requestErr
		}()
		results = append(results, result)
	}
	eventuallyInternal(t, func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return hub.pendingCount == 3
	})
	hub.Close()
	for _, result := range results {
		if err = <-result; !errors.Is(err, ErrPendingHubClosed) {
			t.Fatalf("closed request = %v", err)
		}
	}
	for _, binding := range []*RunBinding{firstBinding, secondBinding, thirdBinding} {
		if err = binding.WaitReleased(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	alpha := hub.partitions["alpha"]
	beta := hub.partitions["beta"]
	if alpha == nil || beta == nil {
		t.Fatal("close partitions are nil")
	}
	if alpha.revision != 4 || beta.revision != 2 {
		t.Fatalf("close revisions = alpha %d beta %d", alpha.revision, beta.revision)
	}
	if hub.pendingCount != 0 || hub.pendingBytes != 0 || hub.observerCount != 0 ||
		hub.queuedEntries != 0 || hub.queuedBytes != 0 || hub.reservedEntries != 0 || hub.reservedBytes != 0 || len(hub.runs) != 0 {
		t.Fatalf("close accounting = pending %d/%d observers %d queue %d/%d reserved %d/%d runs %d",
			hub.pendingCount, hub.pendingBytes, hub.observerCount, hub.queuedEntries, hub.queuedBytes,
			hub.reservedEntries, hub.reservedBytes, len(hub.runs))
	}
}

func TestPendingFenceJoinsDeliveryAndDiscardsQueuedPayloads(t *testing.T) {
	limits := PendingLimits{
		Clients: 1, Runs: 1, RunsPerClient: 1,
		Pending: 3, PendingPerClient: 3, PendingBytes: maximumPendingBytes, PendingBytesPerClient: maximumPendingBytes,
		Observers: 1, ObserversPerClient: 1,
		ObserverQueueEntries: 4, ObserverQueueBytes: maximumObserverQueuedBytes,
		ReservedQueueEntries: 4, ReservedQueueEntriesPerClient: 4,
		ReservedQueueBytes: maximumObserverQueuedBytes, ReservedQueueBytesPerClient: maximumObserverQueuedBytes,
		QueuedEntries: 4, QueuedEntriesPerClient: 4,
		QueuedBytes: maximumObserverQueuedBytes, QueuedBytesPerClient: maximumObserverQueuedBytes,
	}
	hub, err := NewPendingHub(limits)
	if err != nil {
		t.Fatal(err)
	}
	if hub == nil {
		t.Fatal("pending hub is nil")
	}
	scope, _ := interaction.NewScope("run")
	if _, err = hub.BindRun("client", scope); err != nil {
		t.Fatal(err)
	}
	subscription, err := hub.Subscribe(t.Context(), "client")
	if err != nil {
		t.Fatal(err)
	}
	if hub == nil {
		t.Fatal("pending hub is nil")
	}
	cancels := make([]context.CancelFunc, 0, 3)
	for index := range 3 {
		ctx, cancel := context.WithCancel(t.Context())
		cancels = append(cancels, cancel)
		request, requestErr := interaction.NewRequest(
			interaction.ID(string(rune('a'+index))), "confirm", strings.Repeat("p", 256<<10),
			[]byte(`"`+strings.Repeat("s", 256<<10)+`"`),
		)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		go func() { _, _ = hub.Request(ctx, scope, request) }()
	}
	eventuallyInternal(t, func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return hub.queuedEntries == 3
	})
	if err = hub.FenceObservers("client"); err != nil {
		t.Fatal(err)
	}
	select {
	case _, open := <-subscription.Deltas():
		if open {
			t.Fatal("fence returned before the delivery stream closed")
		}
	default:
		t.Fatal("fence returned before the delivery goroutine joined")
	}
	if queued := len(subscription.watcher.queue); queued != 0 {
		t.Fatalf("queued payload references retained = %d", queued)
	}
	hub.mu.Lock()
	if hub.queuedEntries != 0 || hub.queuedBytes != 0 || hub.reservedEntries != 0 || hub.reservedBytes != 0 ||
		subscription.watcher.queuedCount != 0 || subscription.watcher.queuedBytes != 0 {
		hub.mu.Unlock()
		t.Fatal("fence did not clear queue accounting")
	}
	hub.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	hub.Close()
}

func TestPendingExhaustionCountsDeltaSentDuringDetach(t *testing.T) {
	limits := PendingLimits{
		Clients: 1, Runs: 1, RunsPerClient: 1,
		Pending: 1, PendingPerClient: 1, PendingBytes: maximumPendingBytes, PendingBytesPerClient: maximumPendingBytes,
		Observers: 1, ObserversPerClient: 1,
		ObserverQueueEntries: 1, ObserverQueueBytes: maximumObserverQueuedBytes,
		ReservedQueueEntries: 1, ReservedQueueEntriesPerClient: 1,
		ReservedQueueBytes: maximumObserverQueuedBytes, ReservedQueueBytesPerClient: maximumObserverQueuedBytes,
		QueuedEntries: 1, QueuedEntriesPerClient: 1,
		QueuedBytes: maximumObserverQueuedBytes, QueuedBytesPerClient: maximumObserverQueuedBytes,
	}
	hub, err := NewPendingHub(limits)
	if err != nil {
		t.Fatal(err)
	}
	if hub == nil {
		t.Fatal("pending hub is nil")
	}
	scope, _ := interaction.NewScope("run")
	if _, err = hub.BindRun("client", scope); err != nil {
		t.Fatal(err)
	}
	subscription, err := hub.Subscribe(t.Context(), "client")
	if err != nil {
		t.Fatal(err)
	}
	requestContext, cancelRequest := context.WithCancel(t.Context())
	request, _ := interaction.NewRequest("prompt", "confirm", "Continue?", []byte(`{}`))
	go func() { _, _ = hub.Request(requestContext, scope, request) }()
	eventuallyInternal(t, func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return hub.queuedEntries == 1
	})
	hub.mu.Lock()
	received := make(chan Delta, 1)
	go func() { received <- <-subscription.Deltas() }()
	delta := <-received
	hub.finishWatcherLocked(subscription.watcher, nil, true)
	hub.mu.Unlock()
	err = subscription.Wait(t.Context())
	exhausted, ok := errors.AsType[*ObserverExhaustedError](err)
	if !ok || exhausted.LastDelivered != delta.Revision || subscription.LastDelivered() != delta.Revision {
		t.Fatalf("sent-during-detach terminal = %#v last=%d delta=%d", err, subscription.LastDelivered(), delta.Revision)
	}
	cancelRequest()
	hub.Close()
}

func eventuallyInternal(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("condition was not satisfied")
}

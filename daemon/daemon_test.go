package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/daemon"
	"github.com/spice-framework/spice-agent/interaction"
)

func TestDefinitionSetIsCanonicalImmutableAndExact(t *testing.T) {
	alpha, err := agent.NewDefinition("alpha", "model-a", 1000)
	if err != nil {
		t.Fatal(err)
	}
	beta, _ := agent.NewDefinition("beta", "model-b", 1)
	alphaDefinition, _ := daemon.NewDefinition("alpha", "v1", alpha)
	betaDefinition, _ := daemon.NewDefinition("beta", "v1", beta)
	first, err := daemon.NewDefinitionSet([]daemon.Definition{betaDefinition, alphaDefinition})
	if err != nil {
		t.Fatal(err)
	}
	second, err := daemon.NewDefinitionSet([]daemon.Definition{alphaDefinition, betaDefinition})
	if err != nil {
		t.Fatal(err)
	}
	values := first.Definitions()
	values[0] = betaDefinition
	if first.Revision() != second.Revision() || first.Definitions()[0].ID() != "alpha" {
		t.Fatal("definition set is not canonical and defensively copied")
	}
	resolved, err := first.Resolve("beta", "v1")
	if err != nil || resolved.Agent().Model() != "model-b" || resolved.Agent().MaxTurns() != 1 {
		t.Fatalf("resolved definition = %#v, %v", resolved, err)
	}
	if _, err = first.Resolve("missing", "v1"); err == nil {
		t.Fatal("missing definition resolved")
	}
	if _, err = daemon.NewDefinitionSet([]daemon.Definition{alphaDefinition, alphaDefinition}); err == nil {
		t.Fatal("duplicate definition succeeded")
	}
	if _, err = daemon.NewDefinition("alpha\x00v1", "v1", alpha); err == nil {
		t.Fatal("control-character definition identity succeeded")
	}
	if _, err = daemon.NewDefinitionSet(make([]daemon.Definition, 4097)); err == nil {
		t.Fatal("oversized definition set succeeded")
	}
}

func TestSessionReconnectHasOneWinnerAndRootOwnership(t *testing.T) {
	root, cancelRoot := context.WithCancel(t.Context())
	store, err := daemon.NewSessionStore(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	if first.Epoch() != 1 || len(first.ClientID()) != 32 {
		t.Fatalf("fresh session = %q epoch %d", first.ClientID(), first.Epoch())
	}

	var wins atomic.Int32
	var winner daemon.Session
	var winnerMu sync.Mutex
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 32)
	for range 32 {
		wait.Go(func() {
			next, reconnectErr := store.Reconnect(first.ClientID(), 1)
			if reconnectErr == nil {
				wins.Add(1)
				winnerMu.Lock()
				winner = next
				winnerMu.Unlock()
				return
			}
			if !errors.Is(reconnectErr, daemon.ErrStaleSession) {
				errorsSeen <- reconnectErr
			}
		})
	}
	wait.Wait()
	close(errorsSeen)
	for reconnectErr := range errorsSeen {
		t.Errorf("reconnect: %v", reconnectErr)
	}
	if wins.Load() != 1 || winner.Epoch() != 2 || winner.ClientID() != first.ClientID() {
		t.Fatalf("wins = %d, winner = %#v", wins.Load(), winner)
	}
	assertDone(t, first.Context(), "old ownership epoch")
	if store.Check(first.ClientID(), 1) == nil || store.Check(first.ClientID(), 2) != nil {
		t.Fatal("session ownership check mismatch")
	}
	fenced, err := store.Fence(first.ClientID(), 2)
	if err != nil || fenced != winner.Context() {
		t.Fatalf("session fence = %v, %v", fenced, err)
	}
	if _, err = store.Fresh(); err == nil {
		t.Fatal("session capacity was not enforced")
	}
	if _, err = store.Reconnect("client\x00suffix", 1); err == nil {
		t.Fatal("control-character client ID succeeded")
	}
	cancelRoot()
	assertDone(t, winner.Context(), "daemon root cancellation")
	if err = store.Check(first.ClientID(), 2); !errors.Is(err, daemon.ErrSessionStoreClosed) {
		t.Fatalf("synchronous root fence = %v", err)
	}
	store.Close()
}

func TestSessionCloseCancelsEveryOwner(t *testing.T) {
	store, err := daemon.NewSessionStore(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := store.Fresh()
	second, _ := store.Fresh()
	store.Close()
	assertDone(t, first.Context(), "first session")
	assertDone(t, second.Context(), "second session")
	if _, err = store.Fresh(); !errors.Is(err, daemon.ErrSessionStoreClosed) {
		t.Fatalf("fresh after close = %v", err)
	}
	if _, err = daemon.NewSessionStore(t.Context(), 4097); err == nil {
		t.Fatal("oversized session store succeeded")
	}
}

func TestLedgerDeduplicatesPendingWithoutWaiterOwningWork(t *testing.T) {
	ledger, err := daemon.NewLedger(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	digest := daemon.CanonicalDigest([]byte(`{"a":1}`))
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	type result struct {
		outcome   daemon.Outcome
		duplicate bool
		err       error
	}
	owner := make(chan result, 1)
	go func() {
		outcome, duplicate, operationErr := ledger.Do(t.Context(), "client", "op", "start", digest, func(context.Context) (daemon.Outcome, error) {
			calls.Add(1)
			close(started)
			<-release
			value, _ := daemon.NewOutcome(daemon.OutcomeSuccess, []byte("ok"))
			return value, nil
		})
		owner <- result{outcome, duplicate, operationErr}
	}()
	<-started
	waiterContext, cancelWaiter := context.WithCancel(t.Context())
	cancelWaiter()
	if _, duplicate, operationErr := ledger.Do(waiterContext, "client", "op", "start", digest, panicExecutor); !duplicate || !errors.Is(operationErr, context.Canceled) {
		t.Fatalf("cancelled duplicate = %v, %v", duplicate, operationErr)
	}
	if _, _, operationErr := ledger.Do(t.Context(), "client", "op", "other", digest, panicExecutor); operationErr == nil {
		t.Fatal("conflicting operation succeeded")
	}
	close(release)
	owned := <-owner
	if owned.duplicate || owned.err != nil {
		t.Fatalf("owner result = %#v", owned)
	}
	outcome, duplicate, operationErr := ledger.Do(t.Context(), "client", "op", "start", digest, panicExecutor)
	if operationErr != nil || !duplicate || !slices.Equal(outcome.Payload(), owned.outcome.Payload()) || calls.Load() != 1 {
		t.Fatalf("duplicate = %v, %v, %q, calls %d", duplicate, operationErr, outcome.Payload(), calls.Load())
	}
	if _, _, operationErr = ledger.Do(t.Context(), "client", "op2", "start", digest, panicExecutor); operationErr == nil {
		t.Fatal("ledger capacity was not enforced")
	}
	if _, _, operationErr = ledger.Do(t.Context(), "client\x00op", "suffix", "start", digest, panicExecutor); operationErr == nil {
		t.Fatal("control-character operation identity succeeded")
	}
}

func TestLedgerContainsExecutorFailuresWithoutRetainingSecrets(t *testing.T) {
	for _, test := range []struct {
		name    string
		execute func(context.Context) (daemon.Outcome, error)
	}{
		{"error", func(context.Context) (daemon.Outcome, error) {
			return daemon.Outcome{}, errors.New("secret-token-value")
		}},
		{"panic", func(context.Context) (daemon.Outcome, error) { panic("secret-panic-value") }},
		{"invalid outcome", func(context.Context) (daemon.Outcome, error) { return daemon.Outcome{}, nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			ledger, _ := daemon.NewLedger(1, 1)
			digest := daemon.CanonicalDigest([]byte(test.name))
			owner, duplicate, operationErr := ledger.Do(t.Context(), "stable-client", "operation", "mutate", digest, test.execute)
			if duplicate || !errors.Is(operationErr, daemon.ErrOperationExecutor) || owner.Kind() != daemon.OutcomeUncertain {
				t.Fatalf("owner = %s, duplicate %v, error %v", owner.Kind(), duplicate, operationErr)
			}
			replayed, duplicate, replayErr := ledger.Do(t.Context(), "stable-client", "operation", "mutate", digest, panicExecutor)
			if !duplicate || !errors.Is(replayErr, daemon.ErrOperationExecutor) || replayed.Kind() != owner.Kind() || !slices.Equal(replayed.Payload(), owner.Payload()) {
				t.Fatalf("duplicate = %s, %v, %v", replayed.Kind(), duplicate, replayErr)
			}
			visible := operationErr.Error() + replayErr.Error() + string(owner.Payload()) + string(replayed.Payload())
			if strings.Contains(visible, "secret-") {
				t.Fatalf("executor secret escaped: %q", visible)
			}
		})
	}

	ledger, _ := daemon.NewLedger(1, 1)
	failure, _ := daemon.NewOutcome(daemon.OutcomeFailure, []byte(`{"code":"denied"}`))
	outcome, _, err := ledger.Do(t.Context(), "client", "business", "mutate", daemon.CanonicalDigest(nil), func(context.Context) (daemon.Outcome, error) {
		return failure, nil
	})
	if err != nil || outcome.Kind() != daemon.OutcomeFailure {
		t.Fatalf("explicit business failure = %s, %v", outcome.Kind(), err)
	}
}

func TestLedgerCancellationAndCapacityAreOwnershipSafe(t *testing.T) {
	ledger, _ := daemon.NewLedger(2, 1)
	digest := daemon.CanonicalDigest([]byte("input"))
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	var calls atomic.Int32
	if _, duplicate, err := ledger.Do(cancelled, "first", "op", "start", digest, func(context.Context) (daemon.Outcome, error) {
		calls.Add(1)
		return daemon.NewOutcome(daemon.OutcomeSuccess, nil)
	}); duplicate || !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled owner = duplicate %v, error %v", duplicate, err)
	}
	committed, duplicate, err := ledger.Do(t.Context(), "first", "op", "start", digest, func(context.Context) (daemon.Outcome, error) {
		calls.Add(1)
		return daemon.NewOutcome(daemon.OutcomeSuccess, []byte("committed"))
	})
	if duplicate || err != nil || calls.Load() != 1 {
		t.Fatalf("post-cancel owner = duplicate %v, error %v, calls %d", duplicate, err, calls.Load())
	}
	replayed, duplicate, err := ledger.Do(cancelled, "first", "op", "start", digest, panicExecutor)
	if !duplicate || err != nil || !slices.Equal(replayed.Payload(), committed.Payload()) {
		t.Fatalf("committed result lost to waiter cancellation: duplicate %v, error %v", duplicate, err)
	}
	other, duplicate, err := ledger.Do(t.Context(), "second", "op", "start", digest, func(context.Context) (daemon.Outcome, error) {
		return daemon.NewOutcome(daemon.OutcomeSuccess, []byte("other"))
	})
	if duplicate || err != nil || string(other.Payload()) != "other" {
		t.Fatalf("independent client capacity = %q, %v, %v", other.Payload(), duplicate, err)
	}
	if _, _, err = ledger.Do(t.Context(), "third", "op", "start", digest, panicExecutor); err == nil {
		t.Fatal("client capacity was not enforced")
	}
	var nilLedger *daemon.Ledger
	if _, _, err = nilLedger.Do(t.Context(), "client", "op", "start", digest, panicExecutor); err == nil {
		t.Fatal("nil ledger succeeded")
	}
	if _, err = daemon.NewLedger(2048, 1024); err == nil {
		t.Fatal("oversized aggregate ledger succeeded")
	}
}

type pendingTestHub struct {
	*daemon.PendingHub
	mu       sync.Mutex
	bindings map[string]*daemon.RunBinding
}

func newPendingHub(maxPending, maxObservers, observerQueue int) (*pendingTestHub, error) {
	hub, err := daemon.NewPendingHub(daemon.PendingLimits{
		Clients: 64, Runs: 64, RunsPerClient: 64,
		Pending: maxPending, PendingPerClient: maxPending,
		PendingBytes: 16 << 20, PendingBytesPerClient: 16 << 20,
		Observers: maxObservers, ObserversPerClient: maxObservers,
		ObserverQueueEntries: observerQueue, ObserverQueueBytes: 4 << 20,
		ReservedQueueEntries:          maxObservers * observerQueue,
		ReservedQueueEntriesPerClient: maxObservers * observerQueue,
		ReservedQueueBytes:            maxObservers * (4 << 20),
		ReservedQueueBytesPerClient:   maxObservers * (4 << 20),
		QueuedEntries:                 maxObservers * observerQueue,
		QueuedEntriesPerClient:        maxObservers * observerQueue,
		QueuedBytes:                   maxObservers * (4 << 20),
		QueuedBytesPerClient:          maxObservers * (4 << 20),
	})
	if err != nil {
		return nil, err
	}
	if hub == nil {
		return nil, errors.New("pending hub constructor returned nil")
	}
	return &pendingTestHub{PendingHub: hub, bindings: make(map[string]*daemon.RunBinding)}, nil
}

func mustNewPendingHub(t *testing.T, maxPending, maxObservers, observerQueue int) *pendingTestHub {
	t.Helper()
	hub, err := newPendingHub(maxPending, maxObservers, observerQueue)
	if err != nil {
		t.Fatal(err)
	}
	if hub == nil {
		t.Fatal("pending test hub is nil")
	}
	return hub
}

func (hub *pendingTestHub) ensureBound(scope interaction.Scope) error {
	if hub == nil || hub.PendingHub == nil {
		return daemon.ErrPendingHubClosed
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.bindings[scope.RunID()] != nil {
		return nil
	}
	binding, err := hub.BindRun("client", scope)
	if err != nil {
		return err
	}
	hub.bindings[scope.RunID()] = binding
	return nil
}

func (hub *pendingTestHub) Request(ctx context.Context, scope interaction.Scope, request interaction.Request) (interaction.Response, error) {
	if hub == nil || hub.PendingHub == nil {
		return interaction.Response{}, daemon.ErrPendingHubClosed
	}
	if ctx == nil {
		return interaction.Response{}, errors.New("pending interaction context is nil")
	}
	if err := ctx.Err(); err != nil {
		return interaction.Response{}, err
	}
	if err := hub.ensureBound(scope); err != nil {
		return interaction.Response{}, err
	}
	return hub.PendingHub.Request(ctx, scope, request)
}

func (hub *pendingTestHub) Respond(scope interaction.Scope, response interaction.Response) error {
	if hub == nil || hub.PendingHub == nil {
		return daemon.ErrPendingHubClosed
	}
	return hub.PendingHub.Respond("client", scope, response)
}

func (hub *pendingTestHub) Subscribe(ctx context.Context) (*daemon.PendingSubscription, error) {
	if hub == nil || hub.PendingHub == nil {
		return nil, daemon.ErrPendingHubClosed
	}
	return hub.PendingHub.Subscribe(ctx, "client")
}

func (hub *pendingTestHub) Close() {
	if hub != nil && hub.PendingHub != nil {
		hub.PendingHub.Close()
	}
}

func TestPendingSubscriptionIsCompleteFirstAndGapFree(t *testing.T) {
	broker := mustNewPendingHub(t, 4, 4, 4)
	scope := mustScope(t, "run")
	requestA := mustRequest(t, "a")
	requestB := mustRequest(t, "b")
	observer, _ := broker.Subscribe(t.Context())
	firstDone := startPending(t, broker, t.Context(), scope, requestA)
	openedA := receiveDelta(t, observer)
	if openedA.Revision != 1 || openedA.Kind != daemon.DeltaOpened {
		t.Fatalf("first delta = %#v", openedA)
	}

	tail, err := broker.Subscribe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := tail.Snapshot()
	if snapshot.Revision != 1 || len(snapshot.Pending) != 1 || snapshot.Pending[0].Request.ID() != "a" {
		t.Fatalf("mandatory snapshot = %#v", snapshot)
	}
	secondContext, cancelSecond := context.WithCancel(t.Context())
	secondDone := startPending(t, broker, secondContext, scope, requestB)
	openedB := receiveDelta(t, tail)
	if openedB.Revision != snapshot.Revision+1 || openedB.Pending.Request.ID() != "b" {
		t.Fatalf("snapshot-to-tail gap: snapshot %#v, delta %#v", snapshot, openedB)
	}

	respond(t, broker, scope, "a")
	if err = <-firstDone; err != nil {
		t.Fatal(err)
	}
	cancelSecond()
	if err = <-secondDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("second request = %v", err)
	}
	broker.Close()
}

func TestPendingSnapshotIsSortedAndDefensivelyCopied(t *testing.T) {
	broker := mustNewPendingHub(t, 4, 2, 8)
	observer, _ := broker.Subscribe(t.Context())
	contexts := make([]context.CancelFunc, 0, 3)
	for _, pair := range []struct{ run, id string }{{"z", "a"}, {"a", "z"}, {"a", "a"}} {
		ctx, cancel := context.WithCancel(t.Context())
		contexts = append(contexts, cancel)
		_ = startPending(t, broker, ctx, mustScope(t, pair.run), mustRequest(t, pair.id))
		_ = receiveDelta(t, observer)
	}
	subscription, err := broker.Subscribe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := subscription.Snapshot()
	got := make([]string, len(snapshot.Pending))
	for index, pending := range snapshot.Pending {
		got[index] = pending.Scope.RunID() + "/" + string(pending.Request.ID())
	}
	if !slices.Equal(got, []string{"a/a", "a/z", "z/a"}) {
		t.Fatalf("sorted pending = %v", got)
	}
	snapshot.Pending[0] = daemon.Pending{}
	if subscription.Snapshot().Pending[0].Request.ID() != "a" {
		t.Fatal("subscription snapshot was mutable")
	}
	for _, cancel := range contexts {
		cancel()
	}
	broker.Close()
}

func TestPendingCompositeIdentityDoesNotAlias(t *testing.T) {
	broker := mustNewPendingHub(t, 2, 2, 4)
	observer, _ := broker.Subscribe(t.Context())
	firstContext, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	secondContext, cancelSecond := context.WithCancel(t.Context())
	defer cancelSecond()
	_ = startPending(t, broker, firstContext, mustScope(t, "a"), mustRequest(t, "\x00b"))
	_ = receiveDelta(t, observer)
	_ = startPending(t, broker, secondContext, mustScope(t, "a\x00"), mustRequest(t, "b"))
	_ = receiveDelta(t, observer)
	subscription, err := broker.Subscribe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if pending := subscription.Snapshot().Pending; len(pending) != 2 {
		t.Fatalf("composite identities aliased: %#v", pending)
	}
	broker.Close()
}

func TestPendingRejectsCanceledWorkWithoutPublishing(t *testing.T) {
	broker := mustNewPendingHub(t, 1, 1, 1)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := broker.Request(cancelled, mustScope(t, "run"), mustRequest(t, "prompt")); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled request = %v", err)
	}
	if _, err := broker.Subscribe(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled subscription = %v", err)
	}
	subscription, err := broker.Subscribe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := subscription.Snapshot(); snapshot.Revision != 0 || len(snapshot.Pending) != 0 {
		t.Fatalf("cancelled request was published: %#v", snapshot)
	}
	broker.Close()
	if err = broker.Respond(mustScope(t, "run"), mustResponse(t, "prompt")); !errors.Is(err, daemon.ErrPendingHubClosed) {
		t.Fatalf("response after close = %v", err)
	}
	if _, err = newPendingHub(4097, 1, 1); err == nil {
		t.Fatal("oversized pending bound succeeded")
	}
	if _, err = newPendingHub(1, 1025, 1); err == nil {
		t.Fatal("oversized observer bound succeeded")
	}
	if _, err = newPendingHub(1, 1, 1025); err == nil {
		t.Fatal("oversized queue bound succeeded")
	}
	if _, err = newPendingHub(1, 65, 1); err == nil {
		t.Fatal("oversized aggregate observer budget succeeded")
	}
}

func TestPendingDuplicatePrecedesCapacityAndObserversAreBounded(t *testing.T) {
	broker := mustNewPendingHub(t, 1, 1, 1)
	subscription, _ := broker.Subscribe(t.Context())
	scope := mustScope(t, "run")
	request := mustRequest(t, "prompt")
	done := startPending(t, broker, t.Context(), scope, request)
	_ = receiveDelta(t, subscription)
	if _, err := broker.Request(t.Context(), scope, request); err == nil || !strings.Contains(err.Error(), "already pending") {
		t.Fatalf("duplicate at capacity = %v", err)
	}
	if _, err := broker.Subscribe(t.Context()); err == nil || !strings.Contains(err.Error(), "observer capacity") {
		t.Fatalf("second observer = %v", err)
	}
	broker.Close()
	for range subscription.Deltas() {
	}
	if err := <-done; !errors.Is(err, daemon.ErrPendingHubClosed) {
		t.Fatalf("closed request = %v", err)
	}
}

func TestPendingRetainedAndObserverBytesAreBounded(t *testing.T) {
	large := largeRequest(t)
	broker := mustNewPendingHub(t, 20, 1, 8)
	subscription, _ := broker.Subscribe(t.Context())
	contexts := make([]context.CancelFunc, 0, 20)
	accepted := 0
	for index := range 20 {
		ctx, cancel := context.WithCancel(t.Context())
		contexts = append(contexts, cancel)
		request, err := interaction.NewRequest(interaction.ID(fmt.Sprintf("large-%d", index)), large.Kind(), large.Prompt(), large.Schema())
		if err != nil {
			t.Fatal(err)
		}
		result := startPending(t, broker, ctx, mustScope(t, "retained"), request)
		select {
		case <-subscription.Deltas():
			accepted++
		case requestErr := <-result:
			if requestErr == nil || !strings.Contains(requestErr.Error(), "capacity") {
				t.Fatalf("retained byte rejection = %v", requestErr)
			}
			if accepted == 0 || accepted >= 20 {
				t.Fatalf("accepted large requests = %d", accepted)
			}
			goto retainedBounded
		case <-time.After(time.Second):
			t.Fatal("large request neither opened nor failed")
		}
	}
	t.Fatal("retained byte budget was not enforced")

retainedBounded:
	for _, cancel := range contexts {
		cancel()
	}
	broker.Close()

	observerBroker := mustNewPendingHub(t, 8, 1, 8)
	slow, _ := observerBroker.Subscribe(t.Context())
	observerContexts := make([]context.CancelFunc, 0, 4)
	for index := range 4 {
		ctx, cancel := context.WithCancel(t.Context())
		observerContexts = append(observerContexts, cancel)
		request, err := interaction.NewRequest(interaction.ID(fmt.Sprintf("observer-%d", index)), large.Kind(), large.Prompt(), large.Schema())
		if err != nil {
			t.Fatal(err)
		}
		_ = startPending(t, observerBroker, ctx, mustScope(t, "observer"), request)
	}
	waitContext, cancelWait := context.WithTimeout(t.Context(), time.Second)
	defer cancelWait()
	if exhaustion, ok := errors.AsType[*daemon.ObserverExhaustedError](slow.Wait(waitContext)); !ok || exhaustion.LastDelivered != 0 {
		t.Fatalf("observer byte exhaustion = %#v", exhaustion)
	}
	for _, cancel := range observerContexts {
		cancel()
	}
	observerBroker.Close()
}

func TestPendingAcceptedResponseWinsCancellationAndClose(t *testing.T) {
	for iteration := range 100 {
		broker := mustNewPendingHub(t, 1, 1, 2)
		scope := mustScope(t, fmt.Sprintf("run-%d", iteration))
		request := mustRequest(t, "prompt")
		requestContext, cancel := context.WithCancel(t.Context())
		observer, _ := broker.Subscribe(t.Context())
		done := startPending(t, broker, requestContext, scope, request)
		_ = receiveDelta(t, observer)
		response := mustResponse(t, "prompt")
		responded := make(chan error, 1)
		go func() { responded <- broker.Respond(scope, response) }()
		cancel()
		responseErr := <-responded
		requestErr := <-done
		if responseErr == nil && requestErr != nil {
			t.Fatalf("accepted response lost to cancellation: %v", requestErr)
		}
		if responseErr != nil && !errors.Is(requestErr, context.Canceled) {
			t.Fatalf("cancellation winner = response %v, request %v", responseErr, requestErr)
		}
		broker.Close()
	}

	broker := mustNewPendingHub(t, 1, 1, 2)
	scope := mustScope(t, "close-race")
	observer, _ := broker.Subscribe(t.Context())
	done := startPending(t, broker, t.Context(), scope, mustRequest(t, "prompt"))
	_ = receiveDelta(t, observer)
	responded := make(chan error, 1)
	go func() { responded <- broker.Respond(scope, mustResponse(t, "prompt")) }()
	closed := make(chan struct{})
	go func() { broker.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Respond versus Close deadlocked")
	}
	responseErr := <-responded
	requestErr := <-done
	if responseErr == nil && requestErr != nil {
		t.Fatalf("accepted response lost to close: %v", requestErr)
	}
	if responseErr != nil && !errors.Is(requestErr, daemon.ErrPendingHubClosed) {
		t.Fatalf("close winner = response %v, request %v", responseErr, requestErr)
	}
}

func TestPendingSlowSubscriberReportsExactLastDelivered(t *testing.T) {
	broker := mustNewPendingHub(t, 4, 1, 1)
	subscription, _ := broker.Subscribe(t.Context())
	contexts := make([]context.CancelFunc, 0, 4)
	for index := range 4 {
		ctx, cancel := context.WithCancel(t.Context())
		contexts = append(contexts, cancel)
		_ = startPending(t, broker, ctx, mustScope(t, "run"), mustRequest(t, fmt.Sprintf("p%d", index)))
		if index == 0 {
			delta := receiveDelta(t, subscription)
			if delta.Revision != 1 {
				t.Fatalf("first delta revision = %d", delta.Revision)
			}
			eventually(t, func() bool { return subscription.LastDelivered() == 1 })
		}
	}
	waitContext, cancelWait := context.WithTimeout(t.Context(), time.Second)
	defer cancelWait()
	err := subscription.Wait(waitContext)
	exhausted, ok := errors.AsType[*daemon.ObserverExhaustedError](err)
	if !ok || exhausted.LastDelivered != 1 || subscription.LastDelivered() != 1 {
		t.Fatalf("slow observer error = %#v, last %d", err, subscription.LastDelivered())
	}
	for _, cancel := range contexts {
		cancel()
	}
	broker.Close()
}

func TestPendingCloseUnblocksRequestsAndSubscriptions(t *testing.T) {
	broker := mustNewPendingHub(t, 2, 1, 2)
	subscription, _ := broker.Subscribe(t.Context())
	done := startPending(t, broker, t.Context(), mustScope(t, "run"), mustRequest(t, "prompt"))
	_ = receiveDelta(t, subscription)
	broker.Close()
	if err := <-done; !errors.Is(err, daemon.ErrPendingHubClosed) {
		t.Fatalf("request close = %v", err)
	}
	for range subscription.Deltas() {
	}
	if err := subscription.Wait(t.Context()); !errors.Is(err, daemon.ErrPendingHubClosed) {
		t.Fatalf("subscription close = %v", err)
	}
	if _, err := broker.Subscribe(t.Context()); !errors.Is(err, daemon.ErrPendingHubClosed) {
		t.Fatalf("subscribe after close = %v", err)
	}
}

func TestPendingCloseStopsUnreadObserverAndReleasesEveryCall(t *testing.T) {
	broker := mustNewPendingHub(t, 3, 1, 8)
	subscription, _ := broker.Subscribe(t.Context())
	done := make([]<-chan error, 0, 3)
	for _, value := range []struct{ run, id string }{{"z", "a"}, {"a", "z"}, {"a", "a"}} {
		done = append(done, startPending(t, broker, t.Context(), mustScope(t, value.run), mustRequest(t, value.id)))
	}
	closed := make(chan struct{})
	go func() {
		broker.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close blocked on unread observer")
	}
	for range subscription.Deltas() {
	}
	for _, result := range done {
		if err := <-result; !errors.Is(err, daemon.ErrPendingHubClosed) {
			t.Fatalf("closed request = %v", err)
		}
	}
}

func TestPendingNilReceiversFailClosed(t *testing.T) {
	var broker *pendingTestHub
	scope := mustScope(t, "run")
	request := mustRequest(t, "prompt")
	if _, err := broker.Request(t.Context(), scope, request); !errors.Is(err, daemon.ErrPendingHubClosed) {
		t.Fatalf("nil broker request = %v", err)
	}
	if err := broker.Respond(scope, mustResponse(t, "prompt")); !errors.Is(err, daemon.ErrPendingHubClosed) {
		t.Fatalf("nil broker response = %v", err)
	}
	if _, err := broker.Subscribe(t.Context()); !errors.Is(err, daemon.ErrPendingHubClosed) {
		t.Fatalf("nil broker subscribe = %v", err)
	}
	broker.Close()
	var subscription *daemon.PendingSubscription
	if snapshot := subscription.Snapshot(); snapshot.Revision != 0 || len(snapshot.Pending) != 0 {
		t.Fatalf("nil subscription snapshot = %#v", snapshot)
	}
	if _, open := <-subscription.Deltas(); open {
		t.Fatal("nil subscription delta stream remained open")
	}
	if subscription.LastDelivered() != 0 {
		t.Fatal("nil subscription delivered a revision")
	}
	if err := subscription.Wait(t.Context()); err == nil {
		t.Fatal("nil subscription wait succeeded")
	}
}

func panicExecutor(context.Context) (daemon.Outcome, error) { panic("duplicate executed") }

func startPending(t *testing.T, broker *pendingTestHub, ctx context.Context, scope interaction.Scope, request interaction.Request) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		response, err := broker.Request(ctx, scope, request)
		if err == nil && string(response.Value()) != "true" {
			err = fmt.Errorf("response = %s", response.Value())
		}
		done <- err
	}()
	return done
}

func receiveDelta(t *testing.T, subscription *daemon.PendingSubscription) daemon.Delta {
	t.Helper()
	select {
	case delta, open := <-subscription.Deltas():
		if !open {
			t.Fatal("pending delta stream closed")
		}
		return delta
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pending delta")
		return daemon.Delta{}
	}
}

func mustScope(t *testing.T, runID string) interaction.Scope {
	t.Helper()
	scope, err := interaction.NewScope(runID)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func mustRequest(t *testing.T, id string) interaction.Request {
	t.Helper()
	request, err := interaction.NewRequest(interaction.ID(id), "confirm", "Continue?", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func largeRequest(t *testing.T) interaction.Request {
	t.Helper()
	prompt := strings.Repeat("p", interaction.MaximumPayloadBytes)
	schema := json.RawMessage(`"` + strings.Repeat("s", interaction.MaximumPayloadBytes-2) + `"`)
	request, err := interaction.NewRequest("large", "confirm", prompt, schema)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func mustResponse(t *testing.T, id string) interaction.Response {
	t.Helper()
	response, err := interaction.NewResponse(interaction.ID(id), json.RawMessage(`true`))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func respond(t *testing.T, broker *pendingTestHub, scope interaction.Scope, id string) {
	t.Helper()
	if err := broker.Respond(scope, mustResponse(t, id)); err != nil {
		t.Fatal(err)
	}
}

func assertDone(t *testing.T, ctx context.Context, label string) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatalf("%s context remained active", label)
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not satisfied")
		}
		time.Sleep(time.Millisecond)
	}
}

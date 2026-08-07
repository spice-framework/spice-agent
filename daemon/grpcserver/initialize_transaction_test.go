package grpcserver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
)

func TestReconnectInitializationTransactionSerializesStoreAndRegistryEpochs(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 2, 2)
	first := freshRegistrySession(t, store)
	if err := registry.installFresh(first, registryInitializeResponse(first)); err != nil {
		t.Fatal(err)
	}

	firstAdvanced := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan transactionResult, 1)
	go func() {
		var next daemon.Session
		response, err := registry.initializeReconnect(
			t.Context(), first.ClientID(), first.Epoch(),
			registryResponseForOwnership(first.ClientID(), first.Epoch()+1),
			func() (daemon.Session, error) {
				var reconnectErr error
				next, reconnectErr = store.ReconnectContext(t.Context(), first.ClientID(), first.Epoch())
				close(firstAdvanced)
				<-releaseFirst
				return next, reconnectErr
			},
		)
		firstDone <- transactionResult{session: next, response: response, err: err}
	}()
	<-firstAdvanced
	if err := store.Check(first.ClientID(), first.Epoch()+1); err != nil {
		t.Fatalf("store did not reach N+1: %v", err)
	}
	if _, err := registry.lookup(first.ClientID(), first.Epoch()+1); !errors.Is(err, errNegotiatedSessionUnavailable) {
		t.Fatalf("registry exposed uncommitted N+1: %v", err)
	}

	secondEntered := make(chan struct{})
	secondDone := make(chan transactionResult, 1)
	go func() {
		response, err := registry.initializeReconnect(
			t.Context(), first.ClientID(), first.Epoch(),
			registryResponseForOwnership(first.ClientID(), first.Epoch()+1),
			func() (daemon.Session, error) {
				close(secondEntered)
				return store.ReconnectContext(t.Context(), first.ClientID(), first.Epoch())
			},
		)
		secondDone <- transactionResult{response: response, err: err}
	}()
	waitForReconnectWaiters(t, registry, first.ClientID(), 1)
	select {
	case <-secondEntered:
		t.Fatal("overlapping stale-N store CAS entered before the N+1 registry commit")
	default:
	}
	close(releaseFirst)
	firstResult := <-firstDone
	if firstResult.err != nil || firstResult.session.Epoch() != first.Epoch()+1 ||
		firstResult.response.GetOwnershipEpoch() != first.Epoch()+1 {
		t.Fatalf("N+1 transaction = %#v", firstResult)
	}
	secondResult := <-secondDone
	if !errors.Is(secondResult.err, errNegotiatedSessionUnavailable) || secondResult.response != nil {
		t.Fatalf("overlapping stale-N transaction = %#v", secondResult)
	}
	thirdResponse, err := registry.initializeReconnect(
		t.Context(), first.ClientID(), first.Epoch()+1,
		registryResponseForOwnership(first.ClientID(), first.Epoch()+2),
		func() (daemon.Session, error) {
			return store.ReconnectContext(t.Context(), first.ClientID(), first.Epoch()+1)
		},
	)
	if err != nil || thirdResponse.GetOwnershipEpoch() != first.Epoch()+2 {
		t.Fatalf("N+2 transaction = %#v, %v", thirdResponse, err)
	}
	if checkErr := store.Check(first.ClientID(), first.Epoch()+2); checkErr != nil {
		t.Fatalf("store final epoch: %v", checkErr)
	}
	current, err := registry.lookup(first.ClientID(), first.Epoch()+2)
	if err != nil || current.response.GetOwnershipEpoch() != first.Epoch()+2 {
		t.Fatalf("registry final epoch = %#v, %v", current, err)
	}
}

func TestReconnectInitializationGateReleasesOnCancellationAndPanic(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 2, 2)
	first := freshRegistrySession(t, store)
	if err := registry.installFresh(first, registryInitializeResponse(first)); err != nil {
		t.Fatal(err)
	}

	held := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := registry.initializeReconnect(
			t.Context(), first.ClientID(), first.Epoch(),
			registryResponseForOwnership(first.ClientID(), first.Epoch()+1),
			func() (daemon.Session, error) {
				close(held)
				<-release
				return store.ReconnectContext(t.Context(), first.ClientID(), first.Epoch())
			},
		)
		firstDone <- err
	}()
	<-held
	cancelled, cancel := context.WithCancel(t.Context())
	var called atomic.Bool
	waiterDone := make(chan error, 1)
	go func() {
		_, waiterErr := registry.initializeReconnect(
			cancelled, first.ClientID(), first.Epoch(),
			registryResponseForOwnership(first.ClientID(), first.Epoch()+1),
			func() (daemon.Session, error) { called.Store(true); return daemon.Session{}, nil },
		)
		waiterDone <- waiterErr
	}()
	waitForReconnectWaiters(t, registry, first.ClientID(), 1)
	cancel()
	err := <-waiterDone
	if !errors.Is(err, context.Canceled) || called.Load() {
		t.Fatalf("cancelled waiter = called %t, error %v", called.Load(), err)
	}
	close(release)
	if err = <-firstDone; err != nil {
		t.Fatal(err)
	}

	assertTransactionPanic(t, registry, first.ClientID(), first.Epoch()+1)
	response, err := registry.initializeReconnect(
		t.Context(), first.ClientID(), first.Epoch()+1,
		registryResponseForOwnership(first.ClientID(), first.Epoch()+2),
		func() (daemon.Session, error) {
			return store.ReconnectContext(t.Context(), first.ClientID(), first.Epoch()+1)
		},
	)
	if err != nil || response.GetOwnershipEpoch() != first.Epoch()+2 {
		t.Fatalf("transaction after panic = %#v, %v", response, err)
	}
}

func TestReconnectInitializationGatesArePerClientAndBoundWaiters(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 4, 4)
	first := freshRegistrySession(t, store)
	second := freshRegistrySession(t, store)
	for _, session := range []daemon.Session{first, second} {
		if err := registry.installFresh(session, registryInitializeResponse(session)); err != nil {
			t.Fatal(err)
		}
	}
	entered := make(chan string, 2)
	release := make(chan struct{})
	done := make(chan error, 2)
	for _, session := range []daemon.Session{first, second} {
		current := session
		go func() {
			_, err := registry.initializeReconnect(
				t.Context(), current.ClientID(), current.Epoch(),
				registryResponseForOwnership(current.ClientID(), current.Epoch()+1),
				func() (daemon.Session, error) {
					entered <- current.ClientID()
					<-release
					return store.ReconnectContext(t.Context(), current.ClientID(), current.Epoch())
				},
			)
			done <- err
		}()
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case clientID := <-entered:
			seen[clientID] = true
		case <-time.After(time.Second):
			t.Fatal("independent client transaction was head-of-line blocked")
		}
	}
	if !seen[first.ClientID()] || !seen[second.ClientID()] {
		t.Fatalf("entered client gates = %#v", seen)
	}
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}

	holderEntered := make(chan struct{})
	holderRelease := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		_, err := registry.initializeReconnect(
			t.Context(), first.ClientID(), first.Epoch()+1,
			registryResponseForOwnership(first.ClientID(), first.Epoch()+2),
			func() (daemon.Session, error) {
				close(holderEntered)
				<-holderRelease
				return store.ReconnectContext(t.Context(), first.ClientID(), first.Epoch()+1)
			},
		)
		holderDone <- err
	}()
	<-holderEntered
	waitContext, cancelWaiters := context.WithCancel(t.Context())
	waiterDone := make(chan error, maximumReconnectWaitersPerClient)
	for range maximumReconnectWaitersPerClient {
		go func() {
			_, err := registry.initializeReconnect(
				waitContext, first.ClientID(), first.Epoch()+1,
				registryResponseForOwnership(first.ClientID(), first.Epoch()+2),
				func() (daemon.Session, error) {
					return daemon.Session{}, errors.New("bounded waiter unexpectedly entered")
				},
			)
			waiterDone <- err
		}()
	}
	waitForReconnectWaiters(t, registry, first.ClientID(), maximumReconnectWaitersPerClient)
	if _, err := registry.initializeReconnect(
		t.Context(), first.ClientID(), first.Epoch()+1,
		registryResponseForOwnership(first.ClientID(), first.Epoch()+2),
		func() (daemon.Session, error) { return daemon.Session{}, nil },
	); !errors.Is(err, errNegotiatedSessionWaiterLimit) {
		t.Fatalf("excess reconnect waiter = %v", err)
	}
	cancelWaiters()
	for range maximumReconnectWaitersPerClient {
		if err := <-waiterDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled bounded waiter = %v", err)
		}
	}
	close(holderRelease)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
}

func TestReconnectInitializationShutdownFinishesActiveTransactionThenClears(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 2, 2)
	first := freshRegistrySession(t, store)
	if err := registry.installFresh(first, registryInitializeResponse(first)); err != nil {
		t.Fatal(err)
	}
	advanced := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := registry.initializeReconnect(
			t.Context(), first.ClientID(), first.Epoch(),
			registryResponseForOwnership(first.ClientID(), first.Epoch()+1),
			func() (daemon.Session, error) {
				next, reconnectErr := store.ReconnectContext(t.Context(), first.ClientID(), first.Epoch())
				close(advanced)
				<-release
				return next, reconnectErr
			},
		)
		done <- err
	}()
	<-advanced
	queuedCalled := atomic.Bool{}
	queuedDone := make(chan error, 1)
	go func() {
		_, err := registry.initializeReconnect(
			t.Context(), first.ClientID(), first.Epoch(),
			registryResponseForOwnership(first.ClientID(), first.Epoch()+1),
			func() (daemon.Session, error) {
				queuedCalled.Store(true)
				return daemon.Session{}, nil
			},
		)
		queuedDone <- err
	}()
	waitForReconnectWaiters(t, registry, first.ClientID(), 1)
	registry.close()
	registry.mu.Lock()
	entriesDuringTransaction := len(registry.entries)
	registry.mu.Unlock()
	if entriesDuringTransaction != 1 {
		t.Fatalf("shutdown cleared the in-flight transaction base: %d entries", entriesDuringTransaction)
	}
	if err := <-queuedDone; !errors.Is(err, errNegotiatedSessionClosed) || queuedCalled.Load() {
		t.Fatalf("queued shutdown waiter = called %t, error %v", queuedCalled.Load(), err)
	}
	close(release)
	if err := <-done; !errors.Is(err, errNegotiatedSessionClosed) {
		t.Fatalf("active transaction shutdown error = %v", err)
	}
	if err := store.Check(first.ClientID(), first.Epoch()+1); err != nil {
		t.Fatalf("store final epoch after shutdown: %v", err)
	}
	registry.mu.Lock()
	remaining := len(registry.entries)
	active := registry.activeTransactions
	registry.mu.Unlock()
	if remaining != 0 || active != 0 {
		t.Fatalf("closed registry retained entries/transactions = %d/%d", remaining, active)
	}
}

func TestFreshInitializationReservesRegistryCapacityBeforeStoreAllocation(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 2, 1)
	allocated := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := registry.initializeFresh(t.Context(), func() (
			daemon.Session,
			*enginev1.InitializeResponse,
			error,
		) {
			fresh, freshErr := store.Fresh()
			close(allocated)
			<-release
			return fresh, registryInitializeResponse(fresh), freshErr
		})
		done <- err
	}()
	<-allocated
	var secondAllocated atomic.Bool
	_, err := registry.initializeFresh(t.Context(), func() (
		daemon.Session,
		*enginev1.InitializeResponse,
		error,
	) {
		secondAllocated.Store(true)
		fresh, freshErr := store.Fresh()
		return fresh, registryInitializeResponse(fresh), freshErr
	})
	if !errors.Is(err, errNegotiatedSessionCapacity) || secondAllocated.Load() {
		t.Fatalf("contended fresh = allocated %t, error %v", secondAllocated.Load(), err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = store.Fresh(); err != nil {
		t.Fatalf("capacity rejection orphaned a SessionStore allocation: %v", err)
	}
}

func TestFreshInitializationDoesNotSucceedAcrossShutdown(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 2, 2)
	allocated := make(chan struct{})
	release := make(chan struct{})
	var clientID string
	done := make(chan error, 1)
	go func() {
		_, err := registry.initializeFresh(t.Context(), func() (
			daemon.Session,
			*enginev1.InitializeResponse,
			error,
		) {
			fresh, freshErr := store.Fresh()
			clientID = fresh.ClientID()
			close(allocated)
			<-release
			return fresh, registryInitializeResponse(fresh), freshErr
		})
		done <- err
	}()
	<-allocated
	registry.close()
	close(release)
	if err := <-done; !errors.Is(err, errNegotiatedSessionClosed) {
		t.Fatalf("fresh transaction shutdown error = %v", err)
	}
	if err := store.Check(clientID, 1); err != nil {
		t.Fatalf("fresh store ownership was not committed: %v", err)
	}
	registry.mu.Lock()
	remaining := len(registry.entries)
	active := registry.activeTransactions
	registry.mu.Unlock()
	if remaining != 0 || active != 0 {
		t.Fatalf("fresh shutdown retained entries/transactions = %d/%d", remaining, active)
	}
}

type transactionResult struct {
	session  daemon.Session
	response *enginev1.InitializeResponse
	err      error
}

func registryResponseForOwnership(clientID string, epoch uint64) *enginev1.InitializeResponse {
	response := registryInitializeResponse(daemon.Session{})
	response.ClientId = clientID
	response.OwnershipEpoch = epoch
	return response
}

func assertTransactionPanic(
	t *testing.T,
	registry *negotiatedSessionRegistry,
	clientID string,
	expectedEpoch uint64,
) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("transaction operation panic was swallowed")
		}
	}()
	_, _ = registry.initializeReconnect(
		t.Context(), clientID, expectedEpoch,
		registryResponseForOwnership(clientID, expectedEpoch+1),
		func() (daemon.Session, error) { panic("fixture panic") },
	)
}

func waitForReconnectWaiters(
	t *testing.T,
	registry *negotiatedSessionRegistry,
	clientID string,
	want int,
) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		registry.mu.Lock()
		entry := registry.entries[clientID]
		got := 0
		if entry.gate != nil {
			got = entry.gate.waiters
		}
		registry.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("reconnect waiter count did not reach %d", want)
}

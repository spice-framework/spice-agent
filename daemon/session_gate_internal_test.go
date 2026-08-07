package daemon

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionReconnectPrioritizesIntentAndStalesOldQueue(t *testing.T) {
	store, session := newGateTestStore(t, 1)
	active, err := store.AcquireMutationCommit(t.Context(), session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}

	queuedResult := make(chan error, 1)
	go func() {
		lease, acquireErr := store.AcquireMutationCommit(t.Context(), session.ClientID(), session.Epoch())
		if lease != nil {
			lease.Close()
		}
		queuedResult <- acquireErr
	}()
	awaitGateState(t, store, session.ClientID(), func(state *sessionState) bool {
		return len(state.mutationWaiters) == 1
	})

	reconnected := make(chan struct {
		session Session
		err     error
	}, 1)
	go func() {
		next, reconnectErr := store.ReconnectContext(t.Context(), session.ClientID(), session.Epoch())
		reconnected <- struct {
			session Session
			err     error
		}{next, reconnectErr}
	}()
	awaitGateState(t, store, session.ClientID(), func(state *sessionState) bool {
		return len(state.reconnectWaiters) == 1
	})

	active.Close()
	result := <-reconnected
	if result.err != nil || result.session.Epoch() != session.Epoch()+1 {
		t.Fatalf("reconnect = epoch %d, %v", result.session.Epoch(), result.err)
	}
	queuedErr := <-queuedResult
	stale, ok := errors.AsType[*StaleSessionError](queuedErr)
	if !ok || !errors.Is(queuedErr, ErrStaleSession) || stale.ClientID() != session.ClientID() ||
		stale.ExpectedEpoch() != result.session.Epoch() || stale.ObservedEpoch() != session.Epoch() {
		t.Fatalf("queued mutation stale facts = %#v", queuedErr)
	}
	if session.Context().Err() == nil {
		t.Fatal("successful reconnect did not fence the old epoch context")
	}
}

func TestSessionCanceledReconnectRestoresOldEpochAndMutationFIFO(t *testing.T) {
	store, session := newGateTestStore(t, 1)
	active, err := store.AcquireMutationCommit(t.Context(), session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}

	type mutationResult struct {
		order int
		err   error
	}
	results := make(chan mutationResult, 2)
	var acquired atomic.Int32
	for index := range 2 {
		go func(order int) {
			lease, acquireErr := store.AcquireMutationCommit(t.Context(), session.ClientID(), session.Epoch())
			if acquireErr == nil {
				position := int(acquired.Add(1))
				results <- mutationResult{order: order*10 + position}
				lease.Close()
				return
			}
			results <- mutationResult{order: order * 10, err: acquireErr}
		}(index + 1)
		want := index + 1
		awaitGateState(t, store, session.ClientID(), func(state *sessionState) bool {
			return len(state.mutationWaiters) == want
		})
	}

	reconnectContext, cancelReconnect := context.WithCancel(t.Context())
	reconnectResult := make(chan error, 1)
	go func() {
		_, reconnectErr := store.ReconnectContext(reconnectContext, session.ClientID(), session.Epoch())
		reconnectResult <- reconnectErr
	}()
	awaitGateState(t, store, session.ClientID(), func(state *sessionState) bool {
		return len(state.reconnectWaiters) == 1
	})
	cancelReconnect()
	if reconnectErr := <-reconnectResult; !errors.Is(reconnectErr, context.Canceled) {
		t.Fatalf("canceled reconnect = %v", reconnectErr)
	}
	if err = store.Check(session.ClientID(), session.Epoch()); err != nil || session.Context().Err() != nil {
		t.Fatalf("canceled reconnect changed old ownership: check %v context %v", err, session.Context().Err())
	}

	active.Close()
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil || first.order != 11 || second.order != 22 {
		t.Fatalf("restored FIFO = %#v then %#v", first, second)
	}
}

func TestSessionReconnectCancelsAndJoinsOldStreams(t *testing.T) {
	store, session := newGateTestStore(t, 1)
	lease, err := store.AcquireStream(t.Context(), session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}

	cancelObserved := make(chan struct{})
	allowJoin := make(chan struct{})
	joined := make(chan struct{})
	frames := make(chan string, 1)
	go func() {
		<-lease.Context().Done()
		close(cancelObserved)
		<-allowJoin
		frames <- "old-terminal-frame"
		lease.Close()
		close(joined)
	}()

	reconnected := make(chan Session, 1)
	reconnectErrors := make(chan error, 1)
	go func() {
		next, reconnectErr := store.ReconnectContext(t.Context(), session.ClientID(), session.Epoch())
		if reconnectErr != nil {
			reconnectErrors <- reconnectErr
			return
		}
		reconnected <- next
	}()
	select {
	case <-cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("reconnect did not cancel old stream")
	}
	select {
	case next := <-reconnected:
		t.Fatalf("reconnect returned before stream join: %#v", next)
	case err = <-reconnectErrors:
		t.Fatalf("reconnect failed before stream join: %v", err)
	default:
	}
	close(allowJoin)
	if frame := <-frames; frame != "old-terminal-frame" {
		t.Fatalf("old frame = %q", frame)
	}
	<-joined
	var next Session
	select {
	case next = <-reconnected:
	case err = <-reconnectErrors:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("reconnect did not return after stream join")
	}
	select {
	case frame := <-frames:
		t.Fatalf("old frame arrived after reconnect returned: %q", frame)
	default:
	}
	if next.Epoch() != session.Epoch()+1 {
		t.Fatalf("next epoch = %d", next.Epoch())
	}
}

func TestSessionCanceledReconnectDuringStreamJoinRestoresOldMutationFIFO(t *testing.T) {
	store, session := newGateTestStore(t, 1)
	stream, err := store.AcquireStream(t.Context(), session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}

	mutationAcquired := make(chan *MutationCommitLease, 1)
	mutationErrors := make(chan error, 1)
	reconnectContext, cancelReconnect := context.WithCancel(t.Context())
	reconnectResult := make(chan error, 1)
	go func() {
		_, reconnectErr := store.ReconnectContext(reconnectContext, session.ClientID(), session.Epoch())
		reconnectResult <- reconnectErr
	}()
	select {
	case <-stream.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("reconnect did not begin stream fencing")
	}
	go func() {
		commit, acquireErr := store.AcquireMutationCommit(t.Context(), session.ClientID(), session.Epoch())
		if acquireErr != nil {
			mutationErrors <- acquireErr
			return
		}
		mutationAcquired <- commit
	}()
	awaitGateState(t, store, session.ClientID(), func(state *sessionState) bool {
		return len(state.reconnectWaiters) == 1 && len(state.mutationWaiters) == 1
	})

	cancelReconnect()
	if reconnectErr := <-reconnectResult; !errors.Is(reconnectErr, context.Canceled) {
		t.Fatalf("canceled reconnect = %v", reconnectErr)
	}
	var commit *MutationCommitLease
	select {
	case commit = <-mutationAcquired:
	case err = <-mutationErrors:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("old-epoch FIFO did not resume after canceled reconnect")
	}
	if err = store.Check(session.ClientID(), session.Epoch()); err != nil || session.Context().Err() != nil {
		t.Fatalf("ownership after canceled stream fence: check %v context %v", err, session.Context().Err())
	}
	commit.Close()
	stream.Close()
}

func TestSessionStreamCancellationBeforeGrantDoesNotPublishLease(t *testing.T) {
	store, session := newGateTestStore(t, 1)
	base, cancel := context.WithCancel(t.Context())
	observed := &observedErrContext{Context: base, secondCheck: make(chan struct{})}
	type result struct {
		lease *StreamLease
		err   error
	}
	acquired := make(chan result, 1)
	store.mu.Lock()
	go func() {
		lease, err := store.AcquireStream(observed, session.ClientID(), session.Epoch())
		acquired <- result{lease: lease, err: err}
	}()
	<-observed.secondCheck
	cancel()
	store.mu.Unlock()
	streamResult := <-acquired
	if streamResult.lease != nil || !errors.Is(streamResult.err, context.Canceled) {
		t.Fatalf("canceled pre-grant stream = %#v, %v", streamResult.lease, streamResult.err)
	}
	store.mu.Lock()
	state := store.sessions[session.ClientID()]
	if state == nil || len(state.streams) != 0 || len(state.streamWaiters) != 0 {
		store.mu.Unlock()
		t.Fatal("canceled pre-grant stream published gate state")
	}
	store.mu.Unlock()
	store.Close()
}

func TestSessionMutationCancellationBeforeGrantDoesNotPublishLease(t *testing.T) {
	store, session := newGateTestStore(t, 1)
	base, cancel := context.WithCancel(t.Context())
	observed := &observedErrContext{Context: base, secondCheck: make(chan struct{})}
	type result struct {
		lease *MutationCommitLease
		err   error
	}
	acquired := make(chan result, 1)
	store.mu.Lock()
	go func() {
		lease, err := store.AcquireMutationCommit(observed, session.ClientID(), session.Epoch())
		acquired <- result{lease: lease, err: err}
	}()
	<-observed.secondCheck
	cancel()
	store.mu.Unlock()
	mutationResult := <-acquired
	if mutationResult.lease != nil || !errors.Is(mutationResult.err, context.Canceled) {
		t.Fatalf("canceled pre-grant mutation = %#v, %v", mutationResult.lease, mutationResult.err)
	}
	store.mu.Lock()
	state := store.sessions[session.ClientID()]
	if state == nil || state.activeMutation != nil || len(state.mutationWaiters) != 0 {
		store.mu.Unlock()
		t.Fatal("canceled pre-grant mutation published gate state")
	}
	store.mu.Unlock()
	store.Close()
}

func TestSessionReconnectHasOneWinnerAmongThirtyTwoContextClaimants(t *testing.T) {
	store, session := newGateTestStore(t, 1)
	start := make(chan struct{})
	results := make(chan error, 32)
	var winners atomic.Int32
	var wait sync.WaitGroup
	for range 32 {
		wait.Go(func() {
			<-start
			_, err := store.ReconnectContext(t.Context(), session.ClientID(), session.Epoch())
			if err == nil {
				winners.Add(1)
			}
			results <- err
		})
	}
	close(start)
	wait.Wait()
	close(results)
	staleCount := 0
	for err := range results {
		if errors.Is(err, ErrStaleSession) {
			staleCount++
		} else if err != nil {
			t.Errorf("claim = %v", err)
		}
	}
	if winners.Load() != 1 || staleCount != 31 {
		t.Fatalf("reconnect outcomes = %d winners, %d stale", winners.Load(), staleCount)
	}
}

func TestSessionGatesAreIsolatedPerClient(t *testing.T) {
	store, first := newGateTestStore(t, 2)
	second, err := store.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	firstCommit, err := store.AcquireMutationCommit(t.Context(), first.ClientID(), first.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	defer firstCommit.Close()

	secondCommit, err := store.AcquireMutationCommit(t.Context(), second.ClientID(), second.Epoch())
	if err != nil {
		t.Fatalf("second client commit blocked by first: %v", err)
	}
	secondCommit.Close()
	secondNext, err := store.ReconnectContext(t.Context(), second.ClientID(), second.Epoch())
	if err != nil || secondNext.Epoch() != 2 {
		t.Fatalf("second client reconnect = epoch %d, %v", secondNext.Epoch(), err)
	}
}

func TestSessionMutationWaitersAreBoundedAndCancellationSafe(t *testing.T) {
	store, session := newGateTestStore(t, 1)
	active, err := store.AcquireMutationCommit(t.Context(), session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}

	contexts := make([]context.CancelFunc, 0, maximumSessionGateWaitersPerClient)
	results := make(chan error, maximumSessionGateWaitersPerClient)
	for index := range maximumSessionGateWaitersPerClient {
		waitContext, cancel := context.WithCancel(t.Context())
		contexts = append(contexts, cancel)
		go func() {
			lease, acquireErr := store.AcquireMutationCommit(waitContext, session.ClientID(), session.Epoch())
			if lease != nil {
				lease.Close()
			}
			results <- acquireErr
		}()
		want := index + 1
		awaitGateState(t, store, session.ClientID(), func(state *sessionState) bool {
			return len(state.mutationWaiters) == want
		})
	}

	if _, err = store.AcquireMutationCommit(t.Context(), session.ClientID(), session.Epoch()); !errors.Is(err, ErrSessionGateCapacity) {
		t.Fatalf("overflow mutation = %v", err)
	} else {
		capacity, ok := errors.AsType[*SessionGateCapacityError](err)
		if !ok || capacity.Resource() == "" || capacity.Maximum() != maximumSessionGateWaitersPerClient {
			t.Fatalf("capacity facts = %#v", err)
		}
	}
	for _, cancel := range contexts {
		cancel()
	}
	for range maximumSessionGateWaitersPerClient {
		if resultErr := <-results; !errors.Is(resultErr, context.Canceled) {
			t.Errorf("canceled waiter = %v", resultErr)
		}
	}
	active.Close()

	streams := make([]*StreamLease, 0, maximumSessionStreamsPerClient)
	for range maximumSessionStreamsPerClient {
		stream, streamErr := store.AcquireStream(t.Context(), session.ClientID(), session.Epoch())
		if streamErr != nil {
			t.Fatal(streamErr)
		}
		streams = append(streams, stream)
	}
	if _, err = store.AcquireStream(t.Context(), session.ClientID(), session.Epoch()); !errors.Is(err, ErrSessionGateCapacity) {
		t.Fatalf("overflow stream = %v", err)
	} else {
		capacity, ok := errors.AsType[*SessionGateCapacityError](err)
		if !ok || capacity.Resource() != "stream leases" || capacity.Maximum() != maximumSessionStreamsPerClient {
			t.Fatalf("stream capacity facts = %#v", err)
		}
	}
	for _, stream := range streams {
		stream.Close()
	}
}

func TestSessionStreamAcquisitionWaitersAreBoundedTrackedAndClosed(t *testing.T) {
	store, session := newGateTestStore(t, 1)
	active, err := store.AcquireMutationCommit(t.Context(), session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	reconnected := make(chan error, 1)
	go func() {
		_, reconnectErr := store.ReconnectContext(t.Context(), session.ClientID(), session.Epoch())
		reconnected <- reconnectErr
	}()
	awaitGateState(t, store, session.ClientID(), func(state *sessionState) bool {
		return len(state.reconnectWaiters) == 1
	})

	const streamWaiters = maximumSessionGateWaitersPerClient - 1
	waiterResults := make(chan error, streamWaiters)
	for index := range streamWaiters {
		go func() {
			lease, acquireErr := store.AcquireStream(t.Context(), session.ClientID(), session.Epoch())
			if lease != nil {
				lease.Close()
			}
			waiterResults <- acquireErr
		}()
		want := index + 1
		awaitGateState(t, store, session.ClientID(), func(state *sessionState) bool {
			return len(state.streamWaiters) == want
		})
	}
	if _, err = store.AcquireStream(t.Context(), session.ClientID(), session.Epoch()); !errors.Is(err, ErrSessionGateCapacity) {
		t.Fatalf("overflow stream acquisition waiter = %v", err)
	} else {
		capacity, ok := errors.AsType[*SessionGateCapacityError](err)
		if !ok || capacity.Resource() != "session gate waiters" || capacity.Maximum() != maximumSessionGateWaitersPerClient {
			t.Fatalf("stream waiter capacity facts = %#v", err)
		}
	}

	store.Close()
	for range streamWaiters {
		if waiterErr := <-waiterResults; !errors.Is(waiterErr, ErrSessionStoreClosed) {
			t.Errorf("stream acquisition waiter after close = %v", waiterErr)
		}
	}
	if reconnectErr := <-reconnected; !errors.Is(reconnectErr, ErrSessionStoreClosed) {
		t.Fatalf("reconnect after close = %v", reconnectErr)
	}
	store.mu.Lock()
	state := store.sessions[session.ClientID()]
	remaining := -1
	if state != nil {
		remaining = len(state.streamWaiters)
	}
	store.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("stream acquisition waiters retained after close = %d", remaining)
	}
	short, cancelShort := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancelShort()
	if err = store.Shutdown(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown ignored active mutation commit = %v", err)
	}
	active.Close()
	if err = store.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSessionShutdownCountsTrackedStreamAcquisitionWaiters(t *testing.T) {
	store, session := newGateTestStore(t, 1)
	store.mu.Lock()
	state := store.sessions[session.ClientID()]
	if state == nil {
		store.mu.Unlock()
		t.Fatal("session state is nil")
	}
	waiter := &streamWaiter{}
	state.streamWaiters = append(state.streamWaiters, waiter)
	store.mu.Unlock()
	store.Close()
	short, cancelShort := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancelShort()
	if err := store.Shutdown(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown falsely drained a tracked stream waiter: %v", err)
	}
	store.mu.Lock()
	store.removeStreamWaiterLocked(state, waiter)
	store.mu.Unlock()
	if err := store.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCloseIsNonblockingAndShutdownJoinsLeases(t *testing.T) {
	store, session := newGateTestStore(t, 1)
	commit, err := store.AcquireMutationCommit(t.Context(), session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := store.AcquireStream(t.Context(), session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	queued := make(chan error, 1)
	go func() {
		queuedLease, acquireErr := store.AcquireMutationCommit(t.Context(), session.ClientID(), session.Epoch())
		if queuedLease != nil {
			queuedLease.Close()
		}
		queued <- acquireErr
	}()
	awaitGateState(t, store, session.ClientID(), func(state *sessionState) bool {
		return len(state.mutationWaiters) == 1
	})

	closed := make(chan struct{})
	go func() {
		store.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked on active leases")
	}
	if stream.Context().Err() == nil || session.Context().Err() == nil {
		t.Fatal("Close did not cancel session and stream contexts")
	}
	if queuedErr := <-queued; !errors.Is(queuedErr, ErrSessionStoreClosed) {
		t.Fatalf("queued mutation after close = %v", queuedErr)
	}
	short, cancelShort := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancelShort()
	if err = store.Shutdown(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown with active leases = %v", err)
	}
	commit.Close()
	stream.Close()
	if err = store.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcquireStream(t.Context(), session.ClientID(), session.Epoch()); !errors.Is(err, ErrSessionStoreClosed) {
		t.Fatalf("stream after close = %v", err)
	}
}

func TestSessionRootCancellationWakesQueuedGateAndDrains(t *testing.T) {
	root, cancelRoot := context.WithCancel(t.Context())
	store, err := NewSessionStore(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.AcquireMutationCommit(t.Context(), session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	queued := make(chan error, 1)
	go func() {
		_, acquireErr := store.AcquireMutationCommit(t.Context(), session.ClientID(), session.Epoch())
		queued <- acquireErr
	}()
	awaitGateState(t, store, session.ClientID(), func(state *sessionState) bool {
		return len(state.mutationWaiters) == 1
	})
	reconnected := make(chan error, 1)
	go func() {
		_, reconnectErr := store.ReconnectContext(t.Context(), session.ClientID(), session.Epoch())
		reconnected <- reconnectErr
	}()
	awaitGateState(t, store, session.ClientID(), func(state *sessionState) bool {
		return len(state.reconnectWaiters) == 1
	})
	cancelRoot()
	if queuedErr := <-queued; !errors.Is(queuedErr, ErrSessionStoreClosed) {
		t.Fatalf("queued mutation after root cancellation = %v", queuedErr)
	}
	if reconnectErr := <-reconnected; !errors.Is(reconnectErr, ErrSessionStoreClosed) {
		t.Fatalf("reconnect after root cancellation = %v", reconnectErr)
	}
	store.mu.Lock()
	state := store.sessions[session.ClientID()]
	if state == nil {
		store.mu.Unlock()
		t.Fatal("root cancellation removed session state")
	}
	if observed := state.epoch; observed != session.Epoch() {
		store.mu.Unlock()
		t.Fatalf("root cancellation advanced epoch to %d", observed)
	}
	store.mu.Unlock()
	active.Close()
	if err = store.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSessionGateCancellationRaceStressDoesNotLeak(t *testing.T) {
	for iteration := range 200 {
		store, session := newGateTestStore(t, 1)
		active, err := store.AcquireMutationCommit(t.Context(), session.ClientID(), session.Epoch())
		if err != nil {
			t.Fatalf("iteration %d active: %v", iteration, err)
		}
		reconnectContext, cancelReconnect := context.WithCancel(t.Context())
		reconnectDone := make(chan error, 1)
		go func() {
			_, reconnectErr := store.ReconnectContext(reconnectContext, session.ClientID(), session.Epoch())
			reconnectDone <- reconnectErr
		}()
		awaitGateState(t, store, session.ClientID(), func(state *sessionState) bool {
			return len(state.reconnectWaiters) == 1
		})
		if iteration%2 == 0 {
			cancelReconnect()
			active.Close()
		} else {
			active.Close()
			cancelReconnect()
		}
		reconnectErr := <-reconnectDone
		if reconnectErr != nil && !errors.Is(reconnectErr, context.Canceled) {
			t.Fatalf("iteration %d reconnect: %v", iteration, reconnectErr)
		}
		if err = store.Shutdown(t.Context()); err != nil {
			t.Fatalf("iteration %d shutdown: %v", iteration, err)
		}
	}
}

func TestSessionGateBoundaryFailuresAndZeroValues(t *testing.T) {
	var nilStore *SessionStore
	if _, err := nilStore.AcquireMutationCommit(t.Context(), "client", 1); !errors.Is(err, ErrSessionStoreClosed) {
		t.Fatalf("nil store mutation = %v", err)
	}
	if _, err := nilStore.AcquireStream(t.Context(), "client", 1); !errors.Is(err, ErrSessionStoreClosed) {
		t.Fatalf("nil store stream = %v", err)
	}
	if _, err := nilStore.ReconnectContext(t.Context(), "client", 1); !errors.Is(err, ErrSessionStoreClosed) {
		t.Fatalf("nil store reconnect = %v", err)
	}
	if err := nilStore.Shutdown(t.Context()); err != nil {
		t.Fatalf("nil store shutdown = %v", err)
	}

	store, session := newGateTestStore(t, 1)
	var missingContext context.Context
	if _, err := store.AcquireMutationCommit(missingContext, session.ClientID(), session.Epoch()); err == nil {
		t.Fatal("nil mutation context succeeded")
	}
	if _, err := store.AcquireStream(missingContext, session.ClientID(), session.Epoch()); err == nil {
		t.Fatal("nil stream context succeeded")
	}
	if _, err := store.ReconnectContext(missingContext, session.ClientID(), session.Epoch()); err == nil {
		t.Fatal("nil reconnect context succeeded")
	}
	if err := store.Shutdown(missingContext); err == nil {
		t.Fatal("nil shutdown context succeeded")
	}
	if _, err := store.AcquireStream(t.Context(), "bad\x00client", session.Epoch()); err == nil {
		t.Fatal("invalid stream client succeeded")
	}
	if err := store.Check(session.ClientID(), session.Epoch()+1); err == nil {
		t.Fatal("stale ownership check succeeded")
	} else {
		stale, ok := errors.AsType[*StaleSessionError](err)
		if !ok || stale.Error() == "" || stale.ClientID() != session.ClientID() ||
			stale.ExpectedEpoch() != session.Epoch() || stale.ObservedEpoch() != session.Epoch()+1 {
			t.Fatalf("stale error = %#v", err)
		}
	}
	if _, err := store.Fence("unknown-client", 1); !errors.Is(err, ErrStaleSession) {
		t.Fatalf("unknown client fence = %v", err)
	} else if _, typed := errors.AsType[*StaleSessionError](err); typed {
		t.Fatalf("unknown client acquired invented epoch facts: %#v", err)
	}

	var nilStale *StaleSessionError
	if nilStale.Error() == "" || nilStale.ClientID() != "" || nilStale.ExpectedEpoch() != 0 || nilStale.ObservedEpoch() != 0 {
		t.Fatal("nil stale error accessors are unsafe")
	}
	var nilCapacity *SessionGateCapacityError
	if nilCapacity.Error() == "" || nilCapacity.Resource() != "" || nilCapacity.Maximum() != 0 {
		t.Fatal("nil capacity error accessors are unsafe")
	}
	capacity := &SessionGateCapacityError{resource: "test", maximum: 1}
	if capacity.Error() == "" || !errors.Is(capacity, ErrSessionGateCapacity) {
		t.Fatalf("capacity error = %v", capacity)
	}

	var nilCommit *MutationCommitLease
	nilCommit.Close()
	var nilStream *StreamLease
	nilStream.Close()
	if nilStream.Context().Err() != nil {
		t.Fatal("nil stream context was unexpectedly canceled")
	}
	emptyCommit := &MutationCommitLease{}
	emptyCommit.Close()
	emptyStream := &StreamLease{}
	emptyStream.Close()
	emptyStream.Close()

	shortRandom, err := newSessionStore(t.Context(), 1, strings.NewReader("short"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = shortRandom.Fresh(); err == nil {
		t.Fatal("short randomness succeeded")
	}
	shortRandom.Close()
	store.Close()
}

func TestSessionStoreZeroValueFailsClosed(t *testing.T) {
	store := &SessionStore{}
	if _, err := store.Fresh(); !errors.Is(err, ErrSessionStoreClosed) {
		t.Fatalf("zero store fresh = %v", err)
	}
	if err := store.Check("client", 1); !errors.Is(err, ErrSessionStoreClosed) {
		t.Fatalf("zero store check = %v", err)
	}
	if _, err := store.Fence("client", 1); !errors.Is(err, ErrSessionStoreClosed) {
		t.Fatalf("zero store fence = %v", err)
	}
	if _, err := store.Reconnect("client", 1); !errors.Is(err, ErrSessionStoreClosed) {
		t.Fatalf("zero store reconnect = %v", err)
	}
	if _, err := store.AcquireMutationCommit(t.Context(), "client", 1); !errors.Is(err, ErrSessionStoreClosed) {
		t.Fatalf("zero store mutation = %v", err)
	}
	if _, err := store.AcquireStream(t.Context(), "client", 1); !errors.Is(err, ErrSessionStoreClosed) {
		t.Fatalf("zero store stream = %v", err)
	}
	store.Close()
	store.Close()
	if err := store.Shutdown(t.Context()); err != nil {
		t.Fatalf("zero store shutdown = %v", err)
	}
}

func newGateTestStore(t *testing.T, maximum int) (*SessionStore, Session) {
	t.Helper()
	store, err := NewSessionStore(t.Context(), maximum)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	return store, session
}

func awaitGateState(t *testing.T, store *SessionStore, clientID string, predicate func(*sessionState) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		state := store.sessions[clientID]
		matched := state != nil && predicate(state)
		store.mu.Unlock()
		if matched {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("session gate state was not observed")
}

type observedErrContext struct {
	context.Context //nolint:containedctx // deterministic test wrapper must intercept Err while preserving Context behavior.
	calls           atomic.Int32
	secondCheck     chan struct{}
}

func (ctx *observedErrContext) Err() error {
	if ctx.calls.Add(1) == 2 {
		close(ctx.secondCheck)
	}
	return ctx.Context.Err()
}

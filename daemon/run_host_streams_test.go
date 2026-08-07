package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/interaction"
)

func TestRunHostReplayEventsIsOwnedPagedAndEpochBound(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	provider := &gatedHostProvider{started: make(chan struct{}), release: release}
	fixture := newRunHostFixture(t, provider, 1, 2)
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "stream-start", fixture.definition, "stream-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-provider.started

	request := event.ReplayRequest{AfterSequence: 0, MaxEvents: 128, MaxBytes: 1 << 20, Tail: true}
	observation, err := fixture.host.ReplayEvents(t.Context(), fixture.session, started.Run(), request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observation.Close)
	page := observation.Page()
	if len(page.Events) == 0 || page.HasMore || !page.Tailing || page.Tail == nil {
		t.Fatalf("initial replay page = %#v", page)
	}
	for index, envelope := range page.Events {
		want := uint64(index + 1)
		if envelope.RunID() != started.Run().ID() || envelope.Sequence() != want {
			t.Fatalf("event %d = run %q sequence %d, want %q/%d", index, envelope.RunID(), envelope.Sequence(), started.Run().ID(), want)
		}
	}

	other, err := fixture.sessions.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	missing, _ := client.NewRunRef("missing-stream-run")
	_, wrongOwnerErr := fixture.host.ReplayEvents(t.Context(), other, started.Run(), request)
	_, missingErr := fixture.host.ReplayEvents(t.Context(), fixture.session, missing, request)
	if !errors.Is(wrongOwnerErr, ErrHostedRunUnavailable) || !errors.Is(missingErr, ErrHostedRunUnavailable) ||
		wrongOwnerErr.Error() != missingErr.Error() {
		t.Fatalf("wrong-owner %v and missing %v were distinguishable", wrongOwnerErr, missingErr)
	}

	reconnected := make(chan Session, 1)
	reconnectErr := make(chan error, 1)
	go func() {
		next, reconnectFailure := fixture.sessions.ReconnectContext(
			context.Background(), fixture.session.ClientID(), fixture.session.Epoch(),
		)
		reconnected <- next
		reconnectErr <- reconnectFailure
	}()
	select {
	case <-observation.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("reconnect did not fence the event observation")
	}
	select {
	case next := <-reconnected:
		t.Fatalf("reconnect returned before event observation close: %#v", next)
	case <-time.After(20 * time.Millisecond):
	}
	if err = page.Tail.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("fenced event tail = %v", err)
	}
	observation.Close()
	next := <-reconnected
	if err = <-reconnectErr; err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.host.ReplayEvents(t.Context(), fixture.session, started.Run(), request); !errors.Is(err, ErrStaleSession) {
		t.Fatalf("old epoch replay = %v", err)
	}
	request.Tail = false
	request.AfterSequence = page.PageLastSequence
	nextObservation, err := fixture.host.ReplayEvents(t.Context(), next, started.Run(), request)
	if err != nil {
		t.Fatalf("new epoch replay = %v", err)
	}
	t.Cleanup(nextObservation.Close)
	if nextObservation.Page().Tailing {
		t.Fatal("finite event replay unexpectedly tails")
	}
	nextObservation.Close()

	close(release)
	waitForNoHostActive(t, fixture.host)
	terminalObservation, err := fixture.host.ReplayEvents(t.Context(), next, started.Run(), event.ReplayRequest{
		AfterSequence: 0, MaxEvents: 128, MaxBytes: 1 << 20, Tail: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(terminalObservation.Close)
	terminal := terminalObservation.Page()
	if terminal.Tailing || terminal.Tail != nil || len(terminal.Events) == 0 ||
		!terminal.Events[len(terminal.Events)-1].Terminal() {
		t.Fatalf("terminal replay page = %#v", terminal)
	}
	terminalObservation.Close()
	_, err = fixture.host.ReplayEvents(t.Context(), next, started.Run(), event.ReplayRequest{
		AfterSequence: terminal.LatestSequence + 1, MaxEvents: 1, MaxBytes: 1 << 20,
	})
	if _, ok := errors.AsType[*event.OutOfRangeError](err); !ok {
		t.Fatalf("future replay cursor = %v", err)
	}
	_, err = fixture.host.ReplayEvents(t.Context(), next, started.Run(), event.ReplayRequest{
		AfterSequence: 0, MaxEvents: 1, MaxBytes: 1,
	})
	if _, ok := errors.AsType[*event.ResourceExhaustedError](err); !ok {
		t.Fatalf("unprogressable replay page = %v", err)
	}
}

func TestRunHostInteractionSubscriptionIsClientScopedAndEpochBound(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	provider := &gatedHostProvider{started: make(chan struct{}), release: release}
	fixture := newRunHostFixture(t, provider, 1, 2)
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "interaction-stream-start", fixture.definition, "interaction-stream-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-provider.started

	initialObservation, err := fixture.host.SnapshotInteractions(t.Context(), fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(initialObservation.Close)
	initial := initialObservation.Snapshot()
	if initial.Revision != 0 || len(initial.Pending) != 0 {
		t.Fatalf("initial pending snapshot = %#v", initial)
	}
	if initialObservation.Tailing() {
		t.Fatal("finite interaction snapshot unexpectedly tails")
	}
	fixture.pending().mu.Lock()
	observersBefore := fixture.pending().observerCount
	fixture.pending().mu.Unlock()
	if observersBefore != 0 {
		t.Fatalf("snapshot-only observer count = %d, want 0", observersBefore)
	}
	initialObservation.Close()

	subscription, err := fixture.host.SubscribeInteractions(t.Context(), fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(subscription.Close)
	if snapshot := subscription.Snapshot(); snapshot.Revision != 0 || len(snapshot.Pending) != 0 {
		t.Fatalf("initial pending snapshot = %#v", snapshot)
	}
	fixture.host.mu.Lock()
	hosted := fixture.host.active[started.Run().ID()]
	fixture.host.mu.Unlock()
	if hosted == nil || hosted.run == nil {
		t.Fatal("started run is not active")
	}
	request, err := interaction.NewRequest(
		interaction.ID("stream-approval"), "approval", "Continue?", json.RawMessage(`{"type":"string"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	responseResult := make(chan interaction.Response, 1)
	responseErr := make(chan error, 1)
	go func() {
		response, interactionErr := hosted.run.Interact(context.Background(), request)
		responseResult <- response
		responseErr <- interactionErr
	}()
	opened := receivePendingDelta(t, subscription)
	if opened.Kind != DeltaOpened || opened.Revision != 1 ||
		opened.Pending.Scope.RunID() != started.Run().ID() || opened.Pending.Request.ID() != request.ID() {
		t.Fatalf("opened interaction delta = %#v", opened)
	}

	other, err := fixture.sessions.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	fixture.pending().mu.Lock()
	partitionsBefore := len(fixture.pending().partitions)
	fixture.pending().mu.Unlock()
	otherObservation, err := fixture.host.SnapshotInteractions(t.Context(), other)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(otherObservation.Close)
	otherSnapshot := otherObservation.Snapshot()
	if len(otherSnapshot.Pending) != 0 {
		t.Fatalf("other client observed pending interactions = %#v", otherSnapshot)
	}
	otherObservation.Close()
	fixture.pending().mu.Lock()
	partitionsAfter := len(fixture.pending().partitions)
	fixture.pending().mu.Unlock()
	if partitionsAfter != partitionsBefore {
		t.Fatalf("snapshot-only partition count = %d, want %d", partitionsAfter, partitionsBefore)
	}

	reconnected := make(chan Session, 1)
	reconnectErr := make(chan error, 1)
	go func() {
		next, reconnectFailure := fixture.sessions.ReconnectContext(
			context.Background(), fixture.session.ClientID(), fixture.session.Epoch(),
		)
		reconnected <- next
		reconnectErr <- reconnectFailure
	}()
	select {
	case <-subscription.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("reconnect did not fence the interaction observation")
	}
	select {
	case next := <-reconnected:
		t.Fatalf("reconnect returned before interaction observation close: %#v", next)
	case <-time.After(20 * time.Millisecond):
	}
	if err = subscription.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("fenced interaction subscription = %v", err)
	}
	subscription.Close()
	next := <-reconnected
	if err = <-reconnectErr; err != nil {
		t.Fatal(err)
	}

	nextContext, stopNext := context.WithCancel(t.Context())
	nextSubscription, err := fixture.host.SubscribeInteractions(nextContext, next)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nextSubscription.Close)
	if snapshot := nextSubscription.Snapshot(); snapshot.Revision != opened.Revision || len(snapshot.Pending) != 1 ||
		snapshot.Pending[0].Request.ID() != request.ID() {
		t.Fatalf("reconnected pending snapshot = %#v", snapshot)
	}
	operation, _ := client.NewOperationID("interaction-stream-respond")
	value, _ := client.NewStructuredText("yes")
	response, _ := client.NewInteractionResponse(string(request.ID()), value)
	respond, _ := client.NewRespondRequest(started.Run(), operation, response)
	if _, err = fixture.host.Respond(t.Context(), next, respond); err != nil {
		t.Fatal(err)
	}
	if err = <-responseErr; err != nil {
		t.Fatal(err)
	}
	if got := string((<-responseResult).Value()); got != `"yes"` {
		t.Fatalf("interaction response = %s", got)
	}
	closed := receivePendingDelta(t, nextSubscription)
	if closed.Kind != DeltaClosed || closed.Revision != opened.Revision+1 || closed.Pending.Request.ID() != request.ID() {
		t.Fatalf("closed interaction delta = %#v", closed)
	}
	stopNext()
	if err = nextSubscription.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("reconnected subscription cancellation = %v", err)
	}
	nextSubscription.Close()
	close(release)
}

func TestRunHostStreamsRejectInvalidInputsAndHonorShutdown(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	run, _ := client.NewRunRef("stream-boundary-run")
	valid := event.ReplayRequest{MaxEvents: 1, MaxBytes: 1}

	var closed *RunHost
	if _, err := closed.ReplayEvents(t.Context(), fixture.session, run, valid); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("nil host replay = %v", err)
	}
	if _, err := closed.SubscribeInteractions(t.Context(), fixture.session); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("nil host interaction subscription = %v", err)
	}
	if _, err := closed.SnapshotInteractions(t.Context(), fixture.session); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("nil host interaction snapshot = %v", err)
	}
	//nolint:staticcheck // Boundary coverage intentionally passes nil to verify defensive context handling.
	if _, err := fixture.host.ReplayEvents(nil, fixture.session, run, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("nil replay context = %v", err)
	}
	//nolint:staticcheck // Boundary coverage intentionally passes nil to verify defensive context handling.
	if _, err := fixture.host.SubscribeInteractions(nil, fixture.session); !errors.Is(err, context.Canceled) {
		t.Fatalf("nil interaction context = %v", err)
	}
	//nolint:staticcheck // Boundary coverage intentionally passes nil to verify defensive context handling.
	if _, err := fixture.host.SnapshotInteractions(nil, fixture.session); !errors.Is(err, context.Canceled) {
		t.Fatalf("nil interaction snapshot context = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.host.ReplayEvents(canceled, fixture.session, run, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled replay context = %v", err)
	}
	if _, err := fixture.host.SubscribeInteractions(canceled, fixture.session); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled interaction context = %v", err)
	}
	if _, err := fixture.host.SnapshotInteractions(canceled, fixture.session); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled interaction snapshot context = %v", err)
	}
	if _, err := fixture.host.ReplayEvents(t.Context(), fixture.session, client.RunRef{}, valid); !errors.Is(err, ErrHostedRunUnavailable) {
		t.Fatalf("invalid replay run = %v", err)
	}
	for _, invalid := range []event.ReplayRequest{
		{},
		{MaxEvents: int(fixture.config.Limits.ReplayEvents()) + 1, MaxBytes: 1},
		{MaxEvents: 1, MaxBytes: int(fixture.config.Limits.ReplayBytes()) + 1},
	} {
		if _, err := fixture.host.ReplayEvents(t.Context(), fixture.session, run, invalid); !errors.Is(err, ErrRunHostState) {
			t.Fatalf("invalid replay request %#v = %v", invalid, err)
		}
	}

	streamContext, stopStream := context.WithCancel(t.Context())
	fixture.pending().mu.Lock()
	fixture.pending().limits.Observers = 1
	fixture.pending().limits.ObserversPerClient = 1
	fixture.pending().mu.Unlock()
	subscription, err := fixture.host.SubscribeInteractions(streamContext, fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(subscription.Close)
	if _, err = fixture.host.SubscribeInteractions(t.Context(), fixture.session); !errors.Is(err, ErrRunHostCapacity) {
		t.Fatalf("interaction observer capacity = %v", err)
	}
	stopStream()
	if err = subscription.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("interaction cancellation = %v", err)
	}
	subscription.Close()
	finite, err := fixture.host.SnapshotInteractions(t.Context(), fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(finite.Close)
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- fixture.host.Shutdown(context.Background()) }()
	select {
	case <-finite.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel the finite observation")
	}
	select {
	case shutdownErr := <-shutdownDone:
		t.Fatalf("shutdown returned before finite observation close: %v", shutdownErr)
	case <-time.After(20 * time.Millisecond):
	}
	finite.Close()
	if err = <-shutdownDone; err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.host.SubscribeInteractions(t.Context(), fixture.session); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("post-shutdown subscription = %v", err)
	}
	if _, err = fixture.host.SnapshotInteractions(t.Context(), fixture.session); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("post-shutdown snapshot = %v", err)
	}
	if _, err = fixture.pending().Snapshot(fixture.session.ClientID()); !errors.Is(err, ErrPendingHubClosed) {
		t.Fatalf("post-shutdown pending snapshot = %v", err)
	}
	var nilPending *PendingHub
	if _, err = nilPending.Snapshot(fixture.session.ClientID()); !errors.Is(err, ErrPendingHubClosed) {
		t.Fatalf("nil pending snapshot = %v", err)
	}
	var nilEvents *EventObservation
	if nilEvents.Page().Tail != nil || nilEvents.Context().Err() == nil {
		t.Fatal("nil event observation did not fail closed")
	}
	nilEvents.Close()
	var nilInteractions *InteractionObservation
	if nilInteractions.Tailing() || len(nilInteractions.Snapshot().Pending) != 0 ||
		nilInteractions.Context().Err() == nil {
		t.Fatal("nil interaction observation did not fail closed")
	}
	if err = nilInteractions.Wait(t.Context()); err == nil {
		t.Fatal("nil interaction observation wait succeeded")
	}
	nilInteractions.Close()
}

func TestRunHostFiniteObservationsEnforceStreamCapacityWithoutWatchers(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	maximum := int(fixture.config.Limits.ConcurrentStreams())
	observations := make([]*InteractionObservation, 0, maximum)
	t.Cleanup(func() {
		for _, observation := range observations {
			observation.Close()
		}
	})
	for range maximum {
		observation, err := fixture.host.SnapshotInteractions(t.Context(), fixture.session)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Tailing() {
			t.Fatal("finite observation allocated a tail")
		}
		observations = append(observations, observation)
	}
	fixture.sessions.mu.Lock()
	state := fixture.sessions.sessions[fixture.session.ClientID()]
	streamCount := 0
	if state != nil {
		streamCount = len(state.streams)
	}
	fixture.sessions.mu.Unlock()
	if streamCount != maximum {
		t.Fatalf("stream lease count = %d, want %d", streamCount, maximum)
	}
	fixture.pending().mu.Lock()
	observerCount := fixture.pending().observerCount
	fixture.pending().mu.Unlock()
	if observerCount != 0 {
		t.Fatalf("finite observations allocated %d pending watchers", observerCount)
	}

	if _, err := fixture.host.SnapshotInteractions(t.Context(), fixture.session); !errors.Is(err, ErrSessionGateCapacity) {
		t.Fatalf("stream capacity = %v", err)
	} else if capacity, ok := errors.AsType[*SessionGateCapacityError](err); !ok ||
		capacity.Resource() != "stream leases" || capacity.Maximum() != maximum {
		t.Fatalf("typed stream capacity = %#v", capacity)
	}

	observations[0].Close()
	observations[0] = nil
	replacement, err := fixture.host.SnapshotInteractions(t.Context(), fixture.session)
	if err != nil {
		t.Fatalf("replacement observation = %v", err)
	}
	observations[0] = replacement
}

func receivePendingDelta(t *testing.T, observation *InteractionObservation) Delta {
	t.Helper()
	select {
	case delta := <-observation.Deltas():
		return delta
	case <-time.After(time.Second):
		t.Fatal("pending interaction delta was not delivered")
		return Delta{}
	}
}

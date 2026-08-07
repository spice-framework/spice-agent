package daemon_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/daemon"
	"github.com/spice-framework/spice-agent/interaction"
)

func TestPendingHubIsolatesStableClientsAndRejectsWrongOwner(t *testing.T) {
	hub := mustPendingHub(t, pendingHubLimits())
	defer hub.Close()
	scopeA := mustScope(t, "run-a")
	scopeB := mustScope(t, "run-b")
	if _, err := hub.BindRun("client-a", scopeA); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.BindRun("client-b", scopeB); err != nil {
		t.Fatal(err)
	}
	clientA, err := hub.Subscribe(t.Context(), "client-a")
	if err != nil {
		t.Fatal(err)
	}
	clientB, err := hub.Subscribe(t.Context(), "client-b")
	if err != nil {
		t.Fatal(err)
	}
	doneA := startHubPending(t, hub, t.Context(), scopeA, mustRequest(t, "prompt-a"))
	doneB := startHubPending(t, hub, t.Context(), scopeB, mustRequest(t, "prompt-b"))
	if delta := receiveDelta(t, clientA); delta.Pending.Scope.RunID() != "run-a" {
		t.Fatalf("client A received %#v", delta)
	}
	if delta := receiveDelta(t, clientB); delta.Pending.Scope.RunID() != "run-b" {
		t.Fatalf("client B received %#v", delta)
	}
	if err = hub.Respond("client-b", scopeA, mustResponse(t, "prompt-a")); !errors.Is(err, daemon.ErrRunNotBound) {
		t.Fatalf("wrong-client response = %v", err)
	}
	if err = hub.Respond("client-a", scopeA, mustResponse(t, "prompt-a")); err != nil {
		t.Fatal(err)
	}
	if err = hub.Respond("client-b", scopeB, mustResponse(t, "prompt-b")); err != nil {
		t.Fatal(err)
	}
	if err = <-doneA; err != nil {
		t.Fatal(err)
	}
	if err = <-doneB; err != nil {
		t.Fatal(err)
	}
}

func TestRunBindingReleaseDrainsAcceptedWorkBeforeReclaim(t *testing.T) {
	limits := pendingHubLimits()
	limits.Runs = 1
	limits.RunsPerClient = 1
	hub := mustPendingHub(t, limits)
	defer hub.Close()
	scope := mustScope(t, "run")
	binding, err := hub.BindRun("client", scope)
	if err != nil {
		t.Fatal(err)
	}
	if binding.ClientID() != "client" || binding.RunID() != "run" {
		t.Fatalf("binding identity = %q/%q", binding.ClientID(), binding.RunID())
	}
	subscription, err := hub.Subscribe(t.Context(), "client")
	if err != nil {
		t.Fatal(err)
	}
	done := startHubPending(t, hub, t.Context(), scope, mustRequest(t, "accepted"))
	_ = receiveDelta(t, subscription)
	binding.Release()
	if _, err = hub.Request(t.Context(), scope, mustRequest(t, "late")); !errors.Is(err, daemon.ErrRunNotBound) {
		t.Fatalf("request after release = %v", err)
	}
	waitContext, cancelWait := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancelWait()
	if err = binding.WaitReleased(waitContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("released before accepted request finished: %v", err)
	}
	if _, err = hub.BindRun("other", scope); !errors.Is(err, daemon.ErrRunAlreadyBound) {
		t.Fatalf("draining run rebound: %v", err)
	}
	if err = hub.Respond("client", scope, mustResponse(t, "accepted")); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = binding.WaitReleased(t.Context()); err != nil {
		t.Fatal(err)
	}
	rebound, err := hub.BindRun("other", scope)
	if err != nil {
		t.Fatalf("completed binding capacity was not reclaimed: %v", err)
	}
	rebound.Release()
	if err = rebound.WaitReleased(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestPendingHubReconnectFencePreservesCompleteFirstState(t *testing.T) {
	hub := mustPendingHub(t, pendingHubLimits())
	defer hub.Close()
	scope := mustScope(t, "run")
	if _, err := hub.BindRun("client", scope); err != nil {
		t.Fatal(err)
	}
	old, err := hub.Subscribe(t.Context(), "client")
	if err != nil {
		t.Fatal(err)
	}
	done := startHubPending(t, hub, t.Context(), scope, mustRequest(t, "prompt"))
	opened := receiveDelta(t, old)
	if err = hub.FenceObservers("client"); err != nil {
		t.Fatal(err)
	}
	if err = old.Wait(t.Context()); !errors.Is(err, daemon.ErrObserverFenced) {
		t.Fatalf("fenced observer = %v", err)
	}
	reconnected, err := hub.Subscribe(t.Context(), "client")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := reconnected.Snapshot()
	if snapshot.Revision != opened.Revision || len(snapshot.Pending) != 1 || snapshot.Pending[0].Request.ID() != "prompt" {
		t.Fatalf("reconnect snapshot = %#v", snapshot)
	}
	if err = hub.Respond("client", scope, mustResponse(t, "prompt")); err != nil {
		t.Fatal(err)
	}
	closed := receiveDelta(t, reconnected)
	if closed.Kind != daemon.DeltaClosed || closed.Revision != snapshot.Revision+1 {
		t.Fatalf("reconnect tail = %#v", closed)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPendingHubEnforcesRunClientAndQueueReservations(t *testing.T) {
	limits := pendingHubLimits()
	limits.Clients = 2
	limits.Runs = 2
	limits.RunsPerClient = 1
	limits.Observers = 2
	limits.ObserversPerClient = 1
	limits.ReservedQueueEntries = 8
	limits.ReservedQueueEntriesPerClient = 4
	limits.ReservedQueueBytes = 8 << 20
	limits.ReservedQueueBytesPerClient = 4 << 20
	hub := mustPendingHub(t, limits)
	defer hub.Close()
	first, _ := hub.BindRun("first", mustScope(t, "first"))
	if _, err := hub.BindRun("first", mustScope(t, "same-client")); !errors.Is(err, daemon.ErrRunBindingCapacity) {
		t.Fatalf("per-client run cap = %v", err)
	}
	second, err := hub.BindRun("second", mustScope(t, "second"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = hub.BindRun("third", mustScope(t, "global")); !errors.Is(err, daemon.ErrRunBindingCapacity) {
		t.Fatalf("global run cap = %v", err)
	}
	if _, err = hub.Subscribe(t.Context(), "third"); !errors.Is(err, daemon.ErrObserverCapacity) {
		t.Fatalf("global client cap = %v", err)
	}
	if _, err = hub.Subscribe(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	if _, err = hub.Subscribe(t.Context(), "first"); !errors.Is(err, daemon.ErrObserverCapacity) {
		t.Fatalf("per-client observer cap = %v", err)
	}
	if _, err = hub.Subscribe(t.Context(), "second"); err != nil {
		t.Fatal(err)
	}
	first.Release()
	second.Release()
}

func TestPendingLimitsRejectIncompleteAndContradictoryBudgets(t *testing.T) {
	valid := pendingHubLimits()
	tests := []struct {
		name   string
		mutate func(*daemon.PendingLimits)
	}{
		{"zero runs", func(value *daemon.PendingLimits) { value.Runs = 0 }},
		{"client runs exceed global", func(value *daemon.PendingLimits) { value.RunsPerClient = value.Runs + 1 }},
		{"client pending bytes exceed global", func(value *daemon.PendingLimits) { value.PendingBytesPerClient = value.PendingBytes + 1 }},
		{"global reservation cannot cover observers", func(value *daemon.PendingLimits) { value.ReservedQueueEntries-- }},
		{"client reservation cannot cover observers", func(value *daemon.PendingLimits) { value.ReservedQueueBytesPerClient-- }},
		{"actual client queue exceeds global", func(value *daemon.PendingLimits) { value.QueuedEntriesPerClient = value.QueuedEntries + 1 }},
		{"actual entries below reservation", func(value *daemon.PendingLimits) { value.QueuedEntries = value.ReservedQueueEntries - 1 }},
		{"actual client entries below reservation", func(value *daemon.PendingLimits) {
			value.QueuedEntriesPerClient = value.ReservedQueueEntriesPerClient - 1
		}},
		{"actual bytes below reservation", func(value *daemon.PendingLimits) { value.QueuedBytes = value.ReservedQueueBytes - 1 }},
		{"actual client bytes below reservation", func(value *daemon.PendingLimits) { value.QueuedBytesPerClient = value.ReservedQueueBytesPerClient - 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := valid
			test.mutate(&limits)
			if _, err := daemon.NewPendingHub(limits); err == nil {
				t.Fatal("invalid limits succeeded")
			}
		})
	}
}

func TestDefaultPendingLimitsAreImmediatelyUsableAndFullyFunded(t *testing.T) {
	limits := daemon.DefaultPendingLimits()
	hub, err := daemon.NewPendingHub(limits)
	if err != nil {
		t.Fatalf("default limits = %v", err)
	}
	hub.Close()
	if limits.QueuedEntries < limits.ReservedQueueEntries ||
		limits.QueuedEntriesPerClient < limits.ReservedQueueEntriesPerClient ||
		limits.QueuedBytes < limits.ReservedQueueBytes ||
		limits.QueuedBytesPerClient < limits.ReservedQueueBytesPerClient {
		t.Fatalf("default actual budgets do not fund reservations: %#v", limits)
	}
	large := largeRequest(t)
	deltaBytesLowerBound := len(large.Prompt()) + len(large.Schema())
	if limits.ObserverQueueBytes < deltaBytesLowerBound {
		t.Fatalf("default observer byte budget %d cannot hold a maximum request payload %d", limits.ObserverQueueBytes, deltaBytesLowerBound)
	}
}

func TestPendingHubPublishesTypedFailureClasses(t *testing.T) {
	limits := pendingHubLimits()
	limits.Runs = 1
	limits.RunsPerClient = 1
	limits.Pending = 1
	limits.PendingPerClient = 1
	limits.Observers = 1
	limits.ObserversPerClient = 1
	limits.ReservedQueueEntries = limits.ObserverQueueEntries
	limits.ReservedQueueEntriesPerClient = limits.ObserverQueueEntries
	limits.ReservedQueueBytes = limits.ObserverQueueBytes
	limits.ReservedQueueBytesPerClient = limits.ObserverQueueBytes
	limits.QueuedEntries = limits.ReservedQueueEntries
	limits.QueuedEntriesPerClient = limits.ReservedQueueEntriesPerClient
	limits.QueuedBytes = limits.ReservedQueueBytes
	limits.QueuedBytesPerClient = limits.ReservedQueueBytesPerClient
	hub := mustPendingHub(t, limits)
	defer hub.Close()
	scope := mustScope(t, "run")
	_, err := hub.BindRun("client", scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = hub.BindRun("client", scope); !errors.Is(err, daemon.ErrRunAlreadyBound) {
		t.Fatalf("duplicate binding = %v", err)
	}
	if _, err = hub.BindRun("client", mustScope(t, "other")); !errors.Is(err, daemon.ErrRunBindingCapacity) {
		t.Fatalf("binding capacity = %v", err)
	}
	subscription, err := hub.Subscribe(t.Context(), "client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = hub.Subscribe(t.Context(), "client"); !errors.Is(err, daemon.ErrObserverCapacity) {
		t.Fatalf("observer capacity = %v", err)
	}
	done := startHubPending(t, hub, t.Context(), scope, mustRequest(t, "first"))
	_ = receiveDelta(t, subscription)
	if _, err = hub.Request(t.Context(), scope, mustRequest(t, "second")); !errors.Is(err, daemon.ErrPendingCapacity) {
		t.Fatalf("pending capacity = %v", err)
	}
	if err := hub.Respond("client", scope, mustResponse(t, "missing")); !errors.Is(err, daemon.ErrInteractionNotPending) {
		t.Fatalf("missing interaction = %v", err)
	}
	if err := hub.Respond("client", scope, mustResponse(t, "first")); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func pendingHubLimits() daemon.PendingLimits {
	return daemon.PendingLimits{
		Clients: 4, Runs: 8, RunsPerClient: 4,
		Pending: 8, PendingPerClient: 4,
		PendingBytes: 16 << 20, PendingBytesPerClient: 8 << 20,
		Observers: 4, ObserversPerClient: 2,
		ObserverQueueEntries: 4, ObserverQueueBytes: 4 << 20,
		ReservedQueueEntries: 16, ReservedQueueEntriesPerClient: 8,
		ReservedQueueBytes: 16 << 20, ReservedQueueBytesPerClient: 8 << 20,
		QueuedEntries: 16, QueuedEntriesPerClient: 8,
		QueuedBytes: 16 << 20, QueuedBytesPerClient: 8 << 20,
	}
}

func mustPendingHub(t *testing.T, limits daemon.PendingLimits) *daemon.PendingHub {
	t.Helper()
	hub, err := daemon.NewPendingHub(limits)
	if err != nil {
		t.Fatal(err)
	}
	if hub == nil {
		t.Fatal("pending hub is nil")
	}
	return hub
}

func startHubPending(
	t *testing.T,
	hub *daemon.PendingHub,
	ctx context.Context,
	scope interaction.Scope,
	request interaction.Request,
) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		response, err := hub.Request(ctx, scope, request)
		if err == nil && string(response.Value()) != "true" {
			err = errors.New("unexpected interaction response")
		}
		done <- err
	}()
	return done
}

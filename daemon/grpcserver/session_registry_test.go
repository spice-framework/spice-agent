package grpcserver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/proto"
)

func TestNegotiatedSessionRegistryRejectsInvalidConstruction(t *testing.T) {
	t.Parallel()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name     string
		root     context.Context //nolint:containedctx // table input deliberately exercises root contexts.
		capacity int
		want     error
	}{
		{name: "nil root", capacity: 1, want: errNegotiatedSessionInvalid},
		{name: "zero capacity", root: t.Context(), want: errNegotiatedSessionInvalid},
		{name: "excess capacity", root: t.Context(), capacity: maximumNegotiatedSessions + 1, want: errNegotiatedSessionInvalid},
		{name: "canceled root", root: canceled, capacity: 1, want: errNegotiatedSessionClosed},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := newNegotiatedSessionRegistry(test.root, test.capacity); !errors.Is(err, test.want) {
				t.Fatalf("new registry = %v, want %v", err, test.want)
			}
		})
	}
}

func TestNegotiatedSessionRegistryStoresExactSessionAndClonedResponse(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 2, 2)
	session := freshRegistrySession(t, store)
	response := registryInitializeResponse(session)
	if err := registry.installFresh(session, response); err != nil {
		t.Fatal(err)
	}
	response.Server.Component = "mutated-source"
	response.Definitions.Revision = "mutated-source"

	first, err := registry.lookup(session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	if first.session.ClientID() != session.ClientID() || first.session.Epoch() != session.Epoch() ||
		first.session.Context() != session.Context() {
		t.Fatalf("lookup did not retain exact daemon session: %#v", first.session)
	}
	if first.response.GetServer().GetComponent() != "spice-agentd" ||
		first.response.GetDefinitions().GetRevision() != "catalog-v1" {
		t.Fatalf("stored response was mutated: %#v", first.response)
	}
	first.response.Server.Component = "mutated-result"
	first.response.Definitions.Revision = "mutated-result"
	second, err := registry.lookup(session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	if second.response.GetServer().GetComponent() != "spice-agentd" ||
		second.response.GetDefinitions().GetRevision() != "catalog-v1" {
		t.Fatalf("lookup response shared mutable storage: %#v", second.response)
	}
}

func TestNegotiatedSessionRegistryRejectsInvalidEntriesWithoutDetails(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 4, 4)
	session := freshRegistrySession(t, store)
	valid := registryInitializeResponse(session)
	wrongClient := proto.CloneOf(valid)
	wrongClient.ClientId = "different-client"
	wrongEpoch := proto.CloneOf(valid)
	wrongEpoch.OwnershipEpoch++
	failed := &enginev1.InitializeResponse{
		Status: &commonv1.Status{
			Code: commonv1.ErrorCode_ERROR_CODE_INTERNAL, Message: "failed",
		},
	}

	for _, test := range []struct {
		name     string
		session  daemon.Session
		response *enginev1.InitializeResponse
	}{
		{name: "zero session", response: valid},
		{name: "nil response", session: session},
		{name: "zero response", session: session, response: &enginev1.InitializeResponse{}},
		{name: "wrong client", session: session, response: wrongClient},
		{name: "wrong epoch", session: session, response: wrongEpoch},
		{name: "failure response", session: session, response: failed},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := registry.installFresh(test.session, test.response)
			if !errors.Is(err, errNegotiatedSessionInvalid) || strings.Contains(err.Error(), session.ClientID()) {
				t.Fatalf("invalid install = %v", err)
			}
		})
	}

	stale := session
	next, err := store.Reconnect(session.ClientID(), session.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.installFresh(stale, valid); !errors.Is(err, errNegotiatedSessionInvalid) {
		t.Fatalf("canceled session install = %v", err)
	}
	if err := registry.installFresh(next, registryInitializeResponse(next)); !errors.Is(err, errNegotiatedSessionInvalid) {
		t.Fatalf("non-fresh epoch install = %v", err)
	}
}

func TestNegotiatedSessionRegistryHidesDuplicateUnknownAndStaleOwnership(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 3, 1)
	first := freshRegistrySession(t, store)
	response := registryInitializeResponse(first)
	if err := registry.installFresh(first, response); err != nil {
		t.Fatal(err)
	}
	duplicateErr := registry.installFresh(first, response)
	_, unknownErr := registry.lookup("unknown-client", first.Epoch())
	_, staleErr := registry.lookup(first.ClientID(), first.Epoch()+1)
	for name, err := range map[string]error{
		"duplicate": duplicateErr,
		"unknown":   unknownErr,
		"stale":     staleErr,
	} {
		if !errors.Is(err, errNegotiatedSessionUnavailable) || err.Error() != errNegotiatedSessionUnavailable.Error() ||
			strings.Contains(err.Error(), first.ClientID()) {
			t.Fatalf("%s ownership error = %v", name, err)
		}
	}

	second := freshRegistrySession(t, store)
	if err := registry.installFresh(second, registryInitializeResponse(second)); !errors.Is(err, errNegotiatedSessionCapacity) {
		t.Fatalf("capacity install = %v", err)
	}
}

func TestNegotiatedSessionRegistryReconnectUsesExactEpochCAS(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 2, 2)
	first := freshRegistrySession(t, store)
	if err := registry.installFresh(first, registryInitializeResponse(first)); err != nil {
		t.Fatal(err)
	}
	next, err := store.Reconnect(first.ClientID(), first.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.lookup(first.ClientID(), first.Epoch()); !errors.Is(err, errNegotiatedSessionUnavailable) {
		t.Fatalf("fenced old lookup = %v", err)
	}
	if err = registry.replaceReconnect(first.ClientID(), first.Epoch(), next, registryInitializeResponse(next)); err != nil {
		t.Fatal(err)
	}
	current, err := registry.lookup(next.ClientID(), next.Epoch())
	if err != nil || current.session.Context() != next.Context() || current.response.GetOwnershipEpoch() != next.Epoch() {
		t.Fatalf("reconnected lookup = %#v, %v", current, err)
	}
	if err = registry.replaceReconnect(first.ClientID(), first.Epoch(), next, registryInitializeResponse(next)); !errors.Is(err, errNegotiatedSessionUnavailable) {
		t.Fatalf("duplicate reconnect CAS = %v", err)
	}

	third, err := store.Reconnect(next.ClientID(), next.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.replaceReconnect(next.ClientID(), next.Epoch()+1, third, registryInitializeResponse(third)); !errors.Is(err, errNegotiatedSessionInvalid) {
		t.Fatalf("invalid reconnect relation = %v", err)
	}
	if err = registry.replaceReconnect(next.ClientID(), next.Epoch(), third, registryInitializeResponse(third)); err != nil {
		t.Fatal(err)
	}
	if _, err = registry.lookup(third.ClientID(), third.Epoch()); err != nil {
		t.Fatalf("third epoch lookup = %v", err)
	}
}

func TestNegotiatedSessionRegistryCloseAndRootCancellationDoNotOwnSessionStore(t *testing.T) {
	t.Parallel()
	storeRoot, cancelStore := context.WithCancel(context.Background())
	t.Cleanup(cancelStore)
	store, err := daemon.NewSessionStore(storeRoot, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Shutdown(context.Background()) })
	registryRoot, cancelRegistry := context.WithCancel(context.Background())
	registry, err := newNegotiatedSessionRegistry(registryRoot, 2)
	if err != nil {
		t.Fatal(err)
	}
	session := freshRegistrySession(t, store)
	if err = registry.installFresh(session, registryInitializeResponse(session)); err != nil {
		t.Fatal(err)
	}

	registry.close()
	registry.close()
	registry.mu.Lock()
	remaining := len(registry.entries)
	registry.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("closed registry retained %d session entries", remaining)
	}
	if _, err = registry.lookup(session.ClientID(), session.Epoch()); !errors.Is(err, errNegotiatedSessionClosed) {
		t.Fatalf("closed lookup = %v", err)
	}
	if session.Context().Err() != nil || store.Check(session.ClientID(), session.Epoch()) != nil {
		t.Fatal("registry close fenced its non-owned daemon session")
	}
	if err = registry.installFresh(session, registryInitializeResponse(session)); !errors.Is(err, errNegotiatedSessionClosed) {
		t.Fatalf("closed install = %v", err)
	}

	secondRegistry, err := newNegotiatedSessionRegistry(registryRoot, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err = secondRegistry.installFresh(session, registryInitializeResponse(session)); err != nil {
		t.Fatal(err)
	}
	cancelRegistry()
	if _, err = secondRegistry.lookup(session.ClientID(), session.Epoch()); !errors.Is(err, errNegotiatedSessionClosed) {
		t.Fatalf("root-canceled lookup = %v", err)
	}
	if session.Context().Err() != nil || store.Check(session.ClientID(), session.Epoch()) != nil {
		t.Fatal("registry root cancellation fenced its non-owned daemon session")
	}

	var nilRegistry *negotiatedSessionRegistry
	if _, err = nilRegistry.lookup(session.ClientID(), session.Epoch()); !errors.Is(err, errNegotiatedSessionClosed) {
		t.Fatalf("nil registry lookup = %v", err)
	}
	if err = nilRegistry.installFresh(session, registryInitializeResponse(session)); !errors.Is(err, errNegotiatedSessionClosed) {
		t.Fatalf("nil registry install = %v", err)
	}
	if err = nilRegistry.replaceReconnect(session.ClientID(), session.Epoch(), daemon.Session{}, nil); !errors.Is(err, errNegotiatedSessionClosed) {
		t.Fatalf("nil registry reconnect = %v", err)
	}
	nilRegistry.close()
}

func TestNegotiatedSessionRegistryConcurrentLookupIsDeterministic(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 2, 2)
	session := freshRegistrySession(t, store)
	if err := registry.installFresh(session, registryInitializeResponse(session)); err != nil {
		t.Fatal(err)
	}

	const readers = 32
	const iterations = 64
	failures := make(chan error, readers)
	var wait sync.WaitGroup
	for range readers {
		wait.Go(func() {
			for range iterations {
				value, err := registry.lookup(session.ClientID(), session.Epoch())
				if err != nil || value.response.GetClientId() != session.ClientID() ||
					value.response.GetOwnershipEpoch() != session.Epoch() {
					if err == nil {
						err = errors.New("lookup returned inconsistent ownership")
					}
					failures <- err
					return
				}
				value.response.ClientId = "mutated-reader"
			}
		})
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
}

func newNegotiatedRegistryFixture(
	t *testing.T,
	sessionCapacity int,
	registryCapacity int,
) (*daemon.SessionStore, *negotiatedSessionRegistry) {
	t.Helper()
	store, err := daemon.NewSessionStore(t.Context(), sessionCapacity)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newNegotiatedSessionRegistry(t.Context(), registryCapacity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		registry.close()
		if shutdownErr := store.Shutdown(context.Background()); shutdownErr != nil {
			t.Errorf("shutdown session store: %v", shutdownErr)
		}
	})
	return store, registry
}

func freshRegistrySession(t *testing.T, store *daemon.SessionStore) daemon.Session {
	t.Helper()
	session, err := store.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func registryInitializeResponse(session daemon.Session) *enginev1.InitializeResponse {
	limits := &commonv1.Limits{
		MaxMessageBytes: 2 << 20, MaxCollectionItems: 64,
		MaxReplayEvents: 128, MaxReplayBytes: 4 << 20,
		MaxConcurrentStreams: 4, MaxActiveRuns: 8,
	}
	return &enginev1.InitializeResponse{
		Status:   commonv1.OKStatus(),
		Protocol: &commonv1.ProtocolVersion{Major: 1, Minor: 1},
		Server: &commonv1.BuildIdentity{
			Component: "spice-agentd", Version: "v0.1.0-preview.1",
			Commit: "0123456789ab", GoVersion: "go1.26.5",
		},
		Capabilities: &commonv1.CapabilitySet{Names: []string{"events"}},
		Limits:       proto.CloneOf(limits),
		Health: &commonv1.Health{
			State: commonv1.HealthState_HEALTH_STATE_READY, Limits: proto.CloneOf(limits),
		},
		ClientId: session.ClientID(), OwnershipEpoch: session.Epoch(),
		Definitions: &enginev1.DefinitionSet{
			Revision: "catalog-v1",
			Definitions: []*enginev1.Definition{{
				Id: "coding", Revision: "v1", Model: "reasoning", MaxTurns: 8,
			}},
		},
	}
}

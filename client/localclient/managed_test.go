package localclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client/managed"
	"github.com/spice-framework/spice-agent/daemon/endpoint"
)

func TestDiscoveryMapsOnlyExactEndpointAbsence(t *testing.T) {
	t.Parallel()
	exact, err := newDiscovery(fixtureEndpointDiscovery{err: endpoint.ErrNotFound})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exact.Discover(t.Context()); err != managed.ErrEndpointNotFound { //nolint:errorlint // exact mapping is the contract.
		t.Fatalf("exact absence = %v", err)
	}
	joinedSource := errors.Join(endpoint.ErrNotFound, errFixture)
	joined, err := newDiscovery(fixtureEndpointDiscovery{err: joinedSource})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = joined.Discover(t.Context()); err == managed.ErrEndpointNotFound || //nolint:errorlint // wrapped absence must remain hard.
		!errors.Is(err, errFixture) {
		t.Fatalf("joined hard failure = %v", err)
	}
	hard, err := newDiscovery(fixtureEndpointDiscovery{err: errFixture})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = hard.Discover(t.Context()); !errors.Is(err, errFixture) {
		t.Fatalf("hard discovery failure = %v", err)
	}
}

func TestStoreBackedDiscoveryAndStartupLock(t *testing.T) {
	t.Parallel()
	store, err := endpoint.OpenStore(endpoint.StoreConfig{
		Directory: currentStoreDirectory(t), PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	discovery, err := NewDiscovery(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = discovery.Discover(t.Context()); err != managed.ErrEndpointNotFound { //nolint:errorlint // exact mapping is the contract.
		t.Fatalf("empty store discovery = %v", err)
	}
	metadata := currentMetadataFixture(t)
	publication, err := store.Publish(t.Context(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = publication.Close() })
	resolved, err := discovery.Discover(t.Context())
	if err != nil || resolved == nil {
		t.Fatalf("published discovery = %T, %v", resolved, err)
	}
	again, err := discovery.Discover(t.Context())
	if err != nil || again != resolved {
		t.Fatalf("repeated discovery did not reuse connector = %T/%T, %v", resolved, again, err)
	}

	startup, err := NewStartupLock(store)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := startup.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err = startup.Acquire(blocked); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended Acquire error = %v", err)
	}
	if err = lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err = lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryReplacesChangedEndpointAndClosesOnAbsence(t *testing.T) {
	t.Parallel()
	firstMetadata := currentMetadataFixture(t)
	source := &mutableEndpointDiscovery{metadata: firstMetadata}
	discovery, err := newDiscovery(source)
	if err != nil {
		t.Fatal(err)
	}
	firstValue, err := discovery.Discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first, ok := firstValue.(*Connector)
	if !ok {
		t.Fatalf("first discovered connector = %T", firstValue)
	}
	secondMetadata := currentMetadataFixture(t)
	source.set(secondMetadata, nil)
	secondValue, err := discovery.Discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, ok := secondValue.(*Connector)
	if !ok {
		t.Fatalf("second discovered connector = %T", secondValue)
	}
	if first == second || first.available() {
		t.Fatal("changed endpoint did not replace and close its cached connector")
	}
	source.set(endpoint.Metadata{}, errFixture)
	if _, err = discovery.Discover(t.Context()); !errors.Is(err, errFixture) || !second.available() {
		t.Fatalf("hard discovery error changed last-known connector: %v", err)
	}
	source.set(endpoint.Metadata{}, endpoint.ErrNotFound)
	if _, err = discovery.Discover(t.Context()); err != managed.ErrEndpointNotFound || second.available() { //nolint:errorlint // exact mapping is the contract.
		t.Fatalf("absence discovery = %v, connector available %t", err, second.available())
	}
	if err = discovery.Close(); err != nil {
		t.Fatal(err)
	}
	if err = discovery.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = discovery.Discover(t.Context()); err == nil {
		t.Fatal("closed discovery succeeded")
	}
}

func TestDiscoveryClosePreventsFurtherEndpointIO(t *testing.T) {
	t.Parallel()
	source := &countingEndpointDiscovery{metadata: currentMetadataFixture(t)}
	discovery, err := newDiscovery(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = discovery.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = discovery.Discover(t.Context()); err == nil {
		t.Fatal("closed discovery succeeded")
	}
	if calls := source.calls.Load(); calls != 0 {
		t.Fatalf("closed discovery performed %d endpoint reads", calls)
	}
}

func TestManagedAdapterValidationFailuresAndCancellation(t *testing.T) {
	t.Parallel()
	if _, err := NewDiscovery(nil); err == nil {
		t.Fatal("nil discovery store accepted")
	}
	if _, err := newDiscovery(nil); err == nil {
		t.Fatal("nil discovery source accepted")
	}
	if _, err := (*Discovery)(nil).Discover(t.Context()); err == nil {
		t.Fatal("nil discovery used")
	}
	zero, err := newDiscovery(fixtureEndpointDiscovery{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = zero.Discover(t.Context()); err == nil {
		t.Fatal("invalid discovered metadata accepted")
	}
	if _, err = NewStartupLock(nil); err == nil {
		t.Fatal("nil startup store accepted")
	}
	if _, err = newStartupLock(nil); err == nil {
		t.Fatal("nil startup source accepted")
	}
	if _, err = (*StartupLock)(nil).Acquire(t.Context()); err == nil {
		t.Fatal("nil startup lock used")
	}
	startup, err := newStartupLock(fixtureEndpointStartup{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = startup.Acquire(t.Context()); err == nil {
		t.Fatal("nil startup lease accepted")
	}
	startup, err = newStartupLock(fixtureEndpointStartup{lease: &endpoint.StartupLease{}, err: errFixture})
	if err != nil {
		t.Fatal(err)
	}
	if lease, acquireErr := startup.Acquire(t.Context()); !errors.Is(acquireErr, errFixture) || lease == nil {
		t.Fatalf("lease with failure = %T, %v", lease, acquireErr)
	}
	startup, err = newStartupLock(blockingEndpointStartup{})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = startup.Acquire(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Acquire error = %v", err)
	}
}

func TestManagedAdaptersRedactPrivateState(t *testing.T) {
	t.Parallel()
	metadata := currentMetadataFixture(t)
	discovery, err := newDiscovery(fixtureEndpointDiscovery{metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	startup, err := newStartupLock(fixtureEndpointStartup{lease: &endpoint.StartupLease{}})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := metadata.Token().AuthorizationValue()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{discovery, startup} {
		formatted := []string{fmt.Sprintf("%v", value), fmt.Sprintf("%#v", value), fmt.Sprintf("%q", value)}
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		formatted = append(formatted, string(encoded))
		var output bytes.Buffer
		slog.New(slog.NewJSONHandler(&output, nil)).Info("adapter", "value", value)
		formatted = append(formatted, output.String())
		for _, candidate := range formatted {
			if strings.Contains(candidate, metadata.Address()) || strings.Contains(candidate, authorization) {
				t.Fatalf("adapter formatting leaked endpoint state: %q", candidate)
			}
		}
	}
}

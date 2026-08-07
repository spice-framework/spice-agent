package localclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client/managed"
	"github.com/spice-framework/spice-agent/daemon/endpoint"
)

func TestExplicitDiscoveryRequiresExactProtectedEndpointAndCachesConnector(t *testing.T) {
	t.Parallel()
	metadata := currentMetadataFixture(t)
	source := &explicitFixtureSource{metadata: metadata}
	discovery, err := newExplicitDiscovery(source, metadata.Address())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = discovery.Close() })

	first, err := discovery.Discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := discovery.Discover(t.Context())
	if err != nil || first != second || source.calls.Load() != 2 {
		t.Fatalf("explicit discoveries = %T/%T, calls=%d, err=%v", first, second, source.calls.Load(), err)
	}
	local, ok := first.(*Connector)
	if !ok || local.metadata.Address() != metadata.Address() ||
		!local.metadata.Token().Equal(metadata.Token()) || local.opened != nil {
		t.Fatalf("explicit connector was not lazy and metadata-backed: %#v", first)
	}
}

func TestExplicitDiscoveryReadsOnlyProtectedStoreState(t *testing.T) {
	t.Parallel()
	directory := currentStoreDirectory(t)
	store, err := endpoint.OpenStore(endpoint.StoreConfig{
		Directory: directory, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	metadata := currentMetadataFixture(t)

	empty, err := NewExplicitDiscovery(store, metadata.Address())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = empty.Discover(t.Context()); err == nil ||
		err == managed.ErrEndpointNotFound { //nolint:errorlint // Exact identity alone authorizes managed startup.
		t.Fatalf("empty explicit store authorized startup: %T %v", err, err)
	}

	publication, err := store.Publish(t.Context(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := NewExplicitDiscovery(store, metadata.Address())
	if err != nil {
		t.Fatal(err)
	}
	if resolved, discoverErr := exact.Discover(t.Context()); discoverErr != nil || resolved == nil {
		t.Fatalf("published explicit discovery = %T, %v", resolved, discoverErr)
	}
	mismatch, err := NewExplicitDiscovery(store, otherPlatformAddress())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mismatch.Discover(t.Context()); !errors.Is(err, ErrExplicitEndpointMismatch) {
		t.Fatalf("published explicit mismatch = %T %v", err, err)
	}
	if err = publication.Close(); err != nil {
		t.Fatal(err)
	}
	stale, err := NewExplicitDiscovery(store, metadata.Address())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stale.Discover(t.Context()); err == nil ||
		err == managed.ErrEndpointNotFound { //nolint:errorlint // Exact identity alone authorizes managed startup.
		t.Fatalf("stale explicit store authorized startup: %T %v", err, err)
	}
}

func TestExplicitDiscoveryRejectsMalformedProtectedStoreRecord(t *testing.T) {
	t.Parallel()
	directory := currentStoreDirectory(t)
	store, err := endpoint.OpenStore(endpoint.StoreConfig{
		Directory: directory, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = os.WriteFile(filepath.Join(directory, "endpoint.json"), []byte("{malformed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := currentMetadataFixture(t)
	discovery, err := NewExplicitDiscovery(store, metadata.Address())
	if err != nil {
		t.Fatal(err)
	}
	_, err = discovery.Discover(t.Context())
	if err == nil || err == managed.ErrEndpointNotFound { //nolint:errorlint // Exact identity alone authorizes managed startup.
		t.Fatalf("malformed explicit store authorized startup: %T %v", err, err)
	}
	assertExplicitErrorRedacted(t, err, metadata, "")
}

func TestExplicitDiscoveryRejectsMismatchWithoutLeakingEndpointMaterial(t *testing.T) {
	t.Parallel()
	metadata := currentMetadataFixture(t)
	requested := otherPlatformAddress()
	discovery, err := newExplicitDiscovery(&explicitFixtureSource{metadata: metadata}, requested)
	if err != nil {
		t.Fatal(err)
	}
	_, err = discovery.Discover(t.Context())
	if !errors.Is(err, ErrExplicitEndpointMismatch) ||
		err == ErrExplicitEndpointMismatch { //nolint:errorlint // Public discovery must add a safe attach boundary.
		t.Fatalf("explicit mismatch = %T %v", err, err)
	}
	assertExplicitErrorRedacted(t, err, metadata, requested)
}

func TestExplicitDiscoveryTreatsStaleAndMalformedMetadataAsHardAttachFailures(t *testing.T) {
	t.Parallel()
	metadata := currentMetadataFixture(t)
	for name, sourceErr := range map[string]error{
		"stale":     endpoint.ErrNotFound,
		"malformed": errors.New("local endpoint metadata is malformed"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			discovery, err := newExplicitDiscovery(
				&explicitFixtureSource{err: sourceErr}, metadata.Address(),
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = discovery.Discover(t.Context())
			if err == nil ||
				err == endpoint.ErrNotFound || //nolint:errorlint // Exact raw absence must not escape explicit attach.
				err == managed.ErrEndpointNotFound { //nolint:errorlint // Exact managed absence alone authorizes startup.
				t.Fatalf("explicit %s failure = %T %v", name, err, err)
			}
			if !errors.Is(err, sourceErr) {
				t.Fatalf("explicit %s lost source cause: %v", name, err)
			}
			assertExplicitErrorRedacted(t, err, metadata, "")
		})
	}
}

func TestExplicitDiscoveryCancellationPreventsOrDiscardsMetadata(t *testing.T) {
	t.Parallel()
	metadata := currentMetadataFixture(t)

	t.Run("before read", func(t *testing.T) {
		t.Parallel()

		source := &explicitFixtureSource{metadata: metadata}
		discovery, err := newExplicitDiscovery(source, metadata.Address())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err = discovery.Discover(ctx); !errors.Is(err, context.Canceled) || source.calls.Load() != 0 {
			t.Fatalf("pre-cancel discovery calls=%d, err=%v", source.calls.Load(), err)
		}
	})

	t.Run("after read", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancelCause(t.Context())
		source := &explicitFixtureSource{metadata: metadata, after: func() { cancel(errExplicitCancel) }}
		discovery, err := newExplicitDiscovery(source, metadata.Address())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = discovery.Discover(ctx); !errors.Is(err, errExplicitCancel) || source.calls.Load() != 1 {
			t.Fatalf("post-read discovery calls=%d, err=%v", source.calls.Load(), err)
		}
	})
}

func TestExplicitDiscoveryRejectsInvalidConstructionAndRedactsFormatting(t *testing.T) {
	t.Parallel()
	metadata := currentMetadataFixture(t)
	if _, err := NewExplicitDiscovery(nil, metadata.Address()); err == nil {
		t.Fatal("nil explicit endpoint store succeeded")
	}
	if _, err := newExplicitDiscovery(nil, metadata.Address()); err == nil {
		t.Fatal("nil explicit endpoint source succeeded")
	}
	for _, address := range []string{"", " endpoint", "endpoint\n"} {
		if _, err := newExplicitDiscovery(&explicitFixtureSource{}, address); err == nil {
			t.Fatalf("invalid explicit address %q succeeded", address)
		}
	}
	if _, err := (*ExplicitDiscovery)(nil).Discover(t.Context()); err == nil {
		t.Fatal("nil explicit discovery succeeded")
	}
	if err := (*ExplicitDiscovery)(nil).Close(); err != nil {
		t.Fatalf("nil explicit discovery close: %v", err)
	}

	discovery, err := newExplicitDiscovery(&explicitFixtureSource{metadata: metadata}, metadata.Address())
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := metadata.Token().AuthorizationValue()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(discovery)
	if err != nil {
		t.Fatal(err)
	}
	values := []string{
		fmt.Sprint(discovery), fmt.Sprintf("%#v", discovery), fmt.Sprintf("%+v", discovery),
		string(encoded), discovery.LogValue().String(), slog.Any("discovery", discovery).Value.String(),
	}
	for _, value := range values {
		if strings.Contains(value, metadata.Address()) || strings.Contains(value, authorization) {
			t.Fatalf("explicit discovery formatting leaked endpoint material: %q", value)
		}
	}
}

func TestExplicitDiscoveryClosePreventsFurtherMetadataRead(t *testing.T) {
	t.Parallel()
	metadata := currentMetadataFixture(t)
	source := &explicitFixtureSource{metadata: metadata}
	discovery, err := newExplicitDiscovery(source, metadata.Address())
	if err != nil {
		t.Fatal(err)
	}
	if err = discovery.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = discovery.Discover(t.Context()); err == nil || source.calls.Load() != 0 {
		t.Fatalf("closed explicit discovery calls=%d, err=%v", source.calls.Load(), err)
	}
}

type explicitFixtureSource struct {
	metadata endpoint.Metadata
	err      error
	after    func()
	calls    atomic.Int32
}

func (source *explicitFixtureSource) Discover(context.Context) (endpoint.Metadata, error) {
	source.calls.Add(1)
	if source.after != nil {
		source.after()
	}
	return source.metadata, source.err
}

func assertExplicitErrorRedacted(t *testing.T, err error, metadata endpoint.Metadata, requested string) {
	t.Helper()
	authorization, tokenErr := metadata.Token().AuthorizationValue()
	if tokenErr != nil {
		t.Fatal(tokenErr)
	}
	for _, secret := range []string{metadata.Address(), requested, authorization, strings.TrimPrefix(authorization, endpoint.BearerPrefix)} {
		if secret != "" && strings.Contains(err.Error(), secret) {
			t.Fatalf("explicit attach error leaked endpoint material %q: %v", secret, err)
		}
	}
}

var errExplicitCancel = errors.New("explicit discovery canceled")

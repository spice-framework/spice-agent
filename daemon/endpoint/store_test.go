package endpoint

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/daemon/internal/userstorage"
)

func TestStorePublicationDiscoveryAndWithdrawal(t *testing.T) {
	store := openStoreFixture(t, "lifecycle")
	metadata := currentMetadataFixture(t, 1)
	if _, err := store.Discover(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("initial discovery = %v", err)
	}
	publication, err := store.Publish(t.Context(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	discovered, err := store.Discover(t.Context())
	if err != nil || !sameMetadata(discovered, metadata) {
		t.Fatalf("published discovery = %#v, %v", discovered, err)
	}
	if _, err = store.Publish(t.Context(), currentMetadataFixture(t, 2)); !errors.Is(err, ErrActive) {
		t.Fatalf("second publication = %v", err)
	}
	if err = publication.Close(); err != nil {
		t.Fatal(err)
	}
	if err = publication.Close(); err != nil {
		t.Fatalf("second publication close = %v", err)
	}
	if _, err = store.Discover(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("withdrawn discovery = %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatalf("second store close = %v", err)
	}
}

func TestStoreReplacesOnlyCanonicalStaleMetadata(t *testing.T) {
	path := endpointTestPath(t, "replace")
	store := openStoreAtFixture(t, path)
	directory := bindDirectoryFixture(t, path)
	stale := currentMetadataFixture(t, 3)
	writeMetadataFixture(t, directory, stale)

	replacement := currentMetadataFixture(t, 4)
	publication, err := store.Publish(t.Context(), replacement)
	if err != nil {
		t.Fatal(err)
	}
	discovered, err := store.Discover(t.Context())
	if err != nil || !sameMetadata(discovered, replacement) {
		t.Fatalf("replacement discovery = %#v, %v", discovered, err)
	}
	if err = publication.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreDiscoveryDistinguishesStaleAndHardFailures(t *testing.T) {
	for name, setup := range map[string]func(*testing.T, *userstorage.Directory){
		"stale": func(t *testing.T, directory *userstorage.Directory) {
			t.Helper()
			writeMetadataFixture(t, directory, currentMetadataFixture(t, 5))
		},
		"malformed": func(t *testing.T, directory *userstorage.Directory) {
			t.Helper()
			if err := directory.WriteFileAtomic(metadataFileName, []byte("{\n")); err != nil {
				t.Fatal(err)
			}
		},
		"wrong platform": func(t *testing.T, directory *userstorage.Directory) {
			t.Helper()
			writeMetadataFixture(t, directory, foreignMetadataFixture(t, 6))
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := endpointTestPath(t, name)
			store := openStoreAtFixture(t, path)
			directory := bindDirectoryFixture(t, path)
			setup(t, directory)
			_, err := store.Discover(t.Context())
			if name == "stale" {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("stale discovery = %v", err)
				}
				if _, readErr := directory.ReadFile(metadataFileName, maximumMetadataSize); !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("stale metadata remains: %v", readErr)
				}
				return
			}
			if err == nil || errors.Is(err, ErrNotFound) {
				t.Fatalf("hard failure became absence: %v", err)
			}
		})
	}
}

func TestStoreDiscoveryCleansStaleMetadataWhileStartupLeaseIsHeld(t *testing.T) {
	path := endpointTestPath(t, "startup-cleanup")
	store := openStoreAtFixture(t, path)
	directory := bindDirectoryFixture(t, path)
	writeMetadataFixture(t, directory, currentMetadataFixture(t, 7))
	startup, err := store.AcquireStartup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Discover(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale discovery under startup lease = %v", err)
	}
	if _, err = directory.ReadFile(metadataFileName, maximumMetadataSize); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale metadata remains under startup lease: %v", err)
	}
	if err = startup.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreActiveInstanceAndExactWithdrawal(t *testing.T) {
	path := endpointTestPath(t, "active")
	store := openStoreAtFixture(t, path)
	directory := bindDirectoryFixture(t, path)
	active := currentMetadataFixture(t, 8)
	lock, err := directory.AcquireLock(daemonLockName)
	if err != nil {
		t.Fatal(err)
	}
	writeMetadataFixture(t, directory, active)
	discovered, err := store.Discover(t.Context())
	if err != nil || !sameMetadata(discovered, active) {
		t.Fatalf("active discovery = %#v, %v", discovered, err)
	}
	if _, err = store.Publish(t.Context(), currentMetadataFixture(t, 9)); !errors.Is(err, ErrActive) {
		t.Fatalf("publication over active metadata = %v", err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}

	publication, err := store.Publish(t.Context(), currentMetadataFixture(t, 10))
	if err != nil {
		t.Fatal(err)
	}
	replacement := currentMetadataFixture(t, 11)
	writeMetadataFixture(t, directory, replacement)
	if err = publication.Close(); err == nil {
		t.Fatal("publication accepted metadata changed outside its coordination lock")
	}
	encoded, err := directory.ReadFile(metadataFileName, maximumMetadataSize)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := encodeMetadata(replacement)
	if !bytes.Equal(encoded, want) {
		t.Fatal("publication withdrew metadata owned by another process")
	}
	if _, err = store.Discover(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replacement stale discovery = %v", err)
	}
}

func TestStoreStartupLeaseCancellationAndCloseDrain(t *testing.T) {
	path := endpointTestPath(t, "startup")
	first := openStoreAtFixture(t, path)
	second := openStoreAtFixture(t, path)
	lease, err := first.AcquireStartup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err = second.AcquireStartup(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended startup acquisition = %v", err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = first.AcquireStartup(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("acquire after close = %v", err)
	}
	if err = lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err = lease.Release(); err != nil {
		t.Fatalf("second lease release = %v", err)
	}
	acquired, err := second.AcquireStartup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = acquired.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreConcurrentCloseAndLeaseRelease(t *testing.T) {
	store := openStoreFixture(t, "concurrent-close")
	lease, err := store.AcquireStartup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 12 {
		wait.Go(func() {
			for range 10 {
				if closeErr := store.Close(); closeErr != nil {
					t.Errorf("concurrent store close: %v", closeErr)
				}
			}
		})
	}
	wait.Go(func() {
		if releaseErr := lease.Release(); releaseErr != nil {
			t.Errorf("concurrent lease release: %v", releaseErr)
		}
	})
	wait.Wait()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStorePublicationCloseWhileMetadataBusyLeavesSafeStaleRecord(t *testing.T) {
	path := endpointTestPath(t, "busy-withdrawal")
	store := openStoreAtFixture(t, path)
	directory := bindDirectoryFixture(t, path)
	publication, err := store.Publish(t.Context(), currentMetadataFixture(t, 12))
	if err != nil {
		t.Fatal(err)
	}
	metadataLock, err := directory.AcquireLock(metadataLockName)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err = publication.CloseContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled publication close = %v", err)
	}
	if probe, probeErr := directory.AcquireLock(daemonLockName); !errors.Is(probeErr, userstorage.ErrLockBusy) {
		if probe != nil {
			_ = probe.Close()
		}
		t.Fatalf("canceled close released daemon liveness: %v", probeErr)
	}
	if err = metadataLock.Close(); err != nil {
		t.Fatal(err)
	}
	if err = publication.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Discover(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("withdrawn discovery = %v", err)
	}
}

func TestPublicationConcurrentCloseWaiterCanCancel(t *testing.T) {
	path := endpointTestPath(t, "concurrent-withdrawal")
	store := openStoreAtFixture(t, path)
	directory := bindDirectoryFixture(t, path)
	publication, err := store.Publish(t.Context(), currentMetadataFixture(t, 49))
	if err != nil {
		t.Fatal(err)
	}
	metadataLock, err := directory.AcquireLock(metadataLockName)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- publication.Close() }()
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err = publication.CloseContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent close waiter = %v", err)
	}
	if err = metadataLock.Close(); err != nil {
		t.Fatal(err)
	}
	if err = <-closed; err != nil {
		t.Fatalf("primary publication close = %v", err)
	}
	if err = publication.Close(); err != nil {
		t.Fatalf("idempotent publication close = %v", err)
	}
}

func TestStorePublicationFailureCleanupAndMissingWithdrawal(t *testing.T) {
	path := endpointTestPath(t, "publication-failures")
	store := openStoreAtFixture(t, path)
	directory := bindDirectoryFixture(t, path)
	candidate := currentMetadataFixture(t, 16)
	instance, err := directory.AcquireLock(daemonLockName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Publish(t.Context(), candidate); !errors.Is(err, ErrActive) {
		t.Fatalf("duplicate instance publication = %v", err)
	}
	if err = instance.Close(); err != nil {
		t.Fatal(err)
	}

	metadataLock, err := directory.AcquireLock(metadataLockName)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err = store.Publish(ctx, candidate); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended publication = %v", err)
	}
	if err = metadataLock.Close(); err != nil {
		t.Fatal(err)
	}

	if err = directory.WriteFileAtomic(metadataFileName, []byte("not-json\n")); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Publish(t.Context(), candidate); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("publication over malformed metadata = %v", err)
	}
	if err = directory.RemoveFile(metadataFileName); err != nil {
		t.Fatal(err)
	}
	publication, err := store.Publish(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.RemoveFile(metadataFileName); err != nil {
		t.Fatal(err)
	}
	if err = publication.Close(); err != nil {
		t.Fatalf("withdraw missing publication = %v", err)
	}
}

func TestStoreConcurrentDiscoveryAndPublicationClose(t *testing.T) {
	store := openStoreFixture(t, "concurrent")
	metadata := currentMetadataFixture(t, 13)
	publication, err := store.Publish(t.Context(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 12 {
		wait.Go(func() {
			for range 20 {
				discovered, discoverErr := store.Discover(t.Context())
				if discoverErr != nil || !sameMetadata(discovered, metadata) {
					t.Errorf("concurrent discovery = %#v, %v", discovered, discoverErr)
					return
				}
			}
		})
	}
	wait.Wait()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = publication.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Discover(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("discovery after drained close = %v", err)
	}
}

func TestStoreSingleLivenessLockCannotMakeOldMetadataAppearActive(t *testing.T) {
	path := endpointTestPath(t, "ordered-liveness")
	store := openStoreAtFixture(t, path)
	directory := bindDirectoryFixture(t, path)
	oldMetadata := currentMetadataFixture(t, 18)
	newMetadata := currentMetadataFixture(t, 19)
	writeMetadataFixture(t, directory, oldMetadata)
	metadataLock, err := directory.AcquireLock(metadataLockName)
	if err != nil {
		t.Fatal(err)
	}
	type publishResult struct {
		publication *Publication
		err         error
	}
	published := make(chan publishResult, 1)
	go func() {
		publication, publishErr := store.Publish(t.Context(), newMetadata)
		published <- publishResult{publication, publishErr}
	}()
	// A publisher blocked on endpoint.lock must not acquire daemon.lock early.
	// Otherwise the old record could be mistaken for the new daemon.
	time.Sleep(20 * time.Millisecond)
	probe, err := directory.AcquireLock(daemonLockName)
	if err != nil {
		t.Fatalf("publisher acquired daemon liveness before metadata lock: %v", err)
	}
	if err = probe.Close(); err != nil {
		t.Fatal(err)
	}
	discovered := make(chan struct {
		metadata Metadata
		err      error
	}, 1)
	go func() {
		metadata, discoverErr := store.Discover(t.Context())
		discovered <- struct {
			metadata Metadata
			err      error
		}{metadata, discoverErr}
	}()
	if err = metadataLock.Close(); err != nil {
		t.Fatal(err)
	}
	publicationResult := <-published
	if publicationResult.err != nil {
		t.Fatal(publicationResult.err)
	}
	discoveryResult := <-discovered
	if discoveryResult.err == nil && sameMetadata(discoveryResult.metadata, oldMetadata) {
		t.Fatal("old endpoint metadata appeared active for the new daemon")
	}
	if discoveryResult.err != nil && !errors.Is(discoveryResult.err, ErrNotFound) {
		t.Fatalf("concurrent ordered discovery = %v", discoveryResult.err)
	}
	current, err := store.Discover(t.Context())
	if err != nil || !sameMetadata(current, newMetadata) {
		t.Fatalf("new endpoint discovery = %#v, %v", current, err)
	}
	if err = publicationResult.publication.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreLockNamespaceRemainsBoundedAcrossRestarts(t *testing.T) {
	path := endpointTestPath(t, "bounded-locks")
	store := openStoreAtFixture(t, path)
	for seed := byte(20); seed < 40; seed++ {
		publication, err := store.Publish(t.Context(), currentMetadataFixture(t, seed))
		if err != nil {
			t.Fatal(err)
		}
		if err = publication.Close(); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{daemonLockName: true, metadataLockName: true}
	if len(entries) != len(want) {
		t.Fatalf("lock namespace grew across restarts: %v", entryNames(entries))
	}
	for _, entry := range entries {
		if !want[entry.Name()] {
			t.Fatalf("unexpected persistent endpoint state %q", entry.Name())
		}
	}
}

func TestStoreConcurrentPublishDiscoverAndClose(t *testing.T) {
	store := openStoreFixture(t, "publish-discover-close")
	for seed := byte(40); seed < 48; seed++ {
		metadata := currentMetadataFixture(t, seed)
		publication, err := store.Publish(t.Context(), metadata)
		if err != nil {
			t.Fatal(err)
		}
		var wait sync.WaitGroup
		for range 8 {
			wait.Go(func() {
				discovered, discoverErr := store.Discover(t.Context())
				if discoverErr != nil && !errors.Is(discoverErr, ErrNotFound) {
					t.Errorf("concurrent discovery: %v", discoverErr)
				}
				if discoverErr == nil && !sameMetadata(discovered, metadata) {
					t.Errorf("concurrent discovery returned another lifetime: %#v", discovered)
				}
			})
		}
		wait.Go(func() {
			if closeErr := publication.Close(); closeErr != nil {
				t.Errorf("concurrent publication close: %v", closeErr)
			}
		})
		wait.Wait()
	}
}

func TestStoreRejectsInvalidConfigurationContextsAndPlatform(t *testing.T) {
	if store, err := OpenStore(StoreConfig{}); err == nil || store != nil {
		t.Fatal("empty store configuration succeeded")
	}
	store := openStoreFixture(t, "invalid")
	if err := callWithNilContext(func(ctx context.Context) error {
		_, discoverErr := store.Discover(ctx)
		return discoverErr
	}); err == nil {
		t.Fatal("nil discovery context succeeded")
	}
	if err := callWithNilContext(func(ctx context.Context) error {
		_, acquireErr := store.AcquireStartup(ctx)
		return acquireErr
	}); err == nil {
		t.Fatal("nil startup context succeeded")
	}
	if err := callWithNilContext(func(ctx context.Context) error {
		_, publishErr := store.Publish(ctx, currentMetadataFixture(t, 14))
		return publishErr
	}); err == nil {
		t.Fatal("nil publication context succeeded")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.AcquireStartup(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled startup = %v", err)
	}
	if _, err := store.Discover(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled discovery = %v", err)
	}
	if _, err := store.Publish(canceled, currentMetadataFixture(t, 17)); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled publication = %v", err)
	}
	if _, err := store.Publish(t.Context(), foreignMetadataFixture(t, 15)); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign platform publication = %v", err)
	}
	var nilStore *Store
	if err := nilStore.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := nilStore.Discover(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil store discovery = %v", err)
	}
	var nilLease *StartupLease
	if err := nilLease.Release(); err != nil {
		t.Fatal(err)
	}
	var zeroLease StartupLease
	if err := zeroLease.Release(); err != nil {
		t.Fatal(err)
	}
	var nilPublication *Publication
	if err := nilPublication.Close(); err != nil {
		t.Fatal(err)
	}
	var zeroPublication Publication
	if err := zeroPublication.Close(); err != nil {
		t.Fatal(err)
	}
}

func openStoreFixture(tb testing.TB, name string) *Store {
	tb.Helper()
	return openStoreAtFixture(tb, endpointTestPath(tb, name))
}

func openStoreAtFixture(tb testing.TB, path string) *Store {
	tb.Helper()
	store, err := OpenStore(StoreConfig{Directory: path, PollInterval: time.Millisecond})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			tb.Errorf("close endpoint store: %v", closeErr)
		}
	})
	return store
}

func bindDirectoryFixture(tb testing.TB, path string) *userstorage.Directory {
	tb.Helper()
	directory, err := userstorage.Bind(path)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if closeErr := directory.Close(); closeErr != nil {
			tb.Errorf("close endpoint test directory: %v", closeErr)
		}
	})
	return directory
}

func endpointTestPath(tb testing.TB, name string) string {
	tb.Helper()
	if runtime.GOOS != "windows" {
		root, err := filepath.EvalSymlinks(tb.TempDir())
		if err != nil {
			tb.Fatal(err)
		}
		return filepath.Join(root, name)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		tb.Fatal(err)
	}
	base := filepath.Join(cache, "spice-agent-endpoint-tests")
	if err = os.MkdirAll(base, 0o700); err != nil {
		tb.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "endpoint-")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if removeErr := os.RemoveAll(root); removeErr != nil {
			tb.Errorf("remove endpoint test root: %v", removeErr)
		}
	})
	return filepath.Join(root, name)
}

func currentMetadataFixture(tb testing.TB, seed byte) Metadata {
	tb.Helper()
	process, err := NewProcess(
		uint32(seed)+100, time.Unix(1_700_000_000+int64(seed), 0).UTC(), bytes.Repeat([]byte{seed}, ProcessInstanceIDBytes),
	)
	if err != nil {
		tb.Fatal(err)
	}
	metadata, err := NewMetadata(
		currentTransportFixture(), currentAddressFixture(), tokenFixture(tb), buildFixture(tb), protocolFixture(tb), process,
	)
	if err != nil {
		tb.Fatal(err)
	}
	return metadata
}

func foreignMetadataFixture(tb testing.TB, seed byte) Metadata {
	tb.Helper()
	process, err := NewProcess(
		uint32(seed)+200, time.Unix(1_700_001_000+int64(seed), 0).UTC(), bytes.Repeat([]byte{seed}, ProcessInstanceIDBytes),
	)
	if err != nil {
		tb.Fatal(err)
	}
	transport := TransportWindowsNamedPipe
	address := `\\.\pipe\spice-agent-test`
	if runtime.GOOS == "windows" {
		transport = TransportUnixSocket
		address = "/tmp/spice-agent-test.sock"
	}
	metadata, err := NewMetadata(transport, address, tokenFixture(tb), buildFixture(tb), protocolFixture(tb), process)
	if err != nil {
		tb.Fatal(err)
	}
	return metadata
}

func writeMetadataFixture(tb testing.TB, directory *userstorage.Directory, metadata Metadata) {
	tb.Helper()
	encoded, err := encodeMetadata(metadata)
	if err != nil {
		tb.Fatal(err)
	}
	if err = directory.WriteFileAtomic(metadataFileName, encoded); err != nil {
		tb.Fatal(err)
	}
}

// callWithNilContext isolates the deliberate nil API-boundary test from
// ordinary context use.
func callWithNilContext(operation func(context.Context) error) error {
	return operation(nil)
}

func currentTransportFixture() Transport {
	if runtime.GOOS == "windows" {
		return TransportWindowsNamedPipe
	}
	return TransportUnixSocket
}

func currentAddressFixture() string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\spice-agent-test`
	}
	return "/tmp/spice-agent-test.sock"
}

func sameMetadata(first, second Metadata) bool {
	firstEncoded, firstErr := encodeMetadata(first)
	secondEncoded, secondErr := encodeMetadata(second)
	return firstErr == nil && secondErr == nil && bytes.Equal(firstEncoded, secondEncoded)
}

func entryNames(entries []os.DirEntry) []string {
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Name())
	}
	return result
}

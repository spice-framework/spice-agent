package runauthority

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestOpenContainsRandomAndPersistenceFailures(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "random-failure")
	_, err := Open(Config{Directory: directory, Random: failingReader{}})
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("random failure = %v", err)
	}

	directory = filepath.Join(authorityTestRoot(t), "duplicate-material")
	_, err = Open(Config{Directory: directory, Random: bytes.NewReader(bytes.Repeat([]byte{7}, 64))})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("duplicate scope/key = %v", err)
	}
}

func TestRunNameIsDeterministicAndTraversalSafe(t *testing.T) {
	first := runName("../../secret")
	second := runName("../../secret")
	if first != second || strings.ContainsAny(first, `/\\`) || len(first) != len("run-")+64 {
		t.Fatalf("run filename = %q", first)
	}
}

func TestOpenContainsAtomicPersistenceFailure(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "write-failure")
	_, err := Open(Config{
		Directory: directory,
		Random:    bytes.NewReader(append(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)...)),
		writeFile: func(string, []byte) error { return errors.New("secret persistence detail") },
	})
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("persistence failure = %v", err)
	}
}

func TestStartContainsInjectedStatePersistenceUncertainty(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "state-write-failure")
	store, err := Open(Config{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	store.writeFile = func(string, []byte) error { return errors.New("secret state write detail") }
	if _, err = store.Start(t.Context(), "run"); !errors.Is(err, ErrUncertain) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("state persistence failure = %v", err)
	}
	store.writeFile = store.directory.writeFileAtomic
	if _, err = store.Start(t.Context(), "run"); !errors.Is(err, ErrUncertain) {
		t.Fatalf("retry after attempted transition = %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStartMarksAttemptedPersistenceFailureUncertain(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "state-write-uncertain")
	store, err := Open(Config{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	originalWriter := store.directory.writeFileAtomic
	store.writeFile = func(name string, value []byte) error {
		if writeErr := originalWriter(name, value); writeErr != nil {
			return writeErr
		}
		return errors.New("failure after durable write")
	}
	if _, err = store.Start(t.Context(), "run"); !errors.Is(err, ErrUncertain) {
		t.Fatalf("attempted start = %v", err)
	}
	if !store.isUncertain("run") {
		t.Fatal("attempted start did not poison the process-local run identity")
	}
	store.writeFile = originalWriter
	if _, err = store.Start(t.Context(), "run"); !errors.Is(err, ErrUncertain) {
		t.Fatalf("retry after uncertain start = %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentOpenInitializesOneSecureIdentity(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "authority")
	const workers = 12
	start := make(chan struct{})
	results := make(chan error, workers)
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			<-start
			store, err := Open(Config{Directory: directory})
			if err == nil {
				err = store.Close()
			}
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent open: %v", err)
		}
	}
}

func TestCloseRejectsNewWorkAndDrainsOutstandingLease(t *testing.T) {
	store, err := Open(Config{Directory: filepath.Join(authorityTestRoot(t), "authority")})
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.Start(t.Context(), "open-run")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); !errors.Is(err, ErrBusy) {
		t.Fatalf("close with active lease = %v", err)
	}
	if _, err = store.Start(context.Background(), "new-run"); !errors.Is(err, ErrClosed) {
		t.Fatalf("start during close = %v", err)
	}
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatalf("idempotent close after drain = %v", err)
	}
}

func TestConcurrentCloseIsIdempotent(t *testing.T) {
	store, err := Open(Config{Directory: filepath.Join(authorityTestRoot(t), "authority")})
	if err != nil {
		t.Fatal(err)
	}
	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			<-start
			results <- store.Close()
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent close: %v", err)
		}
	}
	if _, err = store.Start(context.Background(), "closed-run"); !errors.Is(err, ErrClosed) {
		t.Fatalf("start after concurrent close = %v", err)
	}
}

func TestCloseClearsCanonicalKeyMaterial(t *testing.T) {
	store, err := Open(Config{Directory: filepath.Join(authorityTestRoot(t), "authority")})
	if err != nil {
		t.Fatal(err)
	}
	if allZero(store.key[:]) {
		t.Fatal("new store has zero key material")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if !allZero(store.key[:]) {
		t.Fatal("closed store retained canonical key material")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("secret source failed") }

var _ io.Reader = failingReader{}

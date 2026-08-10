package process

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializationCleansDirectoryAfterDestinationFailure(t *testing.T) {
	t.Parallel()
	lease := internalMaterializationLease(t)
	defer func() { _ = lease.Close() }()
	root := t.TempDir()
	privateFailure := errors.New("private destination failure")
	materialized, err := lease.materializeForLaunch(
		t.Context(),
		root,
		func(string, int, os.FileMode) (*os.File, error) { return nil, privateFailure },
	)
	assertFailedMaterializationCleaned(t, root, materialized, err, privateFailure)
}

func TestMaterializationCleansDirectoryAfterCopyCancellation(t *testing.T) {
	t.Parallel()
	lease := internalMaterializationLease(t)
	defer func() { _ = lease.Close() }()
	root := t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	privateCause := errors.New("private copy cancellation")
	materialized, err := lease.materializeForLaunch(
		ctx,
		root,
		func(path string, flag int, mode os.FileMode) (*os.File, error) {
			file, openErr := os.OpenFile(path, flag, mode)
			cancel(privateCause)
			return file, openErr
		},
	)
	assertFailedMaterializationCleaned(t, root, materialized, err, privateCause)
}

func TestMaterializationCleanupRefusesUnexpectedContentsAndCanRetry(t *testing.T) {
	t.Parallel()
	lease := internalMaterializationLease(t)
	defer func() { _ = lease.Close() }()
	root := t.TempDir()
	materialized, err := lease.materializeForLaunch(t.Context(), root, os.OpenFile)
	if err != nil {
		t.Fatal(err)
	}
	path := materialized.Path()
	directory := filepath.Dir(path)
	unexpected := filepath.Join(directory, "unexpected")
	if err = os.WriteFile(unexpected, []byte("must survive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = materialized.Close(); err == nil || materialized.Path() != path {
		t.Fatalf("nonempty cleanup = path %q, error %v", materialized.Path(), err)
	}
	content, readErr := os.ReadFile(unexpected)
	if readErr != nil || string(content) != "must survive" {
		t.Fatalf("unexpected content was removed: %q, %v", content, readErr)
	}
	if err = os.Remove(unexpected); err != nil {
		t.Fatal(err)
	}
	if err = materialized.Close(); err != nil {
		t.Fatalf("cleanup retry: %v", err)
	}
	if _, err = os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private directory remains: %v", err)
	}
}

func internalMaterializationLease(t *testing.T) *ExecutableLease {
	t.Helper()
	content := []byte("internal materialization source")
	path := filepath.Join(t.TempDir(), materializedExecutableName())
	if err := os.WriteFile(path, content, 0o700); err != nil {
		t.Fatal(err)
	}
	digest := newSHA256(sha256.Sum256(content))
	lease, err := VerifyExecutable(t.Context(), path, digest)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func assertFailedMaterializationCleaned(
	t *testing.T,
	root string,
	materialized *MaterializedExecutable,
	err error,
	cause error,
) {
	t.Helper()
	failure, ok := errors.AsType[*VerificationError](err)
	if materialized != nil || !ok || failure.Operation() != VerificationOperationMaterialize ||
		!errors.Is(err, cause) || err.Error() == cause.Error() {
		t.Fatalf("failed materialization = %p, %T %v", materialized, err, err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("partial materialization remains: entries=%v, error=%v", entries, readErr)
	}
}

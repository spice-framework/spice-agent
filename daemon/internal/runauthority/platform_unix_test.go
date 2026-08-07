//go:build linux || darwin

package runauthority

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
)

func TestUnixRejectsIntermediateSymlink(t *testing.T) {
	realDirectory := filepath.Join(t.TempDir(), "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDirectory, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Config{Directory: filepath.Join(link, "authority")}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("symlink path = %v", err)
	}
}

func TestUnixRejectsHardLinkedAuthorityFile(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "authority")
	store, err := Open(Config{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(directory, "identity.key"), filepath.Join(directory, "identity-copy.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Config{Directory: directory}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("hard-linked identity = %v", err)
	}
}

func TestUnixBoundDirectorySurvivesPathSubstitution(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "authority")
	store := openTestStore(t, directory)
	active, err := store.Start(t.Context(), "bound-run")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := internalSnapshot(t, active, "bound-run", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "authority-original")
	if err = os.Rename(directory, moved); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	transaction, err := store.PrepareImport(t.Context(), snapshot)
	if err != nil {
		t.Fatalf("prepare through retained descriptor: %v", err)
	}
	if err = transaction.Consume(t.Context()); err != nil {
		t.Fatalf("consume through retained descriptor: %v", err)
	}
	if err = transaction.Abort(); !errors.Is(err, ErrUncertain) {
		t.Fatalf("abort consumed import = %v", err)
	}
	value, err := store.readRecord("bound-run")
	if err != nil || value.Phase != PhaseImporting {
		t.Fatalf("bound record = %#v, %v", value, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("substituted directory was modified: %v", entries)
	}
}

func TestUnixReopenRejectsRollbackThroughWritableAncestor(t *testing.T) {
	root := authorityTestRoot(t)
	ancestor := filepath.Join(root, "ancestor")
	if err := os.Mkdir(ancestor, 0o700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(ancestor, "authority")
	store, err := Open(Config{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(ancestor, 0o777); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(directory, filepath.Join(ancestor, "authority-rollback")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(Config{Directory: directory}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("reopen through writable ancestor = %v", err)
	}
}

func TestUnixStickyTrustedAncestorIsAccepted(t *testing.T) {
	root := authorityTestRoot(t)
	ancestor := filepath.Join(root, "sticky")
	if err := os.Mkdir(ancestor, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ancestor, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}
	store, err := Open(Config{Directory: filepath.Join(ancestor, "authority")})
	if err != nil {
		t.Fatalf("trusted sticky ancestor = %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUnixExistingLeafModeIsRejectedWithoutMutation(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "existing-authority")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Config{Directory: directory}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("open existing 0755 authority = %v", err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o755 {
		t.Fatalf("existing directory mode mutated to %o", permissions)
	}
}

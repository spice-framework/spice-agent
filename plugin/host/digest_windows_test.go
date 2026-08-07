//go:build windows

package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifiedExecutableRejectsWindowsSymlink(t *testing.T) {
	t.Parallel()
	target, digest := writeTestExecutable(t, []byte("target"))
	link := filepath.Join(t.TempDir(), "plugin-link.exe")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("creating a Windows symlink requires optional privilege: %v", err)
	}
	_, err := openVerifiedExecutable(context.Background(), executableForPath(t, link, digest))
	if err == nil {
		t.Fatal("expected reparse-point rejection")
	}
}

func TestVerifiedExecutableBlocksWindowsMutationWhileLeased(t *testing.T) {
	t.Parallel()
	path, digest := writeTestExecutable(t, []byte("original"))
	lease, err := openVerifiedExecutable(context.Background(), executableForPath(t, path, digest))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	if err := os.WriteFile(path, []byte("mutated"), 0o700); err == nil {
		t.Fatal("expected held executable handle to deny mutation")
	}
	if err := lease.Recheck(context.Background()); err != nil {
		t.Fatal(err)
	}
}

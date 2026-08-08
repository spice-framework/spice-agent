//go:build unix

package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifiedExecutableRejectsSymlink(t *testing.T) {
	t.Parallel()
	target, digest := writeTestExecutable(t, []byte("target"))
	link := filepath.Join(t.TempDir(), "plugin-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := openVerifiedExecutable(context.Background(), executableForPath(t, link, digest))
	if err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestVerifiedExecutableDetectsInPlaceMutation(t *testing.T) {
	t.Parallel()
	path, digest := writeTestExecutable(t, []byte("original"))
	lease, err := openVerifiedExecutable(context.Background(), executableForPath(t, path, digest))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	if writeErr := os.WriteFile(path, []byte("mutated"), 0o700); writeErr != nil {
		t.Fatal(writeErr)
	}
	err = lease.Recheck(context.Background())
	if err == nil {
		t.Fatal("expected mutation rejection")
	}
	if strings.Contains(err.Error(), path) {
		t.Fatal("error exposed executable path")
	}
}

func TestVerifiedExecutableDetectsPathReplacement(t *testing.T) {
	t.Parallel()
	path, digest := writeTestExecutable(t, []byte("same content"))
	lease, err := openVerifiedExecutable(context.Background(), executableForPath(t, path, digest))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	replacement := filepath.Join(filepath.Dir(path), "replacement")
	if err := os.WriteFile(replacement, []byte("same content"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if err := lease.Recheck(context.Background()); err == nil {
		t.Fatal("expected identity rejection")
	}
}

func TestVerifiedExecutableRequiresExecutePermission(t *testing.T) {
	t.Parallel()
	path, digest := writeTestExecutable(t, []byte("content"))
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openVerifiedExecutable(context.Background(), executableForPath(t, path, digest)); err == nil {
		t.Fatal("expected execute-permission rejection")
	}
}

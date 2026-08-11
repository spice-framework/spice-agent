package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReleasedWorkspaceReclaimsReadOnlyModuleCache(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	path := filepath.Join(parent, "workspace")
	nested := filepath.Join(path, "gomodcache", "module")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "source.go"), []byte("package source\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nested, 0o500); err != nil {
		t.Fatal(err)
	}
	workspace, err := newReleasedWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Path() != path {
		t.Fatal("released workspace path changed")
	}
	if err = workspace.Close(); err != nil {
		t.Fatal(err)
	}
	if err = workspace.Close(); err != nil || workspace.Path() != "" {
		t.Fatal("released workspace close is not idempotent")
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released workspace still exists: %v", err)
	}
}

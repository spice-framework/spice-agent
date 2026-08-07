//go:build linux || darwin

package userstorage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnixRejectsSymlinkHardLinkAndLooseLeaf(t *testing.T) {
	root := storageTestRoot(t)
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDirectory, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(filepath.Join(link, "storage")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("symlink ancestry = %v", err)
	}

	loose := filepath.Join(root, "loose")
	if err := os.Mkdir(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(loose); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("existing loose leaf = %v", err)
	}

	directory, err := Bind(filepath.Join(root, "hardlink"))
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.WriteFileAtomic("value", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(filepath.Join(root, "hardlink", "value"), filepath.Join(root, "hardlink", "copy")); err != nil {
		t.Fatal(err)
	}
	if _, err = directory.ReadFile("value", 6); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("hard-linked file = %v", err)
	}
	if err = directory.RemoveFile("value"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("remove hard-linked file = %v", err)
	}
	if err = directory.WriteFileAtomic("loose", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(root, "hardlink", "loose"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = directory.RemoveFile("loose"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("remove loose-mode file = %v", err)
	}
	if err = os.Symlink("loose", filepath.Join(root, "hardlink", "symbolic")); err != nil {
		t.Fatal(err)
	}
	if err = directory.RemoveFile("symbolic"); err == nil {
		t.Fatal("removed symbolic link")
	}
	if err = directory.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUnixRetainedDirectorySurvivesPathSubstitution(t *testing.T) {
	root := storageTestRoot(t)
	path := filepath.Join(root, "storage")
	directory, err := Bind(path)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "original")
	if err = os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = directory.WriteFileAtomic("value", []byte("bound")); err != nil {
		t.Fatal(err)
	}
	replacement, err := Bind(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = replacement.WriteFileAtomic("value", []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if err = replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if err = directory.RemoveFile("value"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(moved, "value")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retained identity remove: %v", err)
	}
	value, err := os.ReadFile(filepath.Join(path, "value"))
	if err != nil || string(value) != "replacement" {
		t.Fatalf("substituted directory value = %q, %v", value, err)
	}
	if err = directory.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUnixAbsoluteHelpersRejectUnsafeDirectoryTrust(t *testing.T) {
	root := storageTestRoot(t)
	for name, prepare := range map[string]func(*testing.T) string{
		"loose leaf": func(t *testing.T) string {
			t.Helper()
			directory := filepath.Join(root, "loose-leaf")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			return directory
		},
		"writable ancestor": func(t *testing.T) string {
			t.Helper()
			ancestor := filepath.Join(root, "writable-ancestor")
			if err := os.Mkdir(ancestor, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(ancestor, 0o777); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(ancestor, "private-leaf")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			return directory
		},
	} {
		t.Run(name, func(t *testing.T) {
			directory := prepare(t)
			valuePath := filepath.Join(directory, "value")
			if err := os.WriteFile(valuePath, []byte("value"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadFile(valuePath, 16); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("absolute read accepted unsafe directory: %v", err)
			}
			if err := WriteFileAtomic(valuePath, []byte("replacement")); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("absolute write accepted unsafe directory: %v", err)
			}
			lock, err := AcquireStableLock(filepath.Join(directory, "value.lock"))
			if !errors.Is(err, ErrUnavailable) || lock != nil {
				t.Fatalf("absolute lock accepted unsafe directory: %#v, %v", lock, err)
			}
		})
	}
}

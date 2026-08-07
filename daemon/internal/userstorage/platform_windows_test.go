//go:build windows

package userstorage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsHandleValidationIsBoundToExpectedPath(t *testing.T) {
	directoryPath := filepath.Join(storageTestRoot(t), "storage")
	directory, err := Bind(directoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.WriteFileAtomic("value", []byte("value")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directoryPath, "value")
	file, err := openWindowsFile(path, windows.GENERIC_READ, windows.OPEN_EXISTING)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err = validateWindowsHandle(windows.Handle(file.Fd()), filepath.Join(directoryPath, "other")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mismatched opened path = %v", err)
	}
	if err = directory.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsRetainedDirectoryRemoveSurvivesLeafSubstitution(t *testing.T) {
	root := storageTestRoot(t)
	path := filepath.Join(root, "storage")
	directory, err := Bind(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.WriteFileAtomic("value", []byte("original")); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "original")
	if err = os.Rename(path, moved); err != nil {
		t.Skipf("this Windows filesystem prevents directory rename with an open handle: %v", err)
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
		t.Fatalf("retained identity remove = %v", err)
	}
	value, err := os.ReadFile(filepath.Join(path, "value"))
	if err != nil || string(value) != "replacement" {
		t.Fatalf("replacement value = %q, %v", value, err)
	}
	if err = directory.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsRemoveRejectsHardLink(t *testing.T) {
	path := filepath.Join(storageTestRoot(t), "hardlink")
	directory, err := Bind(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.WriteFileAtomic("value", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(filepath.Join(path, "value"), filepath.Join(path, "copy")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if err = directory.RemoveFile("value"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("remove hard-linked file = %v", err)
	}
	if err = directory.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsAncestryValidationIsBoundToRetainedDirectory(t *testing.T) {
	root := storageTestRoot(t)
	first, err := Bind(filepath.Join(root, "first"))
	if err != nil {
		t.Fatal(err)
	}
	secondPath := filepath.Join(root, "second")
	second, err := Bind(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateWindowsAncestry(secondPath, first.value.handle); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mismatched ancestry identity = %v", err)
	}
	if err = second.Close(); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsAncestryIncludesVolumeRoot(t *testing.T) {
	path := filepath.Join(storageTestRoot(t), "storage")
	paths, err := windowsAncestryPaths(path)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.VolumeName(path) + string(filepath.Separator)
	if len(paths) < 2 || !filepath.IsAbs(paths[0]) || !strings.EqualFold(paths[0], root) || paths[len(paths)-1] != path {
		t.Fatalf("ancestry paths = %v", paths)
	}
}

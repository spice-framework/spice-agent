package userstorage

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDirectoryRelativeFilesLocksAndClose(t *testing.T) {
	directoryPath := filepath.Join(storageTestRoot(t), "nested", "state")
	directory, err := Bind(directoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.WriteFileAtomic("metadata", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err = directory.WriteFileAtomic("metadata", []byte("two")); err != nil {
		t.Fatal(err)
	}
	value, err := directory.ReadFile("metadata", 3)
	if err != nil || string(value) != "two" {
		t.Fatalf("read relative file = %q, %v", value, err)
	}
	if _, err = directory.ReadFile("metadata", 2); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("oversized relative file = %v", err)
	}
	lock, err := directory.AcquireLock("state.lock")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = directory.AcquireLock("state.lock"); !errors.Is(err, ErrLockBusy) {
		t.Fatalf("contended lock = %v", err)
	}
	var wait sync.WaitGroup
	for range 4 {
		wait.Go(func() {
			if closeErr := lock.Close(); closeErr != nil {
				t.Errorf("concurrent lock close: %v", closeErr)
			}
		})
	}
	wait.Wait()
	if err = directory.RemoveFile("metadata"); err != nil {
		t.Fatal(err)
	}
	if err = directory.RemoveFile("metadata"); err != nil {
		t.Fatalf("second remove = %v", err)
	}
	if _, err = directory.ReadFile("metadata", 3); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed file read = %v", err)
	}
	if err = directory.Close(); err != nil {
		t.Fatal(err)
	}
	if err = directory.Close(); err != nil {
		t.Fatalf("second directory close = %v", err)
	}
	if _, err = directory.ReadFile("metadata", 3); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("read after close = %v", err)
	}
}

func TestAbsoluteConvenienceOperationsAndInvalidNames(t *testing.T) {
	directoryPath := filepath.Join(storageTestRoot(t), "absolute")
	directory, err := Bind(directoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directoryPath, "value")
	if err = WriteFileAtomic(path, []byte("value")); err != nil {
		t.Fatal(err)
	}
	value, err := ReadFile(path, 5)
	if err != nil || string(value) != "value" {
		t.Fatalf("absolute read = %q, %v", value, err)
	}
	lock, err := AcquireStableLock(filepath.Join(directoryPath, "value.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}

	directory, err = Bind(directoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = directory.Close() }()
	for _, name := range []string{"", ".", "..", "../escape", "nested/value", "bad\x00name"} {
		if err = directory.WriteFileAtomic(name, nil); !errors.Is(err, ErrUnavailable) {
			t.Errorf("write invalid name %q = %v", name, err)
		}
		if _, err = directory.ReadFile(name, 1); !errors.Is(err, ErrUnavailable) {
			t.Errorf("read invalid name %q = %v", name, err)
		}
		if _, err = directory.AcquireLock(name); !errors.Is(err, ErrUnavailable) {
			t.Errorf("lock invalid name %q = %v", name, err)
		}
		if err = directory.RemoveFile(name); !errors.Is(err, ErrUnavailable) {
			t.Errorf("remove invalid name %q = %v", name, err)
		}
	}
	if _, err = Bind(filepath.Join("relative", "storage")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("relative bind = %v", err)
	}
	if _, err = ReadFile(filepath.Join("relative", "value"), 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("relative read = %v", err)
	}
	if _, err = os.Stat(filepath.Join(directoryPath, "nested")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid name escaped retained directory: %v", err)
	}
}

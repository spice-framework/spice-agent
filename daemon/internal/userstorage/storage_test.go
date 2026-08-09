package userstorage

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

func TestInitializationLockSerializesDistinctDirectoryBindings(t *testing.T) {
	directoryPath := filepath.Join(storageTestRoot(t), "initialization-lock")
	firstDirectory, err := Bind(directoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = firstDirectory.Close() }()
	secondDirectory, err := Bind(directoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = secondDirectory.Close() }()

	first, err := firstDirectory.AcquireInitializationLock("identity.lock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })

	type acquireResult struct {
		lock *Lock
		err  error
	}
	attempted := make(chan struct{})
	result := make(chan acquireResult, 1)
	go func() {
		close(attempted)
		second, acquireErr := secondDirectory.AcquireInitializationLock("identity.lock")
		result <- acquireResult{lock: second, err: acquireErr}
	}()
	<-attempted

	select {
	case early := <-result:
		if early.lock != nil {
			_ = early.lock.Close()
		}
		t.Fatalf("second binding completed before first released initialization lock: %v", early.err)
	case <-time.After(100 * time.Millisecond):
	}

	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case completed := <-result:
		if completed.err != nil {
			t.Fatalf("second binding failed after release: %v", completed.err)
		}
		if completed.lock == nil {
			t.Fatal("second binding returned a nil initialization lock")
		}
		if err = completed.lock.Close(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second binding did not acquire initialization lock after release")
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

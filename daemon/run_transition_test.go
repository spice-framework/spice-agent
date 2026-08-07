package daemon

import (
	"context"
	"errors"
	"testing"
)

func TestRunTransitionDefensiveBoundaries(t *testing.T) {
	t.Parallel()
	var missing *runTransition
	if err := missing.LockContext(t.Context()); err == nil {
		t.Fatal("nil transition acquired a context lock")
	}
	if missing.TryLock() {
		t.Fatal("nil transition acquired a try lock")
	}
	for _, test := range []struct {
		name string
		call func()
	}{
		{name: "lock nil", call: missing.Lock},
		{name: "unlock nil", call: missing.Unlock},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertRunTransitionPanic(t, test.call)
		})
	}

	transition := newRunTransition()
	//nolint:staticcheck // Boundary coverage intentionally passes nil to verify defensive context handling.
	if err := transition.LockContext(nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("nil lock context = %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := transition.LockContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lock context = %v", err)
	}
	if !transition.TryLock() {
		t.Fatal("available transition was not acquired")
	}
	if transition.TryLock() {
		t.Fatal("held transition was acquired twice")
	}
	transition.Unlock()
	assertRunTransitionPanic(t, transition.Unlock)
}

func TestRunTransitionLockAndContextLock(t *testing.T) {
	t.Parallel()
	transition := newRunTransition()
	transition.Lock()
	if transition.TryLock() {
		t.Fatal("held transition was acquired twice")
	}
	transition.Unlock()
	if err := transition.LockContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	transition.Unlock()
}

func assertRunTransitionPanic(t *testing.T, call func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("operation did not panic")
		}
	}()
	call()
}

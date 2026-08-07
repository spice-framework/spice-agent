//go:build !windows && !linux && !darwin

package runauthority

func forceStableLockCleanupFailure(*stableLock) error { return ErrUnavailable }

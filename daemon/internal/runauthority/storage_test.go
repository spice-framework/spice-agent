package runauthority

import "errors"

func forceStableLockCleanupFailure(lock *stableLock) error {
	if lock == nil || lock.closeFn == nil {
		return ErrUnavailable
	}
	closeFn := lock.closeFn
	lock.closeFn = func() error { return errors.Join(closeFn(), ErrUnavailable) }
	return nil
}

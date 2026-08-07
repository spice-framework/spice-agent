//go:build !windows && !linux && !darwin

package userstorage

import (
	"errors"
	"testing"
)

func TestUnsupportedPlatformFailsClosed(t *testing.T) {
	if _, err := Bind("/unsupported"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsupported bind = %v", err)
	}
}

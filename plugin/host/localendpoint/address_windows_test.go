//go:build windows

package localendpoint

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDeriveWindowsAddressHonorsNameBoundary(t *testing.T) {
	identity := identityFor(t, "windows-boundary")
	reserved := len("spice-agent-") + len(windowsPluginNameSegment) + len(identity)
	baseSuffix := strings.Repeat("a", windowsMaximumNameBytes-reserved)
	base := windowsPipePrefix + "spice-agent-" + baseSuffix
	address, err := deriveWindowsAddress(base, identity)
	if err != nil {
		t.Fatalf("derive maximum address: %v", err)
	}
	if got := len(strings.TrimPrefix(address, windowsPipePrefix)); got != windowsMaximumNameBytes {
		t.Fatalf("pipe name length = %d, want %d", got, windowsMaximumNameBytes)
	}
	if _, err = deriveWindowsAddress(base+"a", identity); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("derive overlong address error = %v, want ErrUnavailable", err)
	}
	if _, err = deriveWindowsAddress(`\\server\pipe\spice-agent-user`, identity); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("derive remote address error = %v, want ErrUnavailable", err)
	}
}

func TestWindowsCloseIsLifecycleSafeWithoutPersistentArtifact(t *testing.T) {
	owned, err := NewFactory().Open(context.Background(), identityFor(t, "windows-close"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err = owned.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err = owned.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
}

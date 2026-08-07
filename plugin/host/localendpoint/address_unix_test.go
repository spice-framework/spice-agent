//go:build linux || darwin

package localendpoint

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/daemon/localipc"
)

func TestDeriveUnixAddressHonorsPathBoundary(t *testing.T) {
	name := "p-abcdefghijklmnopqrstuvwxyz"
	directory := "/" + strings.Repeat("a", maximumUnixSocketPathBytes-len(name)-2)
	address, err := deriveUnixAddress(directory, name)
	if err != nil {
		t.Fatalf("derive maximum address: %v", err)
	}
	if len(address) != maximumUnixSocketPathBytes {
		t.Fatalf("address length = %d, want %d", len(address), maximumUnixSocketPathBytes)
	}
	if _, err = deriveUnixAddress(directory+"a", name); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("derive overlong address error = %v, want ErrUnavailable", err)
	}
	if _, err = deriveUnixAddress("relative", name); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("derive relative address error = %v, want ErrUnavailable", err)
	}
	if _, err = deriveUnixAddress(directory, "../escape"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("derive unsafe name error = %v, want ErrUnavailable", err)
	}
	if _, err = deriveUnixAddress(directory, ".."); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("derive parent name error = %v, want ErrUnavailable", err)
	}
}

func TestUnixCloseRemovesOwnedStaleSocket(t *testing.T) {
	owned, err := NewFactory().Open(context.Background(), identityFor(t, "stale"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	raw, err := net.ListenUnix("unix", &net.UnixAddr{Name: owned.Address(), Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	raw.SetUnlinkOnClose(false)
	if err = os.Chmod(owned.Address(), 0o600); err != nil {
		_ = raw.Close()
		t.Fatalf("Chmod: %v", err)
	}
	if err = raw.Close(); err != nil {
		t.Fatalf("close raw listener: %v", err)
	}
	if err = owned.Close(); err != nil {
		t.Fatalf("endpoint Close: %v", err)
	}
	if _, err = os.Lstat(owned.Address()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket remains after Close: %v", err)
	}
}

func TestUnixClosePreservesUnprovenFileAndRedactsFailure(t *testing.T) {
	identity := identityFor(t, "ordinary-file")
	owned, err := NewFactory().Open(context.Background(), identity)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	content := []byte("preserve")
	if err = os.WriteFile(owned.Address(), content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	defer func() { _ = os.Remove(owned.Address()) }()
	err = owned.Close()
	if !errors.Is(err, ErrCleanup) || !errors.Is(err, localipc.ErrUnsafeEndpoint) {
		t.Fatalf("Close error = %v, want ErrCleanup and ErrUnsafeEndpoint", err)
	}
	assertRedacted(t, err.Error(), owned.Address(), identity)
	got, readErr := os.ReadFile(owned.Address())
	if readErr != nil {
		t.Fatalf("ReadFile after Close: %v", readErr)
	}
	if string(got) != string(content) {
		t.Fatalf("file content = %q, want %q", got, content)
	}
	if repeated := owned.Close(); repeated == nil || repeated.Error() != err.Error() {
		t.Fatalf("repeated Close error = %v, want stable %v", repeated, err)
	}
}

func TestUnixCloseRefusesLiveSocket(t *testing.T) {
	owned, err := NewFactory().Open(context.Background(), identityFor(t, "live"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	listener, err := localipc.Listen(owned.Address())
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = listener.Close() }()
	err = owned.Close()
	if !errors.Is(err, ErrCleanup) || !errors.Is(err, localipc.ErrEndpointInUse) {
		t.Fatalf("Close error = %v, want ErrCleanup and ErrEndpointInUse", err)
	}
	if _, statErr := os.Lstat(filepath.Clean(owned.Address())); statErr != nil {
		t.Fatalf("live socket was removed: %v", statErr)
	}
}

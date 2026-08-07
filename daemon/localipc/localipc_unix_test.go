//go:build linux || darwin

package localipc

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnixListenerIsPrivateConnectableAndRemovedOnClose(t *testing.T) {
	t.Parallel()
	address := filepath.Join(unixPrivateDirectory(t), "agent.sock")
	listener, err := Listen(address)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			accepted <- acceptErr
			return
		}
		defer func() { _ = connection.Close() }()
		buffer := make([]byte, 4)
		_, acceptErr = io.ReadFull(connection, buffer)
		if acceptErr == nil {
			_, acceptErr = connection.Write(buffer)
		}
		accepted <- acceptErr
	}()
	connection, err := Dial(t.Context(), address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err = io.ReadFull(connection, response); err != nil || string(response) != "ping" {
		t.Fatalf("echo response = %q, %v", response, err)
	}
	_ = connection.Close()
	if err = <-accepted; err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(address)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("listener socket = %#v, %v", info, err)
	}
	directory, err := openPrivateUnixDirectory(filepath.Dir(address))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = directory.validatePrivateSocket(filepath.Base(address)); err != nil {
		t.Fatal(err)
	}
	_ = directory.close()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(address); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("closed listener left socket: %v", err)
	}
}

func TestUnixListenRemovesOnlyOwnedPrivateStaleSocket(t *testing.T) {
	t.Parallel()
	address := filepath.Join(unixPrivateDirectory(t), "stale.sock")
	raw, err := net.ListenUnix("unix", &net.UnixAddr{Name: address, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	raw.SetUnlinkOnClose(false)
	if err = os.Chmod(address, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(address)
	if err != nil {
		t.Fatalf("replace owned stale socket: %v", err)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUnixListenPreservesLiveAndNonSocketEndpoints(t *testing.T) {
	t.Parallel()
	directory := unixPrivateDirectory(t)
	liveAddress := filepath.Join(directory, "live.sock")
	live, err := net.ListenUnix("unix", &net.UnixAddr{Name: liveAddress, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })
	if err = os.Chmod(liveAddress, 0o600); err != nil {
		t.Fatal(err)
	}
	if replacement, listenErr := Listen(liveAddress); !errors.Is(listenErr, ErrEndpointInUse) || replacement != nil {
		t.Fatalf("replace live socket = %#v, %v", replacement, listenErr)
	}
	if info, statErr := os.Lstat(liveAddress); statErr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("live socket was removed: %#v, %v", info, statErr)
	}

	fileAddress := filepath.Join(directory, "ordinary.sock")
	if err = os.WriteFile(fileAddress, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if replacement, listenErr := Listen(fileAddress); !errors.Is(listenErr, ErrUnsafeEndpoint) || replacement != nil {
		t.Fatalf("replace ordinary file = %#v, %v", replacement, listenErr)
	}
	content, err := os.ReadFile(fileAddress)
	if err != nil || string(content) != "preserve" {
		t.Fatalf("ordinary file was changed: %q, %v", content, err)
	}
}

func TestUnixEndpointValidationAndDeadline(t *testing.T) {
	t.Parallel()
	directory := unixPrivateDirectory(t)
	unsafeDirectory := filepath.Join(directory, "shared")
	if err := os.Mkdir(unsafeDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if listener, err := Listen(filepath.Join(unsafeDirectory, "agent.sock")); !errors.Is(err, ErrUnsafeEndpoint) || listener != nil {
		t.Fatalf("unsafe directory listener = %#v, %v", listener, err)
	}
	for _, address := range []string{"", "relative.sock", filepath.Join(directory, "bad name.sock")} {
		if listener, err := Listen(address); !errors.Is(err, ErrUnsafeEndpoint) || listener != nil {
			t.Fatalf("unsafe address %q = %#v, %v", address, listener, err)
		}
	}
	deadline, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	<-deadline.Done()
	if connection, err := Dial(deadline, filepath.Join(directory, "missing.sock")); !errors.Is(err, context.DeadlineExceeded) || connection != nil {
		t.Fatalf("expired dial = %#v, %v", connection, err)
	}
	var missingContext context.Context
	if connection, err := Dial(missingContext, filepath.Join(directory, "missing.sock")); err == nil || connection != nil {
		t.Fatalf("nil-context dial = %#v, %v", connection, err)
	}
}

func TestUnixEndpointRejectsHostileAncestry(t *testing.T) {
	t.Parallel()
	root := unixPrivateDirectory(t)
	shared := filepath.Join(root, "shared")
	private := filepath.Join(shared, "private")
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	if listener, err := Listen(filepath.Join(private, "agent.sock")); !errors.Is(err, ErrUnsafeEndpoint) || listener != nil {
		t.Fatalf("writable ancestry listener = %#v, %v", listener, err)
	}

	realDirectory := filepath.Join(root, "real")
	linkedDirectory := filepath.Join(root, "linked")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDirectory, linkedDirectory); err != nil {
		t.Fatal(err)
	}
	if listener, err := Listen(filepath.Join(linkedDirectory, "agent.sock")); !errors.Is(err, ErrUnsafeEndpoint) || listener != nil {
		t.Fatalf("symlink ancestry listener = %#v, %v", listener, err)
	}
}

func TestUnixClosePreservesReplacementSocketIdentity(t *testing.T) {
	t.Parallel()
	address := filepath.Join(unixPrivateDirectory(t), "identity.sock")
	listener, err := Listen(address)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(address); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: address, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	replacement.SetUnlinkOnClose(false)
	t.Cleanup(func() {
		_ = replacement.Close()
		_ = os.Remove(address)
	})
	if err = os.Chmod(address, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = listener.Close(); !errors.Is(err, ErrUnsafeEndpoint) {
		t.Fatalf("close after socket replacement = %v", err)
	}
	if info, statErr := os.Lstat(address); statErr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("replacement socket was removed: %#v, %v", info, statErr)
	}
}

func TestUnixFailureBoundariesAndIdempotentCleanup(t *testing.T) {
	t.Parallel()
	directory := unixPrivateDirectory(t)
	missing := filepath.Join(directory, "missing.sock")
	if connection, err := Dial(t.Context(), missing); err == nil || connection != nil {
		t.Fatalf("missing socket dial = %#v, %v", connection, err)
	}

	stale := filepath.Join(directory, "refused.sock")
	raw, err := net.ListenUnix("unix", &net.UnixAddr{Name: stale, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	raw.SetUnlinkOnClose(false)
	if err = os.Chmod(stale, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}
	if connection, dialErr := Dial(t.Context(), stale); dialErr == nil || connection != nil {
		t.Fatalf("stale socket dial = %#v, %v", connection, dialErr)
	}

	unsafe := filepath.Join(directory, "unsafe.sock")
	raw, err = net.ListenUnix("unix", &net.UnixAddr{Name: unsafe, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	raw.SetUnlinkOnClose(false)
	t.Cleanup(func() {
		_ = raw.Close()
		_ = os.Remove(unsafe)
		_ = os.Remove(stale)
	})
	if err = os.Chmod(unsafe, 0o666); err != nil {
		t.Fatal(err)
	}
	if listener, listenErr := Listen(unsafe); !errors.Is(listenErr, ErrUnsafeEndpoint) || listener != nil {
		t.Fatalf("unsafe socket listener = %#v, %v", listener, listenErr)
	}

	owned := filepath.Join(directory, "owned.sock")
	listener, err := Listen(owned)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(owned); err != nil {
		t.Fatal(err)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err = listener.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	var nilListener *unixListener
	if err = nilListener.Close(); err != nil {
		t.Fatalf("nil close: %v", err)
	}
	if safeEndpointName(strings.Repeat("a", 129)) || safeEndpointName("not-safe!") {
		t.Fatal("unsafe endpoint name accepted")
	}
	if bound, openErr := openPrivateUnixDirectory("relative"); !errors.Is(openErr, ErrUnsafeEndpoint) || bound != nil {
		t.Fatalf("relative directory = %#v, %v", bound, openErr)
	}
}

func unixPrivateDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "ipc-")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		_ = os.RemoveAll(directory)
		t.Fatal(err)
	}
	directory = resolved
	if err = os.Chmod(directory, 0o700); err != nil {
		_ = os.RemoveAll(directory)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(directory); removeErr != nil {
			t.Errorf("remove Unix IPC fixture directory: %v", removeErr)
		}
	})
	return directory
}

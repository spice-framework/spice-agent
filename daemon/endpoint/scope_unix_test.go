//go:build linux || darwin

package endpoint

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCurrentUserUnixScopeUsesVerifiedXDGDirectory(t *testing.T) {
	root := filepath.Join(shortUnixTestDirectory(t), "runtime")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	uid := os.Geteuid()

	first, err := currentUserUnixScope(root, "unused", uid)
	if err != nil {
		t.Fatal(err)
	}
	second, err := currentUserUnixScope(root, "unused", uid)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("scope is not stable: first=%+v second=%+v", first, second)
	}
	wantDirectory := filepath.Join(root, "spice-agent")
	if first.Directory() != wantDirectory || first.Address() != filepath.Join(wantDirectory, unixSocketName) {
		t.Fatalf("scope = %+v", first)
	}
	if first.Transport() != TransportUnixSocket {
		t.Fatalf("Transport() = %q", first.Transport())
	}
	store, err := first.OpenStore(time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentUserUnixScopeFallsBackFromUnsafeXDG(t *testing.T) {
	parent := shortUnixTestDirectory(t)
	unsafeXDG := filepath.Join(parent, "unsafe-xdg")
	if err := os.Mkdir(unsafeXDG, 0o755); err != nil {
		t.Fatal(err)
	}
	sticky := filepath.Join(parent, "sticky")
	if err := os.Mkdir(sticky, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sticky, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}
	uid := os.Geteuid()

	scope, err := currentUserUnixScope(unsafeXDG, sticky, uid)
	if err != nil {
		t.Fatal(err)
	}
	wantDirectory := filepath.Join(sticky, "spice-agent-"+strconv.Itoa(uid))
	if scope.Directory() != wantDirectory || scope.Address() != filepath.Join(wantDirectory, unixSocketName) {
		t.Fatalf("fallback scope = %+v", scope)
	}
}

func TestCurrentUserUnixScopeRejectsUnsafeFallback(t *testing.T) {
	parent := shortUnixTestDirectory(t)
	unsafeTemp := filepath.Join(parent, "not-sticky")
	if err := os.Mkdir(unsafeTemp, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafeTemp, 0o777); err != nil {
		t.Fatal(err)
	}

	_, err := currentUserUnixScope(filepath.Join(parent, "missing-xdg"), unsafeTemp, os.Geteuid())
	if err == nil || !strings.Contains(err.Error(), "XDG_RUNTIME_DIR") ||
		!strings.Contains(err.Error(), "trusted sticky") {
		t.Fatalf("currentUserUnixScope() error = %v", err)
	}
}

func TestCurrentUserUnixScopeFallsBackWhenXDGSocketPathIsTooLong(t *testing.T) {
	parent := shortUnixTestDirectory(t)
	suffixLength := 101 - len(parent)
	if suffixLength < 1 {
		t.Skip("temporary test path is too long for the fallback fixture")
	}
	xdg := filepath.Join(parent, strings.Repeat("x", suffixLength))
	if err := os.Mkdir(xdg, 0o700); err != nil {
		t.Fatal(err)
	}
	sticky := filepath.Join(parent, "sticky")
	if err := os.Mkdir(sticky, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sticky, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}

	scope, err := currentUserUnixScope(xdg, sticky, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	if len(scope.Address()) > 100 || !strings.HasPrefix(scope.Directory(), sticky+string(filepath.Separator)) {
		t.Fatalf("fallback scope = %+v", scope)
	}
}

func TestCurrentUserUnixScopeAcceptsMaximumSocketPath(t *testing.T) {
	parent := shortUnixTestDirectory(t)
	suffixLength := 100 - len(parent) - len(string(filepath.Separator)+"spice-agent"+string(filepath.Separator)+unixSocketName) - 1
	if suffixLength < 1 {
		t.Skip("temporary test path is too long for the boundary fixture")
	}
	xdg := filepath.Join(parent, strings.Repeat("x", suffixLength))
	if err := os.Mkdir(xdg, 0o700); err != nil {
		t.Fatal(err)
	}

	scope, err := xdgUserScope(xdg, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(scope.Address()); got != 100 {
		t.Fatalf("socket address length = %d, want 100 (%q)", got, scope.Address())
	}
}

func TestCurrentUserUnixScopeRejectsInvalidIdentityAndSymlinkXDG(t *testing.T) {
	if _, err := currentUserUnixScope("", "/tmp", -1); err == nil {
		t.Fatal("negative user ID succeeded")
	}
	root := shortUnixTestDirectory(t)
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "runtime")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := xdgUserScope(link, os.Geteuid()); err == nil {
		t.Fatal("symlink XDG runtime directory succeeded")
	}
}

func platformTestTransport() Transport  { return TransportUnixSocket }
func platformOtherTransport() Transport { return TransportWindowsNamedPipe }
func platformTestAddress() string       { return "/tmp/spice-agent-test/agent.sock" }
func platformOtherAddress() string      { return `\\.\pipe\spice-agent-user-test` }
func platformTestDirectory() string     { return "/tmp/spice-agent-test" }
func platformTransportError() string    { return "Unix socket" }

func shortUnixTestDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp(defaultStickyTemp(), "sa-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(directory); removeErr != nil {
			t.Errorf("remove short Unix test directory: %v", removeErr)
		}
	})
	return realUnixTestDirectory(t, directory)
}

func realUnixTestDirectory(t *testing.T, directory string) string {
	t.Helper()
	realDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	return realDirectory
}

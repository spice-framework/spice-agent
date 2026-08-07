//go:build windows

package endpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestCurrentUserWindowsScopeIsStableAndUsable(t *testing.T) {
	root := windowsScopeTestRoot(t)
	sid := currentUserSID(t)

	first, err := currentUserWindowsScope(root, sid)
	if err != nil {
		t.Fatal(err)
	}
	second, err := currentUserWindowsScope(root, sid)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("scope is not stable: first=%+v second=%+v", first, second)
	}
	if first.Directory() != filepath.Join(root, windowsScopeDirectoryName, "runtime") {
		t.Fatalf("Directory() = %q", first.Directory())
	}
	if first.Transport() != TransportWindowsNamedPipe {
		t.Fatalf("Transport() = %q", first.Transport())
	}
	digest := sha256.Sum256([]byte(sid))
	wantAddress := `\\.\pipe\spice-agent-user-` + hex.EncodeToString(digest[:])
	if first.Address() != wantAddress {
		t.Fatalf("Address() = %q, want %q", first.Address(), wantAddress)
	}
	if err = first.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	store, err := first.OpenStore(time.Millisecond)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err = first.OpenStore(0); err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Fatalf("OpenStore(0) error = %v", err)
	}
}

func TestCurrentUserWindowsScopeRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(`C:\Users\scope-test`)
	tests := []struct {
		name string
		root string
		sid  string
	}{
		{name: "empty LocalAppData", sid: "S-1-5-21-1-2-3-1001"},
		{name: "relative LocalAppData", root: "AppData", sid: "S-1-5-21-1-2-3-1001"},
		{name: "unclean LocalAppData", root: root + `\..\scope-test`, sid: "S-1-5-21-1-2-3-1001"},
		{name: "empty SID", root: root},
		{name: "malformed SID", root: root, sid: "not-a-sid"},
		{name: "noncanonical SID", root: root, sid: "s-1-5-21-1-2-3-1001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := currentUserWindowsScope(test.root, test.sid); err == nil {
				t.Fatal("currentUserWindowsScope() succeeded")
			}
		})
	}
}

func TestCurrentUserScopeUsesCanonicalCurrentUserIdentity(t *testing.T) {
	first, err := CurrentUserScope()
	if err != nil {
		t.Fatal(err)
	}
	second, err := CurrentUserScope()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("CurrentUserScope() changed: first=%+v second=%+v", first, second)
	}
	if err = first.Validate(); err != nil {
		t.Fatal(err)
	}
}

func windowsScopeTestRoot(t *testing.T) string {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(cache, "spice-agent-endpoint-tests")
	if err = os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "scope-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(root); removeErr != nil {
			t.Errorf("remove endpoint scope test root: %v", removeErr)
		}
	})
	return root
}

func currentUserSID(t *testing.T) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if user == nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		t.Fatal("current user SID is unavailable")
	}
	return user.User.Sid.String()
}

func platformTestTransport() Transport  { return TransportWindowsNamedPipe }
func platformOtherTransport() Transport { return TransportUnixSocket }
func platformTestAddress() string       { return `\\.\pipe\spice-agent-user-test` }
func platformOtherAddress() string      { return "/tmp/agent.sock" }
func platformTestDirectory() string     { return `C:\Users\scope-test` }
func platformTransportError() string    { return "Windows named pipe" }

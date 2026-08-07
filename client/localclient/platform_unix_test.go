//go:build linux || darwin

package localclient

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spice-framework/spice-agent/daemon/endpoint"
)

func currentPlatformTransport() endpoint.Transport { return endpoint.TransportUnixSocket }
func otherPlatformTransport() endpoint.Transport   { return endpoint.TransportWindowsNamedPipe }
func otherPlatformAddress() string                 { return `\\.\pipe\spice-agent-other-platform` }

func currentPlatformAddress(t testing.TB) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "spice-agent-localclient-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	realDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(realDirectory, "agent.sock")
}

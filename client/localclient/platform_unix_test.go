//go:build linux || darwin

package localclient

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spice-framework/spice-agent/daemon/endpoint"
)

func currentPlatformTransport() endpoint.Transport { return endpoint.TransportUnixSocket }
func otherPlatformTransport() endpoint.Transport   { return endpoint.TransportWindowsNamedPipe }
func otherPlatformAddress() string                 { return `\\.\pipe\spice-agent-other-platform` }

func currentPlatformAddress(tb testing.TB) string {
	tb.Helper()
	directory, err := os.MkdirTemp(shortLocalClientTempRoot(), "sa-lc-")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = os.RemoveAll(directory) })
	realDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		tb.Fatal(err)
	}
	return filepath.Join(realDirectory, "agent.sock")
}

func shortLocalClientTempRoot() string {
	if runtime.GOOS == "darwin" {
		return "/private/tmp"
	}
	return ""
}

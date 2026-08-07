//go:build windows

package localclient

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/spice-framework/spice-agent/daemon/endpoint"
)

var testPipeSequence atomic.Uint64

func currentPlatformTransport() endpoint.Transport { return endpoint.TransportWindowsNamedPipe }
func otherPlatformTransport() endpoint.Transport   { return endpoint.TransportUnixSocket }
func otherPlatformAddress() string                 { return "/tmp/spice-agent-other-platform.sock" }

func currentPlatformAddress(testing.TB) string {
	return fmt.Sprintf(`\\.\pipe\spice-agent-localclient-%d-%d`, os.Getpid(), testPipeSequence.Add(1))
}

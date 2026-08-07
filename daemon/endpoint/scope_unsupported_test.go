//go:build !linux && !darwin && !windows

package endpoint

import "testing"

func TestCurrentUserScopeRejectsUnsupportedPlatform(t *testing.T) {
	if _, err := CurrentUserScope(); err == nil {
		t.Fatal("CurrentUserScope() succeeded")
	}
}

func platformTestTransport() Transport  { return TransportUnixSocket }
func platformOtherTransport() Transport { return TransportWindowsNamedPipe }
func platformTestAddress() string       { return "/tmp/spice-agent-test/agent.sock" }
func platformOtherAddress() string      { return `\\.\pipe\spice-agent-user-test` }
func platformTestDirectory() string     { return "/tmp/spice-agent-test" }
func platformTransportError() string    { return "unsupported" }

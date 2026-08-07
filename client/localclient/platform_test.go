package localclient

import (
	"testing"

	"github.com/spice-framework/spice-agent/daemon/endpoint"
)

func TestNewRejectsTransportForAnotherPlatform(t *testing.T) {
	t.Parallel()
	metadata := endpointFixture(t, otherPlatformAddress(), otherPlatformTransport())
	if _, err := New(metadata); err == nil {
		t.Fatal("New accepted another platform's transport")
	}
}

func currentMetadataFixture(tb testing.TB) endpoint.Metadata {
	tb.Helper()
	return endpointFixture(tb, currentPlatformAddress(tb), currentPlatformTransport())
}

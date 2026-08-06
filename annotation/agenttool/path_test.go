package agenttool_test

import (
	"testing"

	"github.com/spice-framework/spice-agent/annotation/agenttool"
)

func TestPathIsCanonicalToolPackage(t *testing.T) {
	t.Parallel()
	const expected = "github.com/spice-framework/spice-agent/cmd/spice-agent-annotations"
	if agenttool.Path != expected {
		t.Fatalf("Path = %q, want %q", agenttool.Path, expected)
	}
}

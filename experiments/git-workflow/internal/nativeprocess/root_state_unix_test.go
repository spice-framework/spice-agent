//go:build linux || darwin

package nativeprocess

import (
	"testing"

	agentprocess "github.com/spice-framework/spice-agent/process"
)

func assertPlatformRootTerminated(testing.TB, agentprocess.Process) {}

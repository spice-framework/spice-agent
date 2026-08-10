//go:build windows

package nativeprocess

import (
	"testing"
	"time"

	agentprocess "github.com/spice-framework/spice-agent/process"
	"golang.org/x/sys/windows"
)

func assertPlatformRootTerminated(t *testing.T, owned agentprocess.Process) {
	t.Helper()
	process := owned.(*windowsProcess)
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(process.command.Process.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle)
	state, err := windows.WaitForSingleObject(handle, uint32(time.Second/time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if state != windows.WAIT_OBJECT_0 {
		t.Fatalf("root process wait state = %d", state)
	}
}

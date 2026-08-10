package nativeprocess

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	agentprocess "github.com/spice-framework/spice-agent/process"
	"github.com/spice-framework/spice-agent/tool"
)

const (
	helperModeEnvironment   = "SPICE_GIT_NATIVE_HELPER_MODE"
	helperMarkerEnvironment = "SPICE_GIT_NATIVE_HELPER_MARKER"
	helperReadyEnvironment  = "SPICE_GIT_NATIVE_HELPER_READY"
)

func TestLauncherContainsDescendantAndJoinsCleanup(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "escaped-descendant")
	ready := filepath.Join(directory, "descendant-ready")
	input, release := io.Pipe()
	spec, err := agentprocess.NewSpec(agentprocess.Config{
		Executable:       executable,
		Arguments:        []string{"-test.run=^TestNativeProcessHelper$"},
		WorkingDirectory: directory,
		Environment: append(
			os.Environ(),
			helperModeEnvironment+"=root",
			helperMarkerEnvironment+"="+marker,
			helperReadyEnvironment+"="+ready,
		),
		Stdin: input, Stdout: io.Discard, Stderr: io.Discard,
		Capabilities: []tool.Capability{tool.CapabilityProcessExecute},
	})
	if err != nil {
		t.Fatal(err)
	}
	owned, err := NewLauncher().Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = release.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = owned.ForceKill(ctx)
		_ = owned.Wait(ctx)
	})
	if _, err = release.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err = release.Close(); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, ready)
	stopContext, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err = owned.RequestStop(stopContext); err != nil {
		t.Fatal(err)
	}
	assertPlatformRootTerminated(t, owned)
	if err = owned.Wait(stopContext); err != nil {
		t.Fatal(err)
	}
	select {
	case <-owned.Done():
	default:
		t.Fatal("owned process did not publish terminal state")
	}
	outcome, err := owned.Result()
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Successful() {
		t.Fatal("stopped helper reported success")
	}
	time.Sleep(500 * time.Millisecond)
	if _, err = os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant escaped containment: %v", err)
	}
}

func TestLauncherRejectsCanceledContextBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	owned, err := NewLauncher().Start(ctx, agentprocess.Spec{})
	if owned != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled launch = %#v, %v", owned, err)
	}
}

func TestNativeProcessHelper(t *testing.T) {
	mode := os.Getenv(helperModeEnvironment)
	if mode == "" {
		return
	}
	if mode == "child" {
		if err := os.WriteFile(os.Getenv(helperReadyEnvironment), []byte("ready"), 0o600); err != nil {
			os.Exit(91)
		}
		time.Sleep(300 * time.Millisecond)
		if err := os.WriteFile(os.Getenv(helperMarkerEnvironment), []byte("escaped"), 0o600); err != nil {
			os.Exit(92)
		}
		return
	}
	if mode != "root" {
		os.Exit(93)
	}
	var release [1]byte
	if _, err := io.ReadFull(os.Stdin, release[:]); err != nil {
		os.Exit(94)
	}
	// #nosec G204 -- this is a test-only invocation of the current test binary.
	command := exec.Command(os.Args[0], "-test.run=^TestNativeProcessHelper$")
	command.Env = replaceEnvironment(os.Environ(), helperModeEnvironment, "child")
	if err := command.Start(); err != nil {
		os.Exit(95)
	}
	if err := command.Wait(); err != nil {
		os.Exit(96)
	}
}

func waitForFile(t testing.TB, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("helper did not create %s on %s", filepath.Base(path), runtime.GOOS)
}

func replaceEnvironment(values []string, name, value string) []string {
	result := make([]string, 0, len(values)+1)
	prefix := name + "="
	for _, entry := range values {
		if len(entry) >= len(prefix) && entry[:len(prefix)] == prefix {
			continue
		}
		result = append(result, entry)
	}
	return append(result, prefix+value)
}

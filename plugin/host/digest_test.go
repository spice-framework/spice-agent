package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/process"
)

func TestVerifiedExecutableLifecycle(t *testing.T) {
	t.Parallel()
	path, digest := writeTestExecutable(t, []byte("verified executable content"))
	executable := executableForPath(t, path, digest)
	lease, err := openVerifiedExecutable(context.Background(), executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Recheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	if err := lease.Recheck(context.Background()); err == nil {
		t.Fatal("expected closed lease rejection")
	}
}

func TestNilVerifiedExecutableRecheckIsSafe(t *testing.T) {
	t.Parallel()
	var lease *process.ExecutableLease
	if err := lease.Recheck(context.Background()); err == nil {
		t.Fatal("expected nil lease rejection")
	}
}

func TestVerifiedExecutableRejectsDigestMismatchSafely(t *testing.T) {
	t.Parallel()
	path, _ := writeTestExecutable(t, []byte("private executable content"))
	digest, err := ParseSHA256(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	_, err = openVerifiedExecutable(context.Background(), executableForPath(t, path, digest))
	if err == nil {
		t.Fatal("expected digest rejection")
	}
	for _, private := range []string{path, digest.String(), "private executable content"} {
		if strings.Contains(err.Error(), private) {
			t.Fatalf("error %q exposed private material", err)
		}
	}
}

func TestVerifiedExecutableHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	path, digest := writeTestExecutable(t, []byte("content"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := openVerifiedExecutable(ctx, executableForPath(t, path, digest))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifiedExecutableRejectsDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	digest, err := ParseSHA256(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	_, err = openVerifiedExecutable(context.Background(), executableForPath(t, root, digest))
	if err == nil {
		t.Fatal("expected directory rejection")
	}
	if strings.Contains(err.Error(), root) {
		t.Fatal("error exposed executable path")
	}
}

func writeTestExecutable(t *testing.T, content []byte) (string, SHA256) {
	t.Helper()
	name := "runtime-plugin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	digest, err := ParseSHA256(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	return path, digest
}

func executableForPath(t *testing.T, path string, digest SHA256) Executable {
	t.Helper()
	config := validExecutableConfig(t)
	config.Path = path
	config.WorkingDirectory = filepath.Dir(path)
	config.SHA256 = digest
	executable, err := NewExecutable(config)
	if err != nil {
		t.Fatal(err)
	}
	return executable
}

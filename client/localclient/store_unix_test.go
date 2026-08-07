//go:build linux || darwin

package localclient

import (
	"os"
	"path/filepath"
	"testing"
)

func currentStoreDirectory(t testing.TB) string {
	t.Helper()
	root, err := os.MkdirTemp("", "spice-agent-localclient-store-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(realRoot, "endpoint")
}

//go:build linux || darwin

package localclient

import (
	"os"
	"path/filepath"
	"testing"
)

func currentStoreDirectory(tb testing.TB) string {
	tb.Helper()
	root, err := os.MkdirTemp(shortLocalClientTempRoot(), "sa-store-")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = os.RemoveAll(root) })
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		tb.Fatal(err)
	}
	return filepath.Join(realRoot, "endpoint")
}

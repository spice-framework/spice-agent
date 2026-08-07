//go:build windows

package localclient

import (
	"os"
	"path/filepath"
	"testing"
)

func currentStoreDirectory(tb testing.TB) string {
	tb.Helper()
	cache, err := os.UserCacheDir()
	if err != nil {
		tb.Fatal(err)
	}
	base := filepath.Join(cache, "spice-agent-localclient-tests")
	if err = os.MkdirAll(base, 0o700); err != nil {
		tb.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "store-")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = os.RemoveAll(root) })
	return filepath.Join(root, "endpoint")
}

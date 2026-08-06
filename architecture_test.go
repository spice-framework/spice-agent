package spiceagent_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompiledCompositionHasNoParallelRuntimeMechanism(t *testing.T) {
	t.Parallel()
	forbidden := []string{
		"type Runtime" + "Graph",
		"type Service" + "Locator",
		"type Extension" + "Registry",
		"reflect" + ".Value",
		"plugin" + ".Open(",
		"packages" + ".Load(",
		"switch invocation.Canonical" + "Name",
		"switch params.Descriptor." + "Name",
	}
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "vendor") {
			return filepath.SkipDir
		}
		if filepath.ToSlash(path) == "internal/qualitygate/main.go" {
			return nil
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path) // #nosec G304 -- path is rooted in the walked repository.
		if readErr != nil {
			return readErr
		}
		for _, value := range forbidden {
			if bytes.Contains(content, []byte(value)) {
				t.Errorf("%s contains forbidden compiled composition mechanism %q", path, value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

package spiceagent_test

import (
	"bytes"
	"os"
	"testing"
)

func TestModulithUsesOneRepositoryRootAndUniquePublicInterfaces(t *testing.T) {
	t.Parallel()
	root, err := os.ReadFile("doc.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(root, []byte("// @Module")) != 1 || bytes.Contains(root, []byte("allowedDependencies")) {
		t.Fatalf("root module declaration = %q", root)
	}
	expected := map[string]string{
		"agent/doc.go":            "kernel",
		"annotation/agent/doc.go": "annotations",
		"event/doc.go":            "event",
		"interaction/doc.go":      "interaction",
		"message/doc.go":          "message",
		"model/doc.go":            "model",
		"stage/doc.go":            "stage",
		"tool/doc.go":             "tool",
	}
	seen := make(map[string]struct{}, len(expected))
	for path, name := range expected {
		content, readErr := os.ReadFile(path) // #nosec G304 -- paths are fixed repository contracts.
		if readErr != nil {
			t.Fatal(readErr)
		}
		if bytes.Contains(content, []byte("// @Module")) || bytes.Contains(content, []byte("allowedDependencies")) {
			t.Fatalf("descendant %s declares a competing module boundary", path)
		}
		marker := []byte("// @NamedInterface(\"" + name + "\")")
		if bytes.Count(content, marker) != 1 {
			t.Fatalf("descendant %s does not expose %q exactly once", path, name)
		}
		if _, duplicate := seen[name]; duplicate {
			t.Fatalf("named interface %q is duplicated", name)
		}
		seen[name] = struct{}{}
	}
	if _, err = os.Stat("internal/annotationtool/doc.go"); !os.IsNotExist(err) {
		t.Fatal("internal annotation tool must remain an unexposed module internal")
	}
}

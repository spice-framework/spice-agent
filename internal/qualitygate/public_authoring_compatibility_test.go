package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicAuthoringCompatibilityManifestFailsClosed(t *testing.T) {
	t.Parallel()
	repository, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(publicAuthoringCompatibilityPath)))
	if err != nil {
		t.Fatal(err)
	}
	valid := string(content)
	tests := []struct {
		name    string
		content string
	}{
		{name: "valid", content: valid},
		{name: "unknown field", content: strings.Replace(valid, "  \"module\":", "  \"unknown\": true,\n  \"module\":", 1)},
		{name: "one extension", content: strings.Replace(valid, "\"required_extensions\": 3", "\"required_extensions\": 1", 1)},
		{name: "inherited module cache", content: strings.Replace(valid, "\"fresh_module_cache\": true", "\"fresh_module_cache\": false", 1)},
		{name: "workspace mode", content: strings.Replace(valid, "\"gowork\": \"off\"", "\"gowork\": \"auto\"", 1)},
		{name: "replace escape", content: strings.Replace(valid, "\"replace_directives\": false", "\"replace_directives\": true", 1)},
		{name: "missing Windows", content: strings.Replace(valid, "    \"linux/amd64\",\n    \"windows/amd64\"", "    \"linux/amd64\"", 1)},
		{name: "missing deletion", content: strings.Replace(valid, ",\n    \"delete\"", "", 1)},
		{name: "false proof", content: strings.Replace(valid, "\"proven\": false", "\"proven\": true", 1)},
		{name: "noncanonical", content: strings.Replace(valid, "  \"schema\"", "    \"schema\"", 1)},
		{name: "multiple values", content: valid + "{}\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeGateFile(t, root, publicAuthoringCompatibilityPath, test.content)
			err := checkPublicAuthoringCompatibility(root)
			if test.name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid public authoring compatibility manifest succeeded")
			}
		})
	}

	if err := checkPublicAuthoringCompatibility(t.TempDir()); err == nil {
		t.Fatal("missing public authoring compatibility manifest succeeded")
	}
}

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
		{name: "status regressed", content: strings.Replace(valid, "\"status\": \"sdk-beta-proven-phase8-pending\"", "\"status\": \"required-not-proven\"", 1)},
		{name: "development generator", content: strings.Replace(valid, "\"version\": \"v0.1.0-preview.2\"", "\"version\": \"0.1.0-dev\"", 1)},
		{name: "stale ownership schema", content: strings.Replace(valid, "\"manifest_schema\": 6", "\"manifest_schema\": 5", 1)},
		{name: "inherited module cache", content: strings.Replace(valid, "\"fresh_module_cache\": true", "\"fresh_module_cache\": false", 1)},
		{name: "workspace mode", content: strings.Replace(valid, "\"gowork\": \"off\"", "\"gowork\": \"auto\"", 1)},
		{name: "replace escape", content: strings.Replace(valid, "\"replace_directives\": false", "\"replace_directives\": true", 1)},
		{name: "missing Windows", content: strings.Replace(valid, "    \"linux/amd64\",\n    \"windows/amd64\"", "    \"linux/amd64\"", 1)},
		{name: "missing deletion", content: strings.Replace(valid, ",\n    \"delete\"", "", 1)},
		{name: "wrong evidence commit", content: strings.Replace(valid, "cbc738067e9f67efd273509481488ba5eadfe1bd", strings.Repeat("0", 40), 1)},
		{name: "wrong evidence tag object", content: strings.Replace(valid, "36539996097937196711433a1e501d299b8fbe9f", strings.Repeat("1", 40), 1)},
		{name: "wrong evidence profile", content: strings.Replace(valid, "compiled-tool-autoconfigure/v1alpha1-preview6", "compiled-tool-autoconfigure/v1alpha1-preview5", 1)},
		{name: "wrong evidence release", content: strings.Replace(valid, "releases/tag/v0.1.0-preview.1", "releases/tag/v0.1.0-preview.2", 1)},
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

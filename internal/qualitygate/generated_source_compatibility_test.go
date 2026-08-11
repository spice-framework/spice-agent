package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedSourceCompatibilityManifestFailsClosed(t *testing.T) {
	t.Parallel()
	repository, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(generatedSourceCompatibilityPath)))
	if err != nil {
		t.Fatal(err)
	}
	valid := string(content)
	manifestMutations := []struct {
		name        string
		old         string
		replacement string
	}{
		{name: "unknown field", old: "  \"module\":", replacement: "  \"unknown\": true,\n  \"module\":"},
		{name: "development generator", old: "\"version\": \"v0.1.0-preview.2\"", replacement: "\"version\": \"0.1.0-dev\""},
		{name: "generator sum", old: generatorSum, replacement: "h1:changed="},
		{name: "schema five", old: "\"manifest_schema\": 6", replacement: "\"manifest_schema\": 5"},
		{name: "missing target", old: "      \"module\": \"github.com/spice-framework/spice-agent\",\n      \"module_root\": \".\",", replacement: "      \"module\": \"github.com/spice-framework/spice-agent/changed\",\n      \"module_root\": \".\","},
		{name: "lost clean room exercise", old: "\"exercised\": true", replacement: "\"exercised\": false"},
		{name: "wrong clean room count", old: "\"exercised_extensions\": 3", replacement: "\"exercised_extensions\": 2"},
		{name: "proof regressed", old: "\"proven\": true", replacement: "\"proven\": false"},
		{name: "noncanonical", old: "  \"schema\"", replacement: "    \"schema\""},
	}
	for _, test := range manifestMutations {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			mutated := strings.Replace(valid, test.old, test.replacement, 1)
			if mutated == valid {
				t.Fatal("manifest mutation did not apply")
			}
			root := copyGeneratedSourceFixture(t, repository, mutated)
			if err := checkGeneratedSourceCompatibility(root); err == nil {
				t.Fatal("invalid generated source compatibility manifest succeeded")
			}
		})
	}
	t.Run("multiple values", func(t *testing.T) {
		t.Parallel()
		root := copyGeneratedSourceFixture(t, repository, valid+"{}\n")
		if err := checkGeneratedSourceCompatibility(root); err == nil {
			t.Fatal("multiple generated source manifest values succeeded")
		}
	})

	selectionMutations := []struct {
		name        string
		relative    string
		old         string
		replacement string
	}{
		{name: "go.mod version", relative: "go.mod", old: generatorModulePath + " " + generatorVersion, replacement: generatorModulePath + " v0.1.0-preview.1"},
		{name: "go.sum content", relative: "go.sum", old: generatorSum, replacement: "h1:changed="},
		{name: "vendor selection", relative: "vendor/modules.txt", old: "# " + generatorModulePath + " " + generatorVersion, replacement: "# " + generatorModulePath + " v0.1.0-preview.1"},
		{name: "ownership schema", relative: ".spice/compositionproof.manifest.json", old: "\"schema\": 6", replacement: "\"schema\": 5"},
		{name: "ownership generator", relative: ".spice/compositionproof.manifest.json", old: "\"generator_version\": \"v0.1.0-preview.2\"", replacement: "\"generator_version\": \"0.1.0-dev\""},
		{name: "ownership target", relative: ".spice/compositionproof.manifest.json", old: "\"id\": \"compositionproof\"", replacement: "\"id\": \"changed\""},
	}
	for _, test := range selectionMutations {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := copyGeneratedSourceFixture(t, repository, valid)
			path := filepath.Join(root, filepath.FromSlash(test.relative))
			value, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			writeGateFile(t, root, test.relative, strings.Replace(string(value), test.old, test.replacement, 1))
			if err := checkGeneratedSourceCompatibility(root); err == nil {
				t.Fatal("invalid generated source selection succeeded")
			}
		})
	}

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		if err := checkGeneratedSourceCompatibility(copyGeneratedSourceFixture(t, repository, valid)); err != nil {
			t.Fatal(err)
		}
	})
	if err := checkGeneratedSourceCompatibility(t.TempDir()); err == nil {
		t.Fatal("missing generated source compatibility manifest succeeded")
	}
}

func copyGeneratedSourceFixture(t *testing.T, repository, manifest string) string {
	t.Helper()
	root := t.TempDir()
	writeGateFile(t, root, generatedSourceCompatibilityPath, manifest)
	copyGeneratedSourceInputs(t, repository, root)
	return root
}

func copyGeneratedSourceInputs(t *testing.T, repository, root string) {
	t.Helper()
	for _, target := range expectedGeneratedSourceCompatibility().Targets {
		for _, relative := range []string{
			target.Manifest,
			filepath.ToSlash(filepath.Join(target.ModuleRoot, "go.mod")),
			filepath.ToSlash(filepath.Join(target.ModuleRoot, "go.sum")),
			filepath.ToSlash(filepath.Join(target.ModuleRoot, "vendor", "modules.txt")),
		} {
			content, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(relative)))
			if err != nil {
				t.Fatal(err)
			}
			writeGateFile(t, root, relative, string(content))
		}
	}
}

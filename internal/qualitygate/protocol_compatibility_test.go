package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEngineProtocolCompatibilityManifestFailsClosed(t *testing.T) {
	t.Parallel()
	valid := canonicalEngineProtocolCompatibility(t)
	for _, test := range []struct {
		name    string
		content string
	}{
		{name: "valid", content: valid},
		{name: "unknown field", content: strings.Replace(valid, "  \"protocol\":", "  \"unknown\": true,\n  \"protocol\":", 1)},
		{name: "future production", content: strings.Replace(valid, "\"maximum\": \"1.3.0\"", "\"maximum\": \"1.4.0\"", 1)},
		{name: "legacy retries", content: strings.Replace(valid, "\"automatic_unavailable_retries\": 0", "\"automatic_unavailable_retries\": 1", 1)},
		{name: "missing required case", content: strings.Replace(valid, "    \"process-cleanup\"\n", "", 1)},
		{name: "false binary proof", content: strings.Replace(valid, "\"proven\": false", "\"proven\": true", 1)},
		{name: "plugin manifest redirected", content: strings.Replace(valid, "plugin/v1/compatibility.json", "plugin/v1/other.json", 1)},
		{name: "false v1 stability", content: strings.Replace(valid, "\"v1_stable\": false", "\"v1_stable\": true", 1)},
		{name: "history removed", content: strings.Replace(valid, "    {\n      \"version\": \"1.2.0\",", "    {\n      \"version\": \"1.1.0\",", 1)},
		{name: "noncanonical", content: strings.Replace(valid, "  \"schema\"", "    \"schema\"", 1)},
		{name: "multiple values", content: valid + "{}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeGateFile(t, root, engineProtocolCompatibilityPath, test.content)
			err := checkEngineProtocolCompatibility(root)
			if test.name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid engine protocol compatibility manifest succeeded")
			}
		})
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "engine", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkEngineProtocolCompatibility(root); err == nil {
		t.Fatal("missing engine protocol compatibility manifest succeeded")
	}
}

func TestCompatibilityManifestsFailClosed(t *testing.T) {
	t.Parallel()
	repository, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		path        string
		old         string
		replacement string
	}{
		{name: "benchmark policy weakened", path: compatibilityPolicyPath, old: "\"status\": \"stable-enforced\"", replacement: "\"status\": \"advisory\""},
		{name: "benchmark ceiling widened", path: benchmarkBudgetPath, old: "\"maximum_ns_per_op\": 1000000", replacement: "\"maximum_ns_per_op\": 2000000"},
		{name: "public authoring count weakened", path: publicAuthoringCompatibilityPath, old: "\"required_extensions\": 3", replacement: "\"required_extensions\": 1"},
		{name: "public authoring proof regressed", path: publicAuthoringCompatibilityPath, old: "\"proven\": true", replacement: "\"proven\": false"},
		{name: "public authoring evidence removed", path: publicAuthoringCompatibilityPath, old: "      \"module\": \"github.com/spice-framework/spice-agent-tool-text\",\n", replacement: "      \"module\": \"github.com/spice-framework/spice-agent-tool-missing\",\n"},
		{name: "second public authoring evidence removed", path: publicAuthoringCompatibilityPath, old: "      \"module\": \"github.com/spice-framework/spice-agent-tool-json\",\n", replacement: "      \"module\": \"github.com/spice-framework/spice-agent-tool-missing\",\n"},
		{name: "third public authoring evidence removed", path: publicAuthoringCompatibilityPath, old: "      \"module\": \"github.com/spice-framework/spice-agent-tool-integer\",\n", replacement: "      \"module\": \"github.com/spice-framework/spice-agent-tool-missing\",\n"},
		{name: "public authoring module sum drift", path: publicAuthoringCompatibilityPath, old: "h1:e9qhtkySbuL/47k/dx9S9U0Y17MhI42p0pcEBrruPF4=", replacement: "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="},
		{name: "public authoring run drift", path: publicAuthoringCompatibilityPath, old: "actions/runs/31442525658", replacement: "actions/runs/1"},
		{name: "public authoring progress regressed", path: compatibilityPolicyPath, old: "\"proven_extensions\": 3", replacement: "\"proven_extensions\": 2"},
		{name: "generated clean-room progress regressed", path: generatedSourceCompatibilityPath, old: "\"exercised_extensions\": 3", replacement: "\"exercised_extensions\": 2"},
		{name: "generated source version drift", path: generatedSourceCompatibilityPath, old: "\"version\": \"v0.1.0-preview.2\"", replacement: "\"version\": \"0.1.0-dev\""},
		{name: "generated source schema drift", path: generatedSourceCompatibilityPath, old: "\"manifest_schema\": 6", replacement: "\"manifest_schema\": 5"},
		{name: "public authoring blocker reintroduced", path: compatibilityPolicyPath, old: "  \"v1_blockers\": [\n", replacement: "  \"v1_blockers\": [\n    \"clean-room-public-authoring-proof\",\n"},
		{name: "generated source exercise blocker reintroduced", path: compatibilityPolicyPath, old: "  \"v1_blockers\": [\n", replacement: "  \"v1_blockers\": [\n    \"clean-room-generated-source-exercise\",\n"},
		{name: "public authoring manifest redirected", path: compatibilityPolicyPath, old: publicAuthoringCompatibilityPath, replacement: "compatibility/other-authoring.json"},
		{name: "generated source manifest redirected", path: compatibilityPolicyPath, old: generatedSourceCompatibilityPath, replacement: "compatibility/other-generated-source.json"},
		{name: "Go API platform drift", path: goAPICompatibilityPath, old: "\"goarch\": \"arm64\"", replacement: "\"goarch\": \"amd64\""},
		{name: "Go API digest drift", path: goAPICompatibilityPath, old: "e70ab391059d657839a3722ac9d700853d6e432c3776f17231ee04de36e712e8", replacement: strings.Repeat("a", 64)},
		{name: "Go API approved break rewritten", path: goAPICompatibilityPath, old: "\"kind\": \"interface-signature\"", replacement: "\"kind\": \"addition\""},
		{name: "durable history removed", path: durableCompatibilityPath, old: "\"status\": \"rejected-missing-workspace-authority\"", replacement: "\"status\": \"rejected\""},
		{name: "plugin coupled to engine", path: pluginProtocolCompatibilityPath, old: "\"versioning\": \"independent-from-engine\"", replacement: "\"versioning\": \"engine-coupled\""},
		{name: "released generation version drift", path: releasedGenerationCompatibilityPath, old: "\"version\": \"v0.1.0-preview.5\"", replacement: "\"version\": \"v0.1.0-preview.4\""},
		{name: "released generation sum drift", path: releasedGenerationCompatibilityPath, old: "h1:XJKJge+xWP/FLNoL1/rXq8z8tdu/5iEkKfmu1dTgFms=", replacement: "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="},
		{name: "released generation direction drift", path: releasedGenerationCompatibilityPath, old: "preview5-client-to-preview6-server", replacement: "preview5-client-to-preview5-server"},
		{name: "released generation source drift", path: releasedGenerationCompatibilityPath, old: "ac1728b36cca211f853e4a9720a9ff46f39b00ca6834002a225dfb4ac6f22518", replacement: strings.Repeat("a", 64)},
		{name: "released generation false proof", path: releasedGenerationCompatibilityPath, old: "\"proven\": false", replacement: "\"proven\": true"},
		{name: "security downgrade", path: securityExceptionsPath, old: "\"active\": []", replacement: "\"active\": [{\"id\": \"test\", \"downgrade\": true}]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			copyCompatibilityFixture(t, repository, root)
			path := filepath.Join(root, filepath.FromSlash(test.path))
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			mutated := strings.Replace(string(content), test.old, test.replacement, 1)
			if mutated == string(content) {
				t.Fatalf("mutation %q did not apply", test.name)
			}
			if writeErr := os.WriteFile(path, []byte(mutated), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			if err := checkCompatibilityManifests(root); err == nil {
				t.Fatal("mutated compatibility contract succeeded")
			}
		})
	}
}

func copyCompatibilityFixture(t *testing.T, repository, root string) {
	t.Helper()
	for _, relative := range compatibilityManifestPaths() {
		content, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		writeGateFile(t, root, relative, string(content))
	}
	copyGeneratedSourceInputs(t, repository, root)
	peerSource := filepath.Join(repository, "internal", "releasedcompatibility", "testdata", "peer")
	entries, err := os.ReadDir(peerSource)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		content, readErr := os.ReadFile(filepath.Join(peerSource, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		writeGateFile(t, root, filepath.ToSlash(filepath.Join("internal", "releasedcompatibility", "testdata", "peer", entry.Name())), string(content))
	}
	for _, relative := range []string{
		"docs/migrations/v0.1.0-preview.4-to-v0.1.0-preview.5.md",
		"docs/migrations/v0.1.0-preview.5-to-v0.1.0-preview.6.md",
	} {
		writeGateFile(t, root, relative, "migration fixture\n")
	}
}

func TestSecurityExceptionContract(t *testing.T) {
	t.Parallel()
	exception := securityException{
		ID: "SEC-0001", Advisory: "GO-TEST-0001", Affected: []string{"v0.1.0-preview.5"},
		FixedIn: "next-preview", Effect: "support-window-narrowed",
		EffectiveAt: "2026-08-10T00:00:00Z", ReviewBy: "2026-08-17T00:00:00Z",
		Migration: "upgrade to the fixed preview", Status: "active",
	}
	value := securityExceptions{
		Schema: "spice.agent.security-exceptions/v1alpha1", Module: modulePath,
		DowngradeToWithdrawnVersion: "forbidden", Active: []securityException{exception},
	}
	if err := validateSecurityExceptions(value); err != nil {
		t.Fatal(err)
	}
	value.History = []securityException{exception}
	if err := validateSecurityExceptions(value); err == nil {
		t.Fatal("duplicate active/history security exception succeeded")
	}
	value.History = nil
	value.Active[0].Downgrade = true
	if err := validateSecurityExceptions(value); err == nil {
		t.Fatal("security downgrade exception succeeded")
	}
}

func canonicalEngineProtocolCompatibility(t *testing.T) string {
	t.Helper()
	encoded, err := json.MarshalIndent(expectedEngineProtocolCompatibility(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded) + "\n"
}

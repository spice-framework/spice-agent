package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhaseEightEvidenceContractsFailClosed(t *testing.T) {
	t.Parallel()
	repository, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, path, old, replacement string
	}{
		{"API evidence removed", apiUsagePath, "spice-agent-coding", ""},
		{"API package redirected", apiUsagePath, modulePath + "/agent", modulePath + "/other"},
		{"security response weakened", securityProcessPath, "\"coordinated_disclosure\": true", "\"coordinated_disclosure\": false"},
		{"security cadence widened", securityProcessPath, "\"critical_review_days\": 1", "\"critical_review_days\": 30"},
		{"kernel consumer removed", kernelConceptsPath, "spice-agent-coding-terminal-policy", ""},
		{"kernel unresolved rewritten", kernelConceptsPath, "semantic-client-session-second-conformance-consumer", "single-extension"},
	} {
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
				t.Fatal("mutation did not apply")
			}
			if writeErr := os.WriteFile(path, []byte(mutated), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			if err := checkCompatibilityManifests(root); err == nil {
				t.Fatal("mutated Phase 8 evidence contract succeeded")
			}
		})
	}
}

func TestPhaseEightEvidenceManifestsAreCanonical(t *testing.T) {
	t.Parallel()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{apiUsagePath, securityProcessPath, kernelConceptsPath} {
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		var value any
		if err = json.Unmarshal(content, &value); err != nil {
			t.Fatal(err)
		}
	}
}

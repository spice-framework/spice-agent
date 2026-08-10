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
		{name: "plugin version coupled", content: strings.Replace(valid, "\"versioning\": \"independent-from-engine\"", "\"versioning\": \"engine-coupled\"", 1)},
		{name: "missing Python breadth", content: strings.Replace(valid, "      \"python\"\n", "", 1)},
		{name: "false Python host launch", content: strings.Replace(valid, "\"python\": \"future-pinned-native-artifact-required\"", "\"python\": \"separately-proven\"", 1)},
		{name: "plugin breadth still pending", content: strings.Replace(valid, "\"plugin_protocol_next_slice\": false", "\"plugin_protocol_next_slice\": true", 1)},
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

func canonicalEngineProtocolCompatibility(t *testing.T) string {
	t.Helper()
	encoded, err := json.MarshalIndent(expectedEngineProtocolCompatibility(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded) + "\n"
}

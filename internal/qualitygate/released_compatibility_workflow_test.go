package main

import (
	"strings"
	"testing"
)

func TestReleasedCompatibilityWorkflowFailsClosed(t *testing.T) {
	t.Parallel()
	valid := (releasedCompatibilityWorkflow{}).Expected()
	for _, test := range []struct {
		name        string
		old         string
		replacement string
	}{
		{name: "missing Linux", old: "  linux:\n", replacement: "  omitted:\n"},
		{name: "missing Windows", old: "  windows:\n", replacement: "  omitted:\n"},
		{name: "network cache reused", old: "cache: false", replacement: "cache: true"},
		{name: "workspace enabled", old: "-mode=released-compatibility", replacement: "-mode=verify"},
		{name: "required bypass", old: "needs: [linux, windows]", replacement: "needs: [linux]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			mutated := strings.Replace(valid, test.old, test.replacement, 1)
			writeGateFile(t, root, ".github/workflows/released-compatibility.yml", mutated)
			if err := (releasedCompatibilityWorkflow{}).Validate(root); err == nil {
				t.Fatal("mutated released compatibility workflow succeeded")
			}
		})
	}
}

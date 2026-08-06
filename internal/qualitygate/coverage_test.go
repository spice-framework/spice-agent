package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExcludeGeneratedCoverageKeepsHandwrittenStatements(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "coverage.out")
	content := strings.Join([]string{
		"mode: atomic",
		modulePath + "/agent/engine.go:10.1,12.2 1 1",
		modulePath + "/internal/spicegen/proof/spice_providers_gen.go:10.1,12.2 1 1",
		modulePath + "/internal/spicegen/proof/sources/app/app_spice_gen.go:8.1,9.2 1 1",
		"example.com/external/internal/spicegen/handwritten.go:1.1,2.2 1 1",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := excludeGeneratedCoverage(path); err != nil {
		t.Fatal(err)
	}
	filtered, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(filtered)
	if strings.Contains(text, modulePath+"/internal/spicegen/") {
		t.Fatalf("generated coverage remains: %s", text)
	}
	for _, expected := range []string{
		"mode: atomic",
		modulePath + "/agent/engine.go",
		"example.com/external/internal/spicegen/handwritten.go",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("filtered profile missing %q: %s", expected, text)
		}
	}
}

func TestExcludeGeneratedCoverageRejectsInvalidProfile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "coverage.out")
	if err := os.WriteFile(path, []byte("invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := excludeGeneratedCoverage(path); err == nil {
		t.Fatal("excludeGeneratedCoverage error = nil")
	}
}

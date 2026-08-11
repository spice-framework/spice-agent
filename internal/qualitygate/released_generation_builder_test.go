package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleasedGenerationBuilderProtectsOnlyOwnedExecutables(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	builder, err := newReleasedGenerationBuilder(t.TempDir(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(workspace, "peer")
	if err = os.WriteFile(executable, []byte("peer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = builder.protectExecutable(executable); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(executable)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("executable mode = %o, want 700", got)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err = os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = builder.protectExecutable(outside); err == nil {
		t.Fatal("outside executable protection succeeded")
	}
}

func TestReleasedGenerationBuilderSeparatesSourceAndExecutablePaths(t *testing.T) {
	t.Parallel()
	builder, err := newReleasedGenerationBuilder(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"previous", "current"} {
		source := builder.sourceDirectory(role)
		if source == builder.peerExecutable(role) || source == builder.fixtureExecutable(role) {
			t.Fatalf("source and executable paths collide for %s", role)
		}
	}
}

func TestReleasedGenerationDownloadIdentityFailsClosed(t *testing.T) {
	t.Parallel()
	generation := releasedGeneration{
		Role: "previous", Version: "v0.1.0-preview.5", Commit: "3e8fe6406171a7e7f1765311a4fa7fc3b878e425",
		ModuleSum: "h1:rGND9DYx3pssliD1tZQOvPDOZ5GVfQLDc7VJQI3HLOM=", GoModSum: "h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=",
	}
	download := releasedModuleDownload{
		Path: modulePath, Version: generation.Version, Sum: generation.ModuleSum, GoModSum: generation.GoModSum,
	}
	download.Origin.VCS = "git"
	download.Origin.URL = "https://github.com/spice-framework/spice-agent"
	download.Origin.Hash = generation.Commit
	download.Origin.Ref = "refs/tags/" + generation.Version
	encoded, err := json.Marshal(download)
	if err != nil {
		t.Fatal(err)
	}
	builder := &releasedGenerationBuilder{}
	if err = builder.validateDownload(string(encoded), generation); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		strings.Replace(string(encoded), generation.ModuleSum, "h1:invalid", 1),
		strings.Replace(string(encoded), generation.Commit, strings.Repeat("0", 40), 1),
		strings.TrimSuffix(string(encoded), "}") + ",\"Unknown\":true}",
		string(encoded) + "{}",
	} {
		if err = builder.validateDownload(invalid, generation); err == nil {
			t.Fatal("invalid released module download succeeded")
		}
	}
}

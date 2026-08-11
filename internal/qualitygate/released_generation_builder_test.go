package main

import (
	"encoding/json"
	"strings"
	"testing"
)

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

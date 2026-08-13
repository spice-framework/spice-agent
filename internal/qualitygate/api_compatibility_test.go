package main

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestPublicGoAPIBaselineIsPlatformTruthful(t *testing.T) {
	t.Parallel()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	digests, packages, err := currentAPIDigests(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 29 || !slices.Contains(packages, modulePath+"/client/conformance") ||
		!slices.Contains(packages, modulePath+"/common/v1") ||
		!slices.Contains(packages, modulePath+"/engine/v1") || !slices.Contains(packages, modulePath+"/plugin/v1") {
		t.Fatalf("public package inventory = %v", packages)
	}
	if len(digests) != 3 || digests[0].GOOS != "darwin" || digests[1].GOOS != "linux" || digests[2].GOOS != "windows" {
		t.Fatalf("platform digests = %+v", digests)
	}
	if digests[0].DeclarationSHA256 != digests[1].DeclarationSHA256 {
		t.Fatal("Darwin and Linux declarations unexpectedly differ")
	}
	if digests[1].DeclarationSHA256 == digests[2].DeclarationSHA256 {
		t.Fatal("Windows-specific declarations were collapsed into a fictional shared surface")
	}
	if err := checkGoAPISurface(ctx, root); err != nil {
		t.Fatal(err)
	}
}

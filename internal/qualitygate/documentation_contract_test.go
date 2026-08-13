package main

import "testing"

func TestDocumentationContractIsCurrent(t *testing.T) {
	t.Parallel()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := readCanonicalJSON[goAPICompatibility](root, goAPICompatibilityPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkDocumentationContract(root, manifest.PublicPackages); err != nil {
		t.Fatal(err)
	}
}

func TestRequiredDocumentFailsClosed(t *testing.T) {
	t.Parallel()
	if err := validateRequiredDocument("test.md", "alpha\nbeta\n", []string{"alpha", "beta"}); err != nil {
		t.Fatal(err)
	}
	if err := validateRequiredDocument("test.md", "alpha\n", []string{"alpha", "beta"}); err == nil {
		t.Fatal("missing documentation contract succeeded")
	}
}

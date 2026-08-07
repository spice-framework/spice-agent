package main

import (
	"os"
	"strings"
	"testing"
)

func TestRunReportsInvalidBootstrapOnlyOnStderr(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "fixture-input-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	output, err := os.CreateTemp(t.TempDir(), "fixture-output-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.Close() }()
	errorOutput, err := os.CreateTemp(t.TempDir(), "fixture-error-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = errorOutput.Close() }()
	if code := run(input, output, errorOutput); code != 1 {
		t.Fatalf("run code = %d", code)
	}
	if _, err = output.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	stdout, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(stdout) != 0 {
		t.Fatalf("stdout = %q", stdout)
	}
	if _, err = errorOutput.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	stderr, err := os.ReadFile(errorOutput.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stderr), "plugin bootstrap must contain exactly one line") {
		t.Fatalf("stderr = %q", stderr)
	}
	closedError, err := os.CreateTemp(t.TempDir(), "fixture-closed-error-*")
	if err != nil {
		t.Fatal(err)
	}
	if err = closedError.Close(); err != nil {
		t.Fatal(err)
	}
	if code := run(input, output, closedError); code != 2 {
		t.Fatalf("run with closed stderr code = %d", code)
	}
}

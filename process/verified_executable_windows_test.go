//go:build windows

package process_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	agentprocess "github.com/spice-framework/spice-agent/process"
)

func TestExecutableLeaseRejectsWindowsReparsePoint(t *testing.T) {
	t.Parallel()
	target, digest := writeVerifiedTestExecutable(t, []byte("target"))
	link := filepath.Join(t.TempDir(), "verified-link.exe")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("creating a Windows symlink requires optional privilege: %v", err)
	}
	if lease, err := agentprocess.VerifyExecutable(t.Context(), link, digest); err == nil || lease != nil {
		t.Fatalf("reparse verification = %v, %v", lease, err)
	}
}

func TestWindowsLeaseBlocksMutationAndSubstitutionBeforeLaunch(t *testing.T) {
	goodMarker := filepath.Join(t.TempDir(), "good.marker")
	evilMarker := filepath.Join(t.TempDir(), "evil.marker")
	root := t.TempDir()
	good := compileWindowsMarkerExecutable(t, root, "good", goodMarker)
	evil := compileWindowsMarkerExecutable(t, root, "evil", evilMarker)
	digest := executableDigestWindows(t, good)
	lease, err := agentprocess.VerifyExecutable(t.Context(), good, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	if err = os.WriteFile(good, []byte("mutated"), 0o700); err == nil {
		t.Fatal("held executable lease permitted in-place mutation")
	}
	if err = os.Rename(evil, good); err == nil {
		t.Fatal("held executable lease permitted pathname substitution")
	}
	spec := verifiedTestSpec(t, good, root, nil)
	if err = startWindowsLeased(t.Context(), lease, spec); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(goodMarker); err != nil {
		t.Fatalf("verified image did not execute: %v", err)
	}
	if _, err = os.Stat(evilMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("substituted image executed: %v", err)
	}
}

func startWindowsLeased(
	ctx context.Context,
	lease *agentprocess.ExecutableLease,
	spec agentprocess.Spec,
) error {
	if err := lease.ValidateSpec(spec); err != nil {
		return err
	}
	// A production Windows launcher creates the process suspended and performs
	// this recheck before ResumeThread. The held non-sharing handle ensures the
	// pathname cannot change between these two operations.
	if err := lease.Recheck(ctx); err != nil {
		return err
	}
	command := exec.Command(spec.Executable(), spec.Arguments()...) // #nosec G204 -- the non-sharing verified lease pins this exact path.
	command.Dir = spec.WorkingDirectory()
	command.Env = spec.Environment()
	command.Stdin = spec.Stdin()
	command.Stdout = spec.Stdout()
	command.Stderr = spec.Stderr()
	return command.Run()
}

func compileWindowsMarkerExecutable(t *testing.T, root, name, marker string) string {
	t.Helper()
	source := filepath.Join(root, name+".go")
	content := fmt.Sprintf("package main\nimport \"os\"\nfunc main(){if err:=os.WriteFile(%q, []byte(%q), 0600); err!=nil { panic(err) }}\n", marker, name)
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, name+".exe")
	command := exec.Command("go", "build", "-trimpath", "-o", executable, source) // #nosec G204 -- fixed Go command builds a generated test helper.
	command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s helper: %v\n%s", name, err, output)
	}
	return executable
}

func executableDigestWindows(t *testing.T, path string) agentprocess.SHA256 {
	t.Helper()
	content, err := os.ReadFile(path) // #nosec G304 -- test owns the exact helper path.
	if err != nil {
		t.Fatal(err)
	}
	_, digest := writeVerifiedTestExecutable(t, content)
	return digest
}

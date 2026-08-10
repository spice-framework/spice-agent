//go:build unix

package process_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	agentprocess "github.com/spice-framework/spice-agent/process"
)

func TestExecutableLeaseRejectsUnixSymlinkAndPermissionDrift(t *testing.T) {
	t.Parallel()
	target, digest := writeVerifiedTestExecutable(t, []byte("target"))
	link := filepath.Join(t.TempDir(), "verified-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if lease, err := agentprocess.VerifyExecutable(t.Context(), link, digest); err == nil || lease != nil {
		t.Fatalf("symlink verification = %v, %v", lease, err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatal(err)
	}
	if lease, err := agentprocess.VerifyExecutable(t.Context(), target, digest); err == nil || lease != nil {
		t.Fatalf("non-executable verification = %v, %v", lease, err)
	}
}

func TestExecutableLeaseDetectsUnixMutationAndReplacement(t *testing.T) {
	t.Parallel()
	path, digest := writeVerifiedTestExecutable(t, []byte("original"))
	lease, err := agentprocess.VerifyExecutable(t.Context(), path, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	if err = os.WriteFile(path, []byte("mutated"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = lease.Recheck(t.Context()); err == nil || strings.Contains(err.Error(), path) {
		t.Fatalf("mutation recheck = %v", err)
	}

	secondPath, secondDigest := writeVerifiedTestExecutable(t, []byte("same-content"))
	secondLease, err := agentprocess.VerifyExecutable(t.Context(), secondPath, secondDigest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = secondLease.Close() }()
	replacement := filepath.Join(filepath.Dir(secondPath), "replacement")
	if err = os.WriteFile(replacement, []byte("same-content"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(replacement, secondPath); err != nil {
		t.Fatal(err)
	}
	if err = secondLease.Recheck(t.Context()); err == nil {
		t.Fatal("same-content identity replacement was accepted")
	}
}

func TestUnixDescriptorLaunchNeverExecutesSubstitutedPath(t *testing.T) {
	goodMarker := filepath.Join(t.TempDir(), "good.marker")
	evilMarker := filepath.Join(t.TempDir(), "evil.marker")
	root := t.TempDir()
	good := compileMarkerExecutable(t, root, "good", goodMarker)
	evil := compileMarkerExecutable(t, root, "evil", evilMarker)
	digest := executableDigest(t, good)
	lease, err := agentprocess.VerifyExecutable(t.Context(), good, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	if err = os.Rename(evil, good); err != nil {
		t.Fatal(err)
	}
	spec := verifiedTestSpec(t, good, root, nil)
	if err = startDescriptorBacked(t.Context(), lease, spec); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(goodMarker); err != nil {
		t.Fatalf("verified image did not execute: %v", err)
	}
	if _, err = os.Stat(evilMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("substituted image executed: %v", err)
	}
	if err = lease.Recheck(t.Context()); err == nil {
		t.Fatal("defense-in-depth recheck accepted substituted path")
	}
}

func startDescriptorBacked(
	ctx context.Context,
	lease *agentprocess.ExecutableLease,
	spec agentprocess.Spec,
) error {
	if err := lease.ValidateSpec(spec); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		materialized, err := lease.MaterializeForLaunch(ctx)
		if err != nil {
			return err
		}
		defer materialized.Close()                                                    //nolint:errcheck // The launch result remains authoritative.
		command := exec.CommandContext(ctx, materialized.Path(), spec.Arguments()...) // #nosec G204 -- private digest-reverified path.
		command.Args[0] = spec.Executable()
		command.Dir = spec.WorkingDirectory()
		command.Env = spec.Environment()
		command.Stdin = spec.Stdin()
		command.Stdout = spec.Stdout()
		command.Stderr = spec.Stderr()
		if err = materialized.Recheck(ctx); err != nil {
			return err
		}
		return command.Run()
	}
	file, err := lease.DuplicateForLaunch()
	if err != nil {
		return err
	}
	defer file.Close()                                                          //nolint:errcheck // The launch result remains authoritative.
	command := exec.CommandContext(ctx, "/proc/self/fd/3", spec.Arguments()...) // #nosec G204 -- fixed descriptor path selects the verified file object.
	command.Args[0] = spec.Executable()
	command.ExtraFiles = []*os.File{file}
	command.Dir = spec.WorkingDirectory()
	command.Env = spec.Environment()
	command.Stdin = spec.Stdin()
	command.Stdout = spec.Stdout()
	command.Stderr = spec.Stderr()
	return command.Run()
}

func compileMarkerExecutable(t *testing.T, root, name, marker string) string {
	t.Helper()
	source := filepath.Join(root, name+".go")
	content := fmt.Sprintf("package main\nimport \"os\"\nfunc main(){if err:=os.WriteFile(%q, []byte(%q), 0600); err!=nil { panic(err) }}\n", marker, name)
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, name)
	command := exec.Command("go", "build", "-trimpath", "-o", executable, source) // #nosec G204 -- fixed Go command builds a generated test helper.
	command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s helper: %v\n%s", name, err, output)
	}
	return executable
}

func executableDigest(t *testing.T, path string) agentprocess.SHA256 {
	t.Helper()
	content, err := os.ReadFile(path) // #nosec G304 -- test owns the exact helper path.
	if err != nil {
		t.Fatal(err)
	}
	_, digest := writeVerifiedTestExecutable(t, content)
	return digest
}

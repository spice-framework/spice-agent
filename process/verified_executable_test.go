package process_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	agentprocess "github.com/spice-framework/spice-agent/process"
	"github.com/spice-framework/spice-agent/tool"
)

func TestExecutableLeaseLifecycleAndSpecBinding(t *testing.T) {
	t.Parallel()
	path, digest := writeVerifiedTestExecutable(t, []byte("verified executable content"))
	lease, err := agentprocess.VerifyExecutable(t.Context(), path, digest)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Path() != path || lease.Digest() != digest {
		t.Fatal("lease did not preserve immutable executable identity")
	}
	spec := verifiedTestSpec(t, path, filepath.Dir(path), nil)
	if err = lease.ValidateSpec(spec); err != nil {
		t.Fatal(err)
	}
	other := verifiedTestSpec(t, filepath.Join(filepath.Dir(path), "other"), filepath.Dir(path), nil)
	if err = lease.ValidateSpec(other); err == nil {
		t.Fatal("mismatched specification was accepted")
	}
	duplicate, err := lease.DuplicateForLaunch()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = duplicate.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(duplicate)
	if closeErr := duplicate.Close(); err != nil || closeErr != nil || string(content) != "verified executable content" {
		t.Fatalf("duplicate content = %q, read = %v, close = %v", content, err, closeErr)
	}
	if err = lease.Recheck(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err = lease.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	if err = lease.Recheck(t.Context()); err == nil {
		t.Fatal("closed lease recheck succeeded")
	}
	if duplicate, err = lease.DuplicateForLaunch(); err == nil || duplicate != nil {
		t.Fatalf("closed duplicate = %v, %v", duplicate, err)
	}
	if err = lease.ValidateSpec(spec); err == nil {
		t.Fatal("closed lease accepted a specification")
	}
}

func TestExecutableVerificationRejectsInvalidInputsAndCancellation(t *testing.T) {
	t.Parallel()
	path, digest := writeVerifiedTestExecutable(t, []byte("private executable content"))
	wrong, err := agentprocess.ParseSHA256(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	privateCause := errors.New("private cancellation cause")
	cancel(privateCause)
	tests := []struct {
		name    string
		context func() context.Context
		path    string
		digest  agentprocess.SHA256
	}{
		{"nil context", func() context.Context { return nil }, path, digest},
		{"relative path", context.Background, "relative", digest},
		{"zero digest", context.Background, path, agentprocess.SHA256{}},
		{"wrong digest", context.Background, path, wrong},
		{"canceled", func() context.Context { return ctx }, path, digest},
		{"directory", context.Background, filepath.Dir(path), wrong},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lease, verifyErr := agentprocess.VerifyExecutable(test.context(), test.path, test.digest)
			var failure *agentprocess.VerificationError
			if lease != nil || !errors.As(verifyErr, &failure) {
				t.Fatalf("verification = %v, %T %v", lease, verifyErr, verifyErr)
			}
			for _, private := range []string{path, wrong.String(), "private", privateCause.Error()} {
				if strings.Contains(verifyErr.Error(), private) {
					t.Fatalf("verification error exposed %q: %v", private, verifyErr)
				}
			}
			if test.name == "canceled" && !errors.Is(verifyErr, privateCause) {
				t.Fatalf("cancellation cause was not preserved: %v", verifyErr)
			}
		})
	}
}

func TestExecutableVerificationFailuresAndLeaseFormattingAreSecretSafe(t *testing.T) {
	t.Parallel()
	path, digest := writeVerifiedTestExecutable(t, []byte("secret-canary-executable"))
	lease, err := agentprocess.VerifyExecutable(t.Context(), path, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	for _, rendered := range []string{
		fmt.Sprint(lease), fmt.Sprintf("%#v", lease), fmt.Sprintf("%+v", lease), lease.LogValue().String(),
	} {
		if !strings.Contains(rendered, "[REDACTED]") || strings.Contains(rendered, path) ||
			strings.Contains(rendered, digest.String()) {
			t.Fatalf("unsafe lease formatting = %q", rendered)
		}
	}
	encoded, err := json.Marshal(lease)
	if err != nil || strings.Contains(string(encoded), path) || strings.Contains(string(encoded), digest.String()) {
		t.Fatalf("unsafe lease JSON = %q, %v", encoded, err)
	}
	var absent *agentprocess.ExecutableLease
	if absent.Path() != "" || absent.Digest() != (agentprocess.SHA256{}) || absent.Close() != nil ||
		absent.LogValue().Kind() != slog.KindString {
		t.Fatal("nil lease boundary was unsafe")
	}
	if err = absent.Recheck(t.Context()); err == nil {
		t.Fatal("nil lease recheck succeeded")
	}
	if file, duplicateErr := absent.DuplicateForLaunch(); duplicateErr == nil || file != nil {
		t.Fatalf("nil lease duplicate = %v, %v", file, duplicateErr)
	}
	var failure *agentprocess.VerificationError
	if failure.Operation() != "" || !strings.Contains(failure.Error(), "failed") || failure.Unwrap() != nil {
		t.Fatal("nil verification failure was unsafe")
	}
}

func TestVerifiedLauncherFunctionPreservesArguments(t *testing.T) {
	t.Parallel()
	path, digest := writeVerifiedTestExecutable(t, []byte("verified"))
	lease, err := agentprocess.VerifyExecutable(t.Context(), path, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	spec := verifiedTestSpec(t, path, filepath.Dir(path), []string{"one", "two"})
	called := false
	launcher := agentprocess.VerifiedLauncherFunc(func(
		ctx context.Context,
		received *agentprocess.ExecutableLease,
		receivedSpec agentprocess.Spec,
	) (agentprocess.Process, error) {
		called = true
		if ctx != t.Context() || received != lease || receivedSpec.Arguments()[1] != "two" {
			t.Fatal("verified launcher changed its immutable inputs")
		}
		return nil, errors.New("test launch")
	})
	if owned, launchErr := launcher.StartVerified(t.Context(), lease, spec); owned != nil || launchErr == nil || !called {
		t.Fatalf("verified launch = %v, %v, called=%t", owned, launchErr, called)
	}
}

func TestMaterializedExecutableLifecycleAndFormatting(t *testing.T) {
	t.Parallel()
	path, digest := writeVerifiedTestExecutable(t, []byte("materialized executable content"))
	lease, err := agentprocess.VerifyExecutable(t.Context(), path, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	materialized, err := lease.MaterializeForLaunch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	materializedPath := materialized.Path()
	materializedDirectory := filepath.Dir(materializedPath)
	if materializedPath == path || !filepath.IsAbs(materializedPath) {
		t.Fatalf("materialized path = %q", materializedPath)
	}
	if err = materialized.Recheck(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fileInfo, statErr := os.Stat(materializedPath)
		directoryInfo, directoryErr := os.Stat(materializedDirectory)
		if statErr != nil || directoryErr != nil || fileInfo.Mode().Perm() != 0o500 ||
			directoryInfo.Mode().Perm() != 0o700 {
			t.Fatalf("private modes = %v, %v; errors = %v, %v", fileInfo, directoryInfo, statErr, directoryErr)
		}
	}
	for _, rendered := range []string{
		fmt.Sprint(materialized), fmt.Sprintf("%#v", materialized),
		fmt.Sprintf("%+v", materialized), materialized.LogValue().String(),
	} {
		if !strings.Contains(rendered, "[REDACTED]") || strings.Contains(rendered, materializedPath) {
			t.Fatalf("unsafe materialized formatting = %q", rendered)
		}
	}
	encoded, err := json.Marshal(materialized)
	if err != nil || strings.Contains(string(encoded), materializedPath) {
		t.Fatalf("unsafe materialized JSON = %q, %v", encoded, err)
	}
	if err = materialized.Close(); err != nil {
		t.Fatal(err)
	}
	if err = materialized.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	if materialized.Path() != "" || materialized.Recheck(t.Context()) == nil {
		t.Fatal("closed materialization remained usable")
	}
	if _, err = os.Stat(materializedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("materialized file remains: %v", err)
	}
	if _, err = os.Stat(materializedDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("materialized directory remains: %v", err)
	}
	var absent *agentprocess.MaterializedExecutable
	if absent.Path() != "" || absent.Close() != nil || absent.Recheck(t.Context()) == nil ||
		absent.LogValue().Kind() != slog.KindString {
		t.Fatal("nil materialization boundary was unsafe")
	}
}

func TestMaterializationRejectsCancellationBeforeFilesystemMutation(t *testing.T) {
	t.Parallel()
	path, digest := writeVerifiedTestExecutable(t, []byte("materialization cancellation"))
	lease, err := agentprocess.VerifyExecutable(t.Context(), path, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	ctx, cancel := context.WithCancelCause(context.Background())
	privateCause := errors.New("private materialization cancellation")
	cancel(privateCause)
	materialized, materializeErr := lease.MaterializeForLaunch(ctx)
	failure, ok := errors.AsType[*agentprocess.VerificationError](materializeErr)
	if materialized != nil || !ok || failure.Operation() != agentprocess.VerificationOperationMaterialize ||
		!errors.Is(materializeErr, privateCause) || strings.Contains(materializeErr.Error(), privateCause.Error()) {
		t.Fatalf("canceled materialization = %v, %T %v", materialized, materializeErr, materializeErr)
	}
}

func TestExecutableLeaseOperationsAreConcurrencySafe(t *testing.T) {
	t.Parallel()
	path, digest := writeVerifiedTestExecutable(t, bytes.Repeat([]byte("lease"), 1024))
	lease, err := agentprocess.VerifyExecutable(t.Context(), path, digest)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index := range 24 {
		workers.Go(func() {
			<-start
			if index%2 == 0 {
				duplicate, duplicateErr := lease.DuplicateForLaunch()
				if duplicate != nil {
					_ = duplicate.Close()
				}
				if duplicateErr != nil {
					if _, ok := errors.AsType[*agentprocess.VerificationError](duplicateErr); !ok {
						t.Errorf("duplicate error = %T %v", duplicateErr, duplicateErr)
					}
				}
				return
			}
			if recheckErr := lease.Recheck(context.Background()); recheckErr != nil {
				if _, ok := errors.AsType[*agentprocess.VerificationError](recheckErr); !ok {
					t.Errorf("recheck error = %T %v", recheckErr, recheckErr)
				}
			}
		})
	}
	workers.Go(func() {
		<-start
		if closeErr := lease.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})
	close(start)
	workers.Wait()
	if err = lease.Close(); err != nil {
		t.Fatalf("final idempotent close: %v", err)
	}
}

func writeVerifiedTestExecutable(t *testing.T, content []byte) (string, agentprocess.SHA256) {
	t.Helper()
	name := "verified-test"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	digest, err := agentprocess.ParseSHA256(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	return path, digest
}

func verifiedTestSpec(t *testing.T, executable, directory string, arguments []string) agentprocess.Spec {
	t.Helper()
	spec, err := agentprocess.NewSpec(agentprocess.Config{
		Executable: executable, Arguments: arguments, WorkingDirectory: directory,
		Environment: []string{"EXACT=value"}, Stdin: bytes.NewReader(nil),
		Stdout: io.Discard, Stderr: io.Discard,
		Capabilities: []tool.Capability{tool.CapabilityProcessExecute},
	})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

package gitworkflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

func TestConstructorsAndStoresFailClosed(t *testing.T) {
	t.Parallel()
	if _, err := NewInspectTool(nil); err == nil {
		t.Fatal("nil inspect backend was accepted")
	}
	if _, err := NewCommitStagedTool(nil, NewAuthorityStore()); err == nil {
		t.Fatal("nil commit backend was accepted")
	}
	if _, err := NewCommitStagedTool(&fakeBackend{}, nil); err == nil {
		t.Fatal("nil authority store was accepted")
	}
	if _, err := NewCommitGuard(nil, NewAuthorityStore()); err == nil {
		t.Fatal("nil guard backend was accepted")
	}
	if _, err := NewCommitGuard(&fakeBackend{}, nil); err == nil {
		t.Fatal("nil guard store was accepted")
	}
	store := NewAuthorityStore()
	store.Close()
	store.Close()
	call := mustCall(t, "call-closed-store", CommitStagedToolName, `{"message":"closed"}`)
	if err := store.authorize(call, testFingerprint, testFingerprint); !errors.Is(err, ErrAuthorizationFailed) {
		t.Fatalf("closed store authorize = %v", err)
	}
	if _, err := store.consume(call); !errors.Is(err, ErrAuthorizationRequired) {
		t.Fatalf("closed store consume = %v", err)
	}
	(*AuthorityStore)(nil).Close()
	(*AuthorityStore)(nil).revoke(call.ID())
}

func TestToolExecutionBoundaries(t *testing.T) {
	t.Parallel()
	call := mustCall(t, "call-boundary", InspectToolName, `{}`)
	result, err := (*InspectTool)(nil).Execute(t.Context(), call, nil)
	if !result.IsZero() || err == nil {
		t.Fatalf("nil inspect = %#v, %v", result, err)
	}
	result, err = (*CommitStagedTool)(nil).Execute(
		t.Context(), mustCall(t, "call-nil-commit", CommitStagedToolName, `{"message":"x"}`), nil,
	)
	if !result.IsZero() || err == nil {
		t.Fatalf("nil commit = %#v, %v", result, err)
	}
	backend := &fakeBackend{err: errors.New("backend secret")}
	inspect, commit, _ := mustGitTools(t, backend, NewAuthorityStore())
	result, err = inspect.Execute(t.Context(), call, nil)
	if !result.IsZero() || err == nil {
		t.Fatalf("failed inspect = %#v, %v", result, err)
	}
	result, err = inspect.Execute(t.Context(), mustCall(t, "call-wrong", CommitStagedToolName, `{}`), nil)
	if !result.IsZero() || err == nil {
		t.Fatalf("wrong inspect call = %#v, %v", result, err)
	}
	result, err = commit.Execute(t.Context(), mustCall(t, "call-bad-args", CommitStagedToolName, `{}`), nil)
	if !result.IsZero() || err == nil {
		t.Fatalf("bad commit = %#v, %v", result, err)
	}
	if message := (fixedOperationFailure{cause: errors.New("secret")}).Error(); message != "git workflow operation failed" {
		t.Fatalf("fixed failure = %q", message)
	}
	var failure *operationFailure
	if failure.Error() != "git workflow operation failed" || failure.Unwrap() != nil {
		t.Fatal("nil operation failure was not safe")
	}
}

func TestGuardRejectsInvalidAndPassesUnrelatedDispatches(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{staged: testFingerprint}
	_, commit, guard := mustGitTools(t, backend, NewAuthorityStore())
	call := mustCall(t, "call-guard-boundary", CommitStagedToolName, `{"message":"x"}`)
	if _, err := guard.Guard(t.Context(), stage.ToolDispatchScope{}, commit.Definition(), call, func() (tool.Result, error) {
		return tool.Result{}, nil
	}); !errors.Is(err, ErrAuthorizationFailed) {
		t.Fatalf("invalid scope = %v", err)
	}
	unknown, err := tool.NewDefinition(
		"git.unknown", "unknown", []byte(`{"type":"object"}`),
		tool.EffectMutating, tool.ReplayUnsafe, tool.CapabilityProcessExecute,
	)
	if err != nil {
		t.Fatal(err)
	}
	unknownCall := mustCall(t, "call-unknown", "git.unknown", `{}`)
	continued := false
	result, err := guard.Guard(
		t.Context(), mustScope(t, interaction.UnavailableRequester{}), unknown, unknownCall,
		func() (tool.Result, error) {
			continued = true
			return tool.NewResult(unknownCall.ID(), []byte(`{"unchanged":true}`))
		},
	)
	if err != nil || !continued || result.CallID() != unknownCall.ID() {
		t.Fatalf("unrelated dispatch = %#v, %v, continued=%t", result, err, continued)
	}
	if _, err = guard.Guard(t.Context(), mustScope(t, interaction.UnavailableRequester{}), commit.Definition(), call, nil); !errors.Is(err, ErrAuthorizationFailed) {
		t.Fatalf("nil continuation = %v", err)
	}
}

func TestAuthorityAndGuardFailureBoundaries(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{staged: testFingerprint, err: errors.New("backend secret")}
	_, commit, guard := mustGitTools(t, backend, NewAuthorityStore())
	call := mustCall(t, "call-authority-boundary", CommitStagedToolName, `{"message":"x"}`)
	if _, err := guard.Guard(
		t.Context(), mustScope(t, approvingRequester{}), commit.Definition(), call,
		func() (tool.Result, error) { return tool.Result{}, nil },
	); !errors.Is(err, ErrAuthorizationFailed) {
		t.Fatalf("backend failure = %v", err)
	}
	if _, err := AuthorityToken(stage.ToolDispatchScope{}, commit.Definition(), call, testFingerprint); err == nil {
		t.Fatal("invalid scope token was accepted")
	}
	if _, err := AuthorityToken(mustScope(t, interaction.UnavailableRequester{}), tool.Definition{}, call, testFingerprint); err == nil {
		t.Fatal("invalid definition token was accepted")
	}
	if _, err := AuthorityToken(mustScope(t, interaction.UnavailableRequester{}), commit.Definition(), call, "invalid"); err == nil {
		t.Fatal("invalid staged digest token was accepted")
	}
	if validDigest("sha256:ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef0123456789") {
		t.Fatal("noncanonical digest was accepted")
	}
}

func TestRunnerConstructionAndInputBoundaries(t *testing.T) {
	t.Parallel()
	executable := copySelf(t)
	digest, err := FileSHA256(executable)
	if err != nil {
		t.Fatal(err)
	}
	repository := t.TempDir()
	valid := RunnerConfig{Executable: executable, SHA256: digest, Repository: repository, Launcher: &countingLauncher{}}
	tests := []RunnerConfig{
		{Executable: executable, SHA256: digest, Repository: repository},
		{Executable: "relative", SHA256: digest, Repository: repository, Launcher: &countingLauncher{}},
		{Executable: executable, SHA256: "invalid", Repository: repository, Launcher: &countingLauncher{}},
		{Executable: executable, SHA256: digest, Repository: "relative", Launcher: &countingLauncher{}},
		{Executable: executable, SHA256: digest, Repository: executable, Launcher: &countingLauncher{}},
		{Executable: executable, SHA256: digest, Repository: repository, Launcher: &countingLauncher{}, MaximumOutputBytes: 1},
		{Executable: executable, SHA256: digest, Repository: repository, Launcher: &countingLauncher{}, Environment: []string{"INVALID"}},
		{Executable: executable, SHA256: digest, Repository: repository, Launcher: &countingLauncher{}, Environment: []string{"A=1", "a=2"}},
	}
	for index, config := range tests {
		if runner, runnerErr := NewRunner(config); runnerErr == nil {
			_ = runner.Close(context.Background())
			t.Fatalf("invalid runner %d was accepted", index)
		}
	}
	runner, err := NewRunner(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.CommitStaged(t.Context(), " bad ", testFingerprint); err == nil {
		t.Fatal("invalid commit input was accepted")
	}
	if _, err = runner.StagedDigest(nil); err == nil {
		t.Fatal("nil operation context was accepted")
	}
	if err = runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Inspect(t.Context()); err == nil {
		t.Fatal("closed runner admitted inspect")
	}
	if err = (*Runner)(nil).Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = runner.Close(nil); err == nil {
		t.Fatal("nil close context was accepted")
	}
	if _, err = FileSHA256("relative"); err == nil {
		t.Fatal("relative digest path was accepted")
	}
	if _, err = FileSHA256(repository); err == nil {
		t.Fatal("directory digest path was accepted")
	}
	if _, err = FileSHA256(filepath.Join(repository, "missing")); err == nil {
		t.Fatal("missing digest path was accepted")
	}
}

func TestSecurityDirectoryRejectsSymlinkWhenSupported(t *testing.T) {
	if os.Getenv("CI") == "" && filepath.Separator == '\\' {
		// Unprivileged local Windows installations commonly cannot create
		// symlinks; the identity/content cases cover the same fail-closed path.
		t.Skip("Windows developer mode is not guaranteed")
	}
	// The no-follow check itself is exercised by the platform where creation is
	// available; failure to create is a host capability, not a product failure.
	executable := copySelf(t)
	digest, err := FileSHA256(executable)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(RunnerConfig{
		Executable: executable, SHA256: digest, Repository: t.TempDir(), Launcher: &countingLauncher{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRunner(t, runner) })
	link := runner.hooksDirectory + "-link"
	if err = os.Symlink(runner.hooksDirectory, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err = os.Remove(runner.hooksDirectory); err != nil {
		t.Skipf("held hooks identity prevents replacement: %v", err)
	}
	if err = os.Rename(link, runner.hooksDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err = runner.StagedDigest(t.Context()); err == nil {
		t.Fatal("hooks symlink was accepted")
	}
}

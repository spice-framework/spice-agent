package gitworkflow

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/experiments/git-workflow/internal/nativeprocess"
	agentprocess "github.com/spice-framework/spice-agent/process"
)

func TestRunnerInspectsAndCommitsOnlyStagedIndex(t *testing.T) {
	git := findGit(t)
	repository := initializeRepository(t, git)
	stagedPath := filepath.Join(repository, "record.txt")
	if err := os.WriteFile(stagedPath, []byte("staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, repository, "add", "record.txt")
	if err := os.WriteFile(stagedPath, []byte("unstaged\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	hookMarker := filepath.Join(repository, "hook-ran")
	fsmonitorMarker := filepath.Join(repository, "fsmonitor-ran")
	hooks := filepath.Join(repository, ".git", "hooks")
	if err := os.WriteFile(filepath.Join(hooks, "post-commit"), []byte("#!/bin/sh\nprintf hook > \""+filepath.ToSlash(hookMarker)+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, repository, "config", "commit.gpgsign", "true")
	fsmonitor := filepath.Join(repository, "untrusted-fsmonitor")
	if err := os.WriteFile(
		fsmonitor,
		[]byte("#!/bin/sh\nprintf fsmonitor > \""+filepath.ToSlash(fsmonitorMarker)+"\"\nprintf '{}'"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, repository, "config", "core.fsmonitor", filepath.ToSlash(fsmonitor))

	runner := newRealRunner(t, git, repository)
	inspection, err := runner.Inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inspection.Status, "record.txt") || !validDigest(inspection.StagedDigest) {
		t.Fatalf("inspection = %#v", inspection)
	}
	if err = runner.CommitStaged(t.Context(), "staged-only proof", inspection.StagedDigest); err != nil {
		t.Fatal(err)
	}
	committed := runGitOutput(t, git, repository, "show", "HEAD:record.txt")
	if committed != "staged\n" {
		t.Fatalf("committed content = %q", committed)
	}
	working, err := os.ReadFile(stagedPath)
	if err != nil || string(working) != "unstaged\n" {
		t.Fatalf("working content = %q, %v", working, err)
	}
	if _, err = os.Stat(hookMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository hook executed: %v", err)
	}
	if _, err = os.Stat(fsmonitorMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository fsmonitor executed: %v", err)
	}
}

func TestRunnerRejectsStagedIndexChangeBeforeLaunch(t *testing.T) {
	git := findGit(t)
	repository := initializeRepository(t, git)
	path := filepath.Join(repository, "record.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, repository, "add", "record.txt")
	runner := newRealRunner(t, git, repository)
	digest, err := runner.StagedDigest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, repository, "add", "record.txt")
	if err = runner.CommitStaged(t.Context(), "must not commit", digest); err == nil || operationUncertain(err) {
		t.Fatalf("changed staged index = %v, uncertain=%v", err, operationUncertain(err))
	}
	if output := runGitOutputAllowFailure(t, git, repository, "rev-parse", "--verify", "HEAD"); output != "" {
		t.Fatalf("unexpected commit %q", output)
	}
}

func TestRunnerRejectsExecutableSubstitutionBeforeLaunch(t *testing.T) {
	executable := copySelf(t)
	repository := t.TempDir()
	digest, err := FileSHA256(executable)
	if err != nil {
		t.Fatal(err)
	}
	launcher := &countingLauncher{}
	runner, err := NewRunner(RunnerConfig{Executable: executable, SHA256: digest, Repository: repository, Launcher: launcher})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRunner(t, runner) })
	if err = os.WriteFile(executable, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = runner.StagedDigest(t.Context()); err == nil || launcher.calls.Load() != 0 {
		t.Fatalf("substitution = %v, launches=%d", err, launcher.calls.Load())
	}
}

func TestRunnerRejectsExecutableSubstitutionAfterLaunch(t *testing.T) {
	executable := copySelf(t)
	repository := t.TempDir()
	digest, err := FileSHA256(executable)
	if err != nil {
		t.Fatal(err)
	}
	launcher := launcherFunc(func(_ context.Context, spec agentprocess.Spec) (agentprocess.Process, error) {
		_, _ = spec.Stdout().Write([]byte(":100644 100644 a b M\x00record.txt\x00"))
		if writeErr := os.WriteFile(executable, []byte("replacement"), 0o700); writeErr != nil {
			return nil, writeErr
		}
		return successfulProcess(), nil
	})
	runner, err := NewRunner(RunnerConfig{Executable: executable, SHA256: digest, Repository: repository, Launcher: launcher})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRunner(t, runner) })
	if _, err = runner.StagedDigest(t.Context()); err == nil || operationUncertain(err) {
		t.Fatalf("post-launch substitution = %v, uncertain=%v", err, operationUncertain(err))
	}
}

func TestRunnerRejectsSecurityBoundaryMutationBeforeLaunch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Runner) error
	}{
		{name: "repository identity", mutate: func(runner *Runner) error {
			original := runner.repository + "-original"
			if err := os.Rename(runner.repository, original); err != nil {
				return err
			}
			return os.Mkdir(runner.repository, 0o700)
		}},
		{name: "hooks content", mutate: func(runner *Runner) error {
			return os.WriteFile(filepath.Join(runner.hooksDirectory, "pre-commit"), []byte("forbidden"), 0o600)
		}},
		{name: "hooks identity", mutate: func(runner *Runner) error {
			if err := os.Remove(runner.hooksDirectory); err != nil {
				return err
			}
			return os.Mkdir(runner.hooksDirectory, 0o700)
		}},
		{name: "global configuration", mutate: func(runner *Runner) error {
			return os.WriteFile(runner.globalConfig, []byte("[credential]\nhelper=forbidden\n"), 0o600)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			executable := copySelf(t)
			repository := t.TempDir()
			digest, err := FileSHA256(executable)
			if err != nil {
				t.Fatal(err)
			}
			launcher := &countingLauncher{}
			runner, err := NewRunner(RunnerConfig{Executable: executable, SHA256: digest, Repository: repository, Launcher: launcher})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { closeRunner(t, runner) })
			if mutationErr := test.mutate(runner); mutationErr != nil {
				// A held identity handle may itself make replacement impossible,
				// notably for directories on Windows. That is stronger than the
				// subsequent fail-closed identity check.
				return
			}
			if _, err = runner.StagedDigest(t.Context()); err == nil || launcher.calls.Load() != 0 {
				t.Fatalf("security mutation = %v, launches=%d", err, launcher.calls.Load())
			}
		})
	}
}

func TestRunnerRejectsGitEnvironmentOverrides(t *testing.T) {
	t.Parallel()
	executable := copySelf(t)
	digest, err := FileSHA256(executable)
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range []string{"GIT_DIR=elsewhere", "git_work_tree=elsewhere", "GIT_SSH=forbidden"} {
		if _, err = NewRunner(RunnerConfig{
			Executable: executable, SHA256: digest, Repository: t.TempDir(),
			Environment: []string{environment}, Launcher: &countingLauncher{},
		}); err == nil {
			t.Fatalf("environment %q was accepted", environment)
		}
	}
}

func TestRunnerBoundsProcessOutput(t *testing.T) {
	t.Parallel()
	executable := copySelf(t)
	digest, err := FileSHA256(executable)
	if err != nil {
		t.Fatal(err)
	}
	launcher := launcherFunc(func(_ context.Context, spec agentprocess.Spec) (agentprocess.Process, error) {
		_, _ = spec.Stdout().Write(make([]byte, 1025))
		return successfulProcess(), nil
	})
	runner, err := NewRunner(RunnerConfig{
		Executable: executable, SHA256: digest, Repository: t.TempDir(),
		MaximumOutputBytes: 1024, Launcher: launcher,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRunner(t, runner) })
	if _, err = runner.StagedDigest(t.Context()); err == nil {
		t.Fatal("oversized output was accepted")
	}
}

func TestRunnerClassifiesPostLaunchCommitFailureUncertain(t *testing.T) {
	executable := copySelf(t)
	repository := t.TempDir()
	digest, err := FileSHA256(executable)
	if err != nil {
		t.Fatal(err)
	}
	launcher := launcherFunc(func(_ context.Context, spec agentprocess.Spec) (agentprocess.Process, error) {
		_, _ = spec.Stdout().Write([]byte(":100644 100644 a b M\x00record.txt\x00"))
		return successfulProcess(), nil
	})
	runner, err := NewRunner(RunnerConfig{Executable: executable, SHA256: digest, Repository: repository, Launcher: launcher})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRunner(t, runner) })
	digest, err = runner.StagedDigest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var launches atomic.Int32
	runner.launcher = launcherFunc(func(_ context.Context, spec agentprocess.Spec) (agentprocess.Process, error) {
		if launches.Add(1) == 1 {
			_, _ = spec.Stdout().Write([]byte(":100644 100644 a b M\x00record.txt\x00"))
			return successfulProcess(), nil
		}
		return successfulProcess(), errors.New("launch boundary failed")
	})
	err = runner.CommitStaged(t.Context(), "uncertain", digest)
	if err == nil || !operationUncertain(err) {
		t.Fatalf("commit error = %v", err)
	}
}

func TestRunnerCloseWaitsForActiveCallAndRejectsAdmission(t *testing.T) {
	executable := copySelf(t)
	repository := t.TempDir()
	digest, err := FileSHA256(executable)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var signalStarted sync.Once
	launcher := launcherFunc(func(context.Context, agentprocess.Spec) (agentprocess.Process, error) {
		signalStarted.Do(func() { close(started) })
		return blockingProcess(release), nil
	})
	runner, err := NewRunner(RunnerConfig{Executable: executable, SHA256: digest, Repository: repository, Launcher: launcher})
	if err != nil {
		t.Fatal(err)
	}
	callDone := make(chan error, 1)
	go func() { _, err := runner.Inspect(context.Background()); callDone <- err }()
	<-started
	closeDone := make(chan error, 1)
	go func() { closeDone <- runner.Close(context.Background()) }()
	time.Sleep(20 * time.Millisecond)
	if _, err = runner.Inspect(t.Context()); err == nil {
		t.Fatal("closed runner admitted a call")
	}
	close(release)
	if err = <-callDone; err != nil {
		t.Fatal(err)
	}
	if err = <-closeDone; err != nil {
		t.Fatal(err)
	}
}

func newRealRunner(t *testing.T, git, repository string) *Runner {
	t.Helper()
	digest, err := FileSHA256(git)
	if err != nil {
		t.Fatal(err)
	}
	environment := []string{}
	if runtime.GOOS == "windows" {
		if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
			environment = append(environment, "SystemRoot="+systemRoot)
		}
	}
	runner, err := NewRunner(RunnerConfig{
		Executable: git, SHA256: digest, Repository: repository,
		Environment: environment, Launcher: nativeprocess.NewLauncher(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRunner(t, runner) })
	return runner
}

func closeRunner(t testing.TB, runner *Runner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runner.Close(ctx); err != nil {
		t.Error(err)
	}
}

func findGit(t testing.TB) string {
	t.Helper()
	path, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git is not installed")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Clean(path)
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = filepath.Clean(resolved)
	}
	return path
}

func initializeRepository(t *testing.T, git string) string {
	t.Helper()
	repository := t.TempDir()
	runGit(t, git, repository, "init", "--quiet")
	runGit(t, git, repository, "config", "user.name", "Spice Agent Test")
	runGit(t, git, repository, "config", "user.email", "spice-agent@example.invalid")
	return repository
}

func runGit(t testing.TB, git, repository string, arguments ...string) {
	t.Helper()
	_ = runGitOutput(t, git, repository, arguments...)
}

func runGitOutput(t testing.TB, git, repository string, arguments ...string) string {
	t.Helper()
	// #nosec G204 -- test-only fixed Git executable and arguments.
	command := exec.Command(git, arguments...)
	command.Dir = repository
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return string(output)
}

func runGitOutputAllowFailure(t testing.TB, git, repository string, arguments ...string) string {
	t.Helper()
	// #nosec G204 -- test-only fixed Git executable and arguments.
	command := exec.Command(git, arguments...)
	command.Dir = repository
	output, _ := command.Output()
	return string(output)
}

func copySelf(t *testing.T) string {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	name := "fixture"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(t.TempDir(), name)
	if err = os.WriteFile(target, content, 0o700); err != nil {
		t.Fatal(err)
	}
	return target
}

type launcherFunc func(context.Context, agentprocess.Spec) (agentprocess.Process, error)

func (function launcherFunc) Start(ctx context.Context, spec agentprocess.Spec) (agentprocess.Process, error) {
	return function(ctx, spec)
}

type countingLauncher struct{ calls atomic.Int32 }

func (launcher *countingLauncher) Start(context.Context, agentprocess.Spec) (agentprocess.Process, error) {
	launcher.calls.Add(1)
	return successfulProcess(), nil
}

type fakeProcess struct {
	done    <-chan struct{}
	outcome agentprocess.Outcome
}

func successfulProcess() *fakeProcess {
	done := make(chan struct{})
	close(done)
	outcome, _ := agentprocess.NewExitedOutcome(0)
	return &fakeProcess{done: done, outcome: outcome}
}

func blockingProcess(done <-chan struct{}) *fakeProcess {
	outcome, _ := agentprocess.NewExitedOutcome(0)
	return &fakeProcess{done: done, outcome: outcome}
}
func (process *fakeProcess) Done() <-chan struct{} { return process.done }
func (process *fakeProcess) Result() (agentprocess.Outcome, error) {
	<-process.done
	return process.outcome, nil
}
func (*fakeProcess) RequestStop(context.Context) error { return nil }
func (*fakeProcess) ForceKill(context.Context) error   { return nil }
func (process *fakeProcess) Wait(ctx context.Context) error {
	select {
	case <-process.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

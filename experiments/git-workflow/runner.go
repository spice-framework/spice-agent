package gitworkflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	agentprocess "github.com/spice-framework/spice-agent/process"
	"github.com/spice-framework/spice-agent/tool"
)

const (
	defaultMaximumOutputBytes = 512 << 10
	maximumRunnerOutputBytes  = 1 << 20
	cleanupTimeout            = 5 * time.Second
)

// RunnerConfig is the complete trusted Git process configuration. Executable,
// SHA256, repository, and environment are application-owned values; Runner
// never searches PATH or reads credentials.
type RunnerConfig struct {
	Executable         string
	SHA256             string
	Repository         string
	Environment        []string
	Launcher           agentprocess.Launcher
	MaximumOutputBytes int
}

// Runner executes a closed set of Git commands through an injected public
// process.Launcher. The immutable preview5 module cannot atomically bind a
// verified file handle to child creation, so every invocation performs strict
// pre/post identity and digest checks and promotion remains blocked.
type Runner struct {
	executable           string
	digest               string
	repository           string
	environment          []string
	launcher             agentprocess.Launcher
	maximumOutput        int
	executableIdentity   os.FileInfo
	executableHandle     *os.File
	repositoryIdentity   os.FileInfo
	repositoryHandle     *os.File
	securityDirectory    string
	securityIdentity     os.FileInfo
	securityHandle       *os.File
	hooksDirectory       string
	hooksIdentity        os.FileInfo
	hooksHandle          *os.File
	globalConfig         string
	globalConfigIdentity os.FileInfo
	globalConfigHandle   *os.File

	stateMu    sync.Mutex
	closed     bool
	active     sync.WaitGroup
	cleanupMu  sync.Mutex
	cleaned    bool
	cleanupErr error
	commitMu   sync.Mutex
}

// NewRunner validates the trusted executable and creates a private hookless
// Git configuration owned by this runner.
func NewRunner(config RunnerConfig) (_ *Runner, returnErr error) {
	if config.Launcher == nil {
		return nil, errors.New("git runner launcher is required")
	}
	if err := validateAbsoluteCanonical("git executable", config.Executable); err != nil {
		return nil, err
	}
	if err := validateAbsoluteCanonical("git repository", config.Repository); err != nil {
		return nil, err
	}
	if !validDigest(config.SHA256) {
		return nil, errors.New("git executable SHA-256 is invalid")
	}
	maximum := config.MaximumOutputBytes
	if maximum == 0 {
		maximum = defaultMaximumOutputBytes
	}
	if maximum < 1024 || maximum > maximumRunnerOutputBytes {
		return nil, fmt.Errorf("git output limit must be between 1024 and %d", maximumRunnerOutputBytes)
	}
	identity, err := validateExecutable(config.Executable, config.SHA256, nil)
	if err != nil {
		return nil, err
	}
	repository, err := os.Lstat(config.Repository)
	if err != nil || !repository.IsDir() || repository.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("git repository is unavailable")
	}
	environment, err := validateEnvironment(config.Environment)
	if err != nil {
		return nil, err
	}
	executableHandle, err := os.Open(config.Executable) // #nosec G304 -- exact trusted absolute executable.
	if err != nil {
		return nil, errors.New("open trusted Git executable identity")
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, executableHandle.Close())
		}
	}()
	repositoryHandle, err := os.Open(config.Repository) // #nosec G304 -- exact trusted absolute repository.
	if err != nil {
		return nil, errors.New("open Git repository identity")
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, repositoryHandle.Close())
		}
	}()
	securityDirectory, err := os.MkdirTemp("", "spice-agent-git-*")
	if err != nil {
		return nil, errors.New("create private Git configuration")
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, os.RemoveAll(securityDirectory))
		}
	}()
	if err = os.Chmod(securityDirectory, 0o700); err != nil {
		return nil, errors.New("protect private Git configuration")
	}
	hooksDirectory := filepath.Join(securityDirectory, "hooks")
	if err = os.Mkdir(hooksDirectory, 0o700); err != nil {
		return nil, errors.New("create empty Git hooks directory")
	}
	globalConfig := filepath.Join(securityDirectory, "global.gitconfig")
	if err = os.WriteFile(globalConfig, nil, 0o600); err != nil {
		return nil, errors.New("create empty Git global configuration")
	}
	securityIdentity, err := os.Stat(securityDirectory)
	if err != nil {
		return nil, errors.New("inspect private Git configuration")
	}
	hooksIdentity, err := os.Lstat(hooksDirectory)
	if err != nil {
		return nil, errors.New("inspect private Git hooks directory")
	}
	globalConfigIdentity, err := os.Lstat(globalConfig)
	if err != nil {
		return nil, errors.New("inspect private Git global configuration")
	}
	securityHandle, err := os.Open(securityDirectory) // #nosec G304 -- runner-created private directory.
	if err != nil {
		return nil, errors.New("open private Git configuration identity")
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, securityHandle.Close())
		}
	}()
	hooksHandle, err := os.Open(hooksDirectory) // #nosec G304 -- runner-created empty hooks directory.
	if err != nil {
		return nil, errors.New("open private Git hooks identity")
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, hooksHandle.Close())
		}
	}()
	globalConfigHandle, err := os.Open(globalConfig) // #nosec G304 -- runner-created empty global config.
	if err != nil {
		return nil, errors.New("open private Git global configuration identity")
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, globalConfigHandle.Close())
		}
	}()
	return &Runner{
		executable: config.Executable, digest: config.SHA256,
		repository: config.Repository, environment: environment,
		launcher: config.Launcher, maximumOutput: maximum,
		executableIdentity: identity, executableHandle: executableHandle,
		repositoryIdentity: repository, repositoryHandle: repositoryHandle,
		securityDirectory: securityDirectory,
		securityIdentity:  securityIdentity, securityHandle: securityHandle,
		hooksDirectory: hooksDirectory, hooksIdentity: hooksIdentity, hooksHandle: hooksHandle,
		globalConfig: globalConfig, globalConfigIdentity: globalConfigIdentity,
		globalConfigHandle: globalConfigHandle,
	}, nil
}

// FileSHA256 returns the canonical digest of a regular non-symlink file.
func FileSHA256(path string) (string, error) {
	if err := validateAbsoluteCanonical("digest file", path); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("digest file is not a regular file")
	}
	file, err := os.Open(path) // #nosec G304 -- exact application-configured absolute executable.
	if err != nil {
		return "", errors.New("open digest file")
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", errors.New("hash digest file")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// Inspect returns one bounded porcelain-v2 status snapshot.
func (runner *Runner) Inspect(ctx context.Context) (Inspection, error) {
	if err := runner.begin(); err != nil {
		return Inspection{}, err
	}
	defer runner.active.Done()
	output, _, err := runner.invoke(ctx, []string{
		"status", "--porcelain=v2", "--branch", "--untracked-files=all", "--no-renames",
	}, nil, false, false)
	if err != nil {
		return Inspection{}, err
	}
	staged, err := runner.stagedDigest(ctx)
	if err != nil && !errors.Is(err, errNoStagedChanges) {
		return Inspection{}, err
	}
	return Inspection{Status: string(output), StagedDigest: staged}, nil
}

// StagedDigest returns a deterministic digest of Git's raw staged-index view.
func (runner *Runner) StagedDigest(ctx context.Context) (string, error) {
	if err := runner.begin(); err != nil {
		return "", err
	}
	defer runner.active.Done()
	return runner.stagedDigest(ctx)
}

var errNoStagedChanges = errors.New("git staged index is empty")

func (runner *Runner) stagedDigest(ctx context.Context) (string, error) {
	output, _, err := runner.invoke(ctx, []string{
		"diff", "--cached", "--raw", "--no-abbrev", "--no-renames", "-z",
	}, nil, false, false)
	if err != nil {
		return "", err
	}
	if len(output) == 0 {
		return "", errNoStagedChanges
	}
	digest := sha256.Sum256(output)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// CommitStaged rechecks the authorized staged digest immediately before
// launching the fixed hookless, unsigned, non-interactive commit command.
func (runner *Runner) CommitStaged(ctx context.Context, message, expectedStagedDigest string) error {
	if err := runner.begin(); err != nil {
		return err
	}
	defer runner.active.Done()
	if message == "" || message != strings.TrimSpace(message) || len(message) > maximumCommitMessageBytes ||
		strings.ContainsRune(message, 0) || !validDigest(expectedStagedDigest) {
		return &operationFailure{cause: errors.New("git commit input is invalid")}
	}
	runner.commitMu.Lock()
	defer runner.commitMu.Unlock()
	actual, err := runner.stagedDigest(ctx)
	if err != nil {
		return err
	}
	if actual != expectedStagedDigest {
		return &operationFailure{cause: errors.New("git staged index changed after authorization")}
	}
	_, started, err := runner.invoke(
		ctx, []string{"commit", "--no-gpg-sign", "--no-verify", "--file=-"},
		strings.NewReader(message+"\n"), true, true,
	)
	if err != nil {
		return &operationFailure{uncertain: started, cause: err}
	}
	return nil
}

// Close stops admission, waits for active calls, and removes the private
// hook/config directory. It never owns or changes the repository itself.
func (runner *Runner) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("git runner close context is required")
	}
	if runner == nil {
		return nil
	}
	runner.stateMu.Lock()
	runner.closed = true
	runner.stateMu.Unlock()
	done := make(chan struct{})
	go func() {
		runner.active.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return runner.cleanup()
}

func (runner *Runner) cleanup() error {
	runner.cleanupMu.Lock()
	defer runner.cleanupMu.Unlock()
	if runner.cleaned {
		return runner.cleanupErr
	}
	runner.cleaned = true
	var err error
	for _, handle := range []*os.File{
		runner.globalConfigHandle, runner.hooksHandle, runner.securityHandle,
		runner.repositoryHandle, runner.executableHandle,
	} {
		if handle != nil {
			err = errors.Join(err, handle.Close())
		}
	}
	err = errors.Join(err, os.RemoveAll(runner.securityDirectory))
	if err != nil {
		runner.cleanupErr = errors.New("remove private Git configuration")
	}
	return runner.cleanupErr
}

func (runner *Runner) begin() error {
	if runner == nil || runner.launcher == nil {
		return &operationFailure{cause: errors.New("git runner is unavailable")}
	}
	runner.stateMu.Lock()
	defer runner.stateMu.Unlock()
	if runner.closed {
		return &operationFailure{cause: errors.New("git runner is closed")}
	}
	runner.active.Add(1)
	return nil
}

func (runner *Runner) invoke(
	ctx context.Context,
	commandArguments []string,
	stdin io.Reader,
	mutation bool,
	allowLocks bool,
) ([]byte, bool, error) {
	if ctx == nil {
		return nil, false, &operationFailure{cause: errors.New("git context is required")}
	}
	if err := ctx.Err(); err != nil {
		return nil, false, &operationFailure{cause: err}
	}
	if _, err := validateExecutable(runner.executable, runner.digest, runner.executableIdentity); err != nil {
		return nil, false, &operationFailure{cause: err}
	}
	if err := runner.validateSecurityDirectory(); err != nil {
		return nil, false, &operationFailure{cause: err}
	}
	arguments := runner.gitArguments(commandArguments)
	environment := runner.gitEnvironment(allowLocks)
	stdout, stderr := newBoundedBuffer(runner.maximumOutput), newBoundedBuffer(runner.maximumOutput)
	if stdin == nil {
		stdin = bytes.NewReader(nil)
	}
	capabilities := []tool.Capability{tool.CapabilityFilesystemRead, tool.CapabilityProcessExecute}
	if mutation {
		capabilities = append(capabilities, tool.CapabilityFilesystemWrite)
	}
	spec, err := agentprocess.NewSpec(agentprocess.Config{
		Executable: runner.executable, Arguments: arguments,
		WorkingDirectory: runner.repository, Environment: environment,
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
		Capabilities: capabilities,
	})
	if err != nil {
		return nil, false, &operationFailure{cause: err}
	}
	owned, startErr := runner.launcher.Start(ctx, spec)
	started := owned != nil
	if owned == nil {
		if startErr == nil {
			startErr = errors.New("Git launcher returned no owned process")
		}
		return nil, false, &operationFailure{cause: startErr}
	}
	if startErr != nil {
		cleanupErr := stopAndJoin(owned)
		return nil, started && mutation, &operationFailure{uncertain: started && mutation, cause: errors.Join(startErr, cleanupErr)}
	}
	select {
	case <-owned.Done():
	case <-ctx.Done():
		cleanupErr := stopAndJoin(owned)
		return nil, started && mutation, &operationFailure{uncertain: started && mutation, cause: errors.Join(ctx.Err(), cleanupErr)}
	}
	outcome, resultErr := owned.Result()
	cleanupContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	waitErr := owned.Wait(cleanupContext)
	cancel()
	_, verifyErr := validateExecutable(runner.executable, runner.digest, runner.executableIdentity)
	securityErr := runner.validateSecurityDirectory()
	if outputErr := errors.Join(stdout.Err(), stderr.Err()); outputErr != nil {
		return nil, started && mutation, &operationFailure{uncertain: started && mutation, cause: outputErr}
	}
	if err = errors.Join(resultErr, waitErr, verifyErr, securityErr); err != nil {
		return nil, started && mutation, &operationFailure{uncertain: started && mutation, cause: err}
	}
	if err = outcome.Validate(); err != nil || !outcome.Successful() {
		return nil, started && mutation, &operationFailure{uncertain: started && mutation, cause: errors.New("Git command did not succeed")}
	}
	return stdout.Bytes(), started, nil
}

func stopAndJoin(owned agentprocess.Process) error {
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	stopErr := owned.RequestStop(stopContext)
	cancel()
	select {
	case <-owned.Done():
	case <-time.After(100 * time.Millisecond):
		killContext, killCancel := context.WithTimeout(context.Background(), time.Second)
		stopErr = errors.Join(stopErr, owned.ForceKill(killContext))
		killCancel()
	}
	waitContext, waitCancel := context.WithTimeout(context.Background(), cleanupTimeout)
	waitErr := owned.Wait(waitContext)
	waitCancel()
	return errors.Join(stopErr, waitErr)
}

func (runner *Runner) gitArguments(command []string) []string {
	result := []string{
		"--no-pager",
		"-c", "core.hooksPath=" + runner.hooksDirectory,
		"-c", "commit.gpgSign=false",
		"-c", "credential.helper=",
		"-c", "core.askPass=",
		"-c", "core.fsmonitor=false",
		"-c", "maintenance.auto=false",
		"-c", "gc.auto=0",
		"-c", "sequence.editor=false",
		"-c", "core.pager=cat",
	}
	return append(result, command...)
}

func (runner *Runner) gitEnvironment(allowLocks bool) []string {
	result := slices.Clone(runner.environment)
	result = append(
		result,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+runner.globalConfig,
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=Never",
		"LC_ALL=C",
		"LANG=C",
	)
	if !allowLocks {
		result = append(result, "GIT_OPTIONAL_LOCKS=0")
	}
	slices.Sort(result)
	return result
}

func (runner *Runner) validateSecurityDirectory() error {
	repository, err := os.Lstat(runner.repository)
	if err != nil || !repository.IsDir() || repository.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(repository, runner.repositoryIdentity) {
		return errors.New("Git repository identity changed")
	}
	current, err := os.Lstat(runner.securityDirectory)
	if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(current, runner.securityIdentity) {
		return errors.New("private Git configuration identity changed")
	}
	hooks, err := os.Lstat(runner.hooksDirectory)
	if err != nil || !hooks.IsDir() || hooks.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(hooks, runner.hooksIdentity) {
		return errors.New("private Git hooks directory changed")
	}
	entries, err := os.ReadDir(runner.hooksDirectory)
	if err != nil || len(entries) != 0 {
		return errors.New("private Git hooks directory is not empty")
	}
	config, err := os.Lstat(runner.globalConfig)
	if err != nil || !config.Mode().IsRegular() || config.Mode()&os.ModeSymlink != 0 || config.Size() != 0 ||
		!os.SameFile(config, runner.globalConfigIdentity) {
		return errors.New("private Git global configuration changed")
	}
	return nil
}

func validateExecutable(path, digest string, expected os.FileInfo) (os.FileInfo, error) {
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("trusted Git executable is unavailable")
	}
	if expected != nil && !os.SameFile(current, expected) {
		return nil, errors.New("trusted Git executable identity changed")
	}
	actual, err := FileSHA256(path)
	if err != nil || actual != digest {
		return nil, errors.New("trusted Git executable digest changed")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(current, after) {
		return nil, errors.New("trusted Git executable changed during verification")
	}
	return after, nil
}

func validateAbsoluteCanonical(label, value string) error {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return fmt.Errorf("%s must be an absolute canonical path", label)
	}
	return nil
}

func validateEnvironment(values []string) ([]string, error) {
	reserved := map[string]struct{}{
		"GIT_CONFIG_NOSYSTEM": {}, "GIT_CONFIG_GLOBAL": {}, "GIT_ATTR_NOSYSTEM": {},
		"GIT_TERMINAL_PROMPT": {}, "GIT_ASKPASS": {}, "GCM_INTERACTIVE": {},
		"GIT_OPTIONAL_LOCKS": {}, "LC_ALL": {}, "LANG": {},
	}
	result := slices.Clone(values)
	seen := make(map[string]struct{}, len(result))
	for _, value := range result {
		separator := strings.IndexByte(value, '=')
		if separator <= 0 || strings.ContainsRune(value, 0) {
			return nil, errors.New("git runner environment is invalid")
		}
		name := strings.ToUpper(value[:separator])
		if _, blocked := reserved[name]; blocked || strings.HasPrefix(name, "GIT_") {
			return nil, errors.New("git runner environment overrides a security setting")
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, errors.New("git runner environment contains a duplicate")
		}
		seen[name] = struct{}{}
	}
	slices.Sort(result)
	return result, nil
}

type boundedBuffer struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	maximum int
	err     error
}

func newBoundedBuffer(maximum int) *boundedBuffer { return &boundedBuffer{maximum: maximum} }

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.err != nil {
		return 0, buffer.err
	}
	remaining := buffer.maximum - buffer.buffer.Len()
	if len(value) > remaining {
		if remaining > 0 {
			_, _ = buffer.buffer.Write(value[:remaining])
		}
		buffer.err = errors.New("Git process output exceeded its bound")
		return len(value), buffer.err
	}
	return buffer.buffer.Write(value)
}

func (buffer *boundedBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.buffer.Bytes()...)
}

func (buffer *boundedBuffer) Err() error {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.err
}

var _ Backend = (*Runner)(nil)

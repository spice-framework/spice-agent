//go:build linux || darwin

package nativeprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	agentprocess "github.com/spice-framework/spice-agent/process"
)

const unixPollInterval = 5 * time.Millisecond

type unixProcess struct {
	command *exec.Cmd
	groupID int
	done    chan struct{}
	joined  chan struct{}

	mu         sync.Mutex
	signalMu   sync.Mutex
	outcome    agentprocess.Outcome
	resultErr  error
	cleanupErr error
	stopSent   bool
	killSent   bool
}

func startPlatformProcess(spec agentprocess.Spec) (agentprocess.Process, error) {
	// #nosec G204 -- preview5 Spec validates an absolute executable and the Git
	// runner owns the closed argument set; no shell participates.
	command := exec.Command(spec.Executable(), spec.Arguments()...) //nolint:noctx // Process lifetime is independently owned.
	command.Dir = spec.WorkingDirectory()
	command.Env = spec.Environment()
	command.Stdin = spec.Stdin()
	command.Stdout = spec.Stdout()
	command.Stderr = spec.Stderr()
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return nil, err
	}
	owned := &unixProcess{
		command: command, groupID: command.Process.Pid,
		done: make(chan struct{}), joined: make(chan struct{}),
	}
	go owned.reap()
	return owned, nil
}

func (owned *unixProcess) reap() {
	waitErr := owned.command.Wait()
	outcome, resultErr, cleanupErr := unixOutcome(owned.command.ProcessState, waitErr)
	owned.mu.Lock()
	owned.outcome, owned.resultErr = outcome, resultErr
	owned.cleanupErr = errors.Join(owned.cleanupErr, cleanupErr)
	owned.mu.Unlock()
	close(owned.done)
	for {
		err := syscall.Kill(-owned.groupID, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			owned.mu.Lock()
			owned.cleanupErr = errors.Join(owned.cleanupErr, err)
			owned.mu.Unlock()
			break
		}
		time.Sleep(unixPollInterval)
	}
	close(owned.joined)
}

func unixOutcome(state *os.ProcessState, waitErr error) (agentprocess.Outcome, error, error) {
	if state == nil {
		return agentprocess.Outcome{}, agentprocess.NewFailure(agentprocess.OperationResult, errors.New("root process produced no result")), waitErr
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return agentprocess.NewUnknownOutcome(), nil, nonExitFailure(waitErr)
	}
	if status.Signaled() {
		return agentprocess.NewSignaledOutcome(), nil, nonExitFailure(waitErr)
	}
	outcome, err := agentprocess.NewExitedOutcome(int64(status.ExitStatus()))
	return outcome, err, nonExitFailure(waitErr)
}

func nonExitFailure(err error) error {
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		return nil
	}
	return err
}

func (owned *unixProcess) Done() <-chan struct{} {
	if owned == nil {
		return nil
	}
	return owned.done
}

func (owned *unixProcess) Result() (agentprocess.Outcome, error) {
	if owned == nil || owned.done == nil {
		return agentprocess.Outcome{}, agentprocess.NewFailure(agentprocess.OperationResult, errors.New("process is unavailable"))
	}
	select {
	case <-owned.done:
		owned.mu.Lock()
		defer owned.mu.Unlock()
		return owned.outcome, owned.resultErr
	default:
		return agentprocess.Outcome{}, agentprocess.NewFailure(agentprocess.OperationResult, errors.New("process is running"))
	}
}

func (owned *unixProcess) RequestStop(ctx context.Context) error {
	return owned.signal(ctx, syscall.SIGTERM, agentprocess.OperationRequestStop, false)
}

func (owned *unixProcess) ForceKill(ctx context.Context) error {
	return owned.signal(ctx, syscall.SIGKILL, agentprocess.OperationForceKill, true)
}

func (owned *unixProcess) signal(ctx context.Context, signal syscall.Signal, operation agentprocess.Operation, force bool) error {
	if err := validateContext(ctx, operation); err != nil {
		return err
	}
	if owned == nil {
		return agentprocess.NewFailure(operation, errors.New("process is unavailable"))
	}
	owned.signalMu.Lock()
	defer owned.signalMu.Unlock()
	if (force && owned.killSent) || (!force && owned.stopSent) {
		return nil
	}
	if err := syscall.Kill(-owned.groupID, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return agentprocess.NewFailure(operation, err)
	}
	if force {
		owned.killSent = true
	} else {
		owned.stopSent = true
	}
	return nil
}

func (owned *unixProcess) Wait(ctx context.Context) error {
	if err := validateContext(ctx, agentprocess.OperationWait); err != nil {
		return err
	}
	if owned == nil || owned.joined == nil {
		return agentprocess.NewFailure(agentprocess.OperationWait, errors.New("process is unavailable"))
	}
	select {
	case <-owned.joined:
		owned.mu.Lock()
		defer owned.mu.Unlock()
		return containmentFailure(owned.cleanupErr)
	case <-ctx.Done():
		return agentprocess.NewFailure(agentprocess.OperationWait, ctx.Err())
	}
}

var _ agentprocess.Process = (*unixProcess)(nil)

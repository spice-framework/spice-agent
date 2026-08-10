//go:build windows

package nativeprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	agentprocess "github.com/spice-framework/spice-agent/process"
	"golang.org/x/sys/windows"
)

const (
	windowsStopExitCode               = 0x53414754 // "SAGT"
	windowsKillExitCode               = windowsStopExitCode + 1
	windowsCompletionPollMilliseconds = 100
	windowsCompletionPolls            = 50
	windowsActiveProcessZero          = 4
	windowsCompletionKey              = 0x53414754
)

type windowsProcess struct {
	command        *exec.Cmd
	job            windows.Handle
	completionPort windows.Handle
	assigned       bool
	done           chan struct{}
	joined         chan struct{}

	mu         sync.Mutex
	signalMu   sync.Mutex
	outcome    agentprocess.Outcome
	resultErr  error
	cleanupErr error
	stopSent   bool
	killSent   bool
	closed     bool
}

func startPlatformProcess(spec agentprocess.Spec) (agentprocess.Process, error) {
	job, completionPort, err := newJob()
	if err != nil {
		return nil, err
	}
	// #nosec G204 -- preview5 Spec validates an absolute executable and the Git
	// runner owns the closed argument set; no shell participates.
	command := exec.Command(spec.Executable(), spec.Arguments()...) //nolint:noctx // Process lifetime is independently owned.
	command.Dir = spec.WorkingDirectory()
	command.Env = spec.Environment()
	command.Stdin = spec.Stdin()
	command.Stdout = spec.Stdout()
	command.Stderr = spec.Stderr()
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW}
	if err = command.Start(); err != nil {
		return nil, errors.Join(err, closeHandle(completionPort), closeHandle(job))
	}
	owned := &windowsProcess{
		command: command, job: job, completionPort: completionPort,
		done: make(chan struct{}), joined: make(chan struct{}),
	}
	handle, openErr := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false, uint32(command.Process.Pid), // #nosec G115 -- Windows process IDs are uint32.
	)
	if openErr == nil {
		err = windows.AssignProcessToJobObject(job, handle)
		openErr = errors.Join(err, windows.CloseHandle(handle))
	}
	if openErr == nil {
		owned.assigned = true
	} else {
		killErr := command.Process.Kill()
		owned.cleanupErr = errors.Join(owned.cleanupErr, openErr, killErr)
	}
	go owned.reap()
	if openErr != nil {
		return owned, openErr
	}
	return owned, nil
}

func (owned *windowsProcess) reap() {
	waitErr := owned.command.Wait()
	outcome, resultErr, cleanupErr := windowsOutcome(owned.command.ProcessState, waitErr)
	if owned.assigned {
		cleanupErr = errors.Join(cleanupErr, waitJobEmpty(owned.completionPort))
	}
	cleanupErr = errors.Join(cleanupErr, closeHandle(owned.completionPort), closeHandle(owned.job))
	owned.mu.Lock()
	owned.outcome, owned.resultErr = outcome, resultErr
	owned.cleanupErr = errors.Join(owned.cleanupErr, cleanupErr)
	owned.closed = true
	owned.job = 0
	owned.completionPort = 0
	owned.mu.Unlock()
	close(owned.done)
	close(owned.joined)
}

func windowsOutcome(state *os.ProcessState, waitErr error) (agentprocess.Outcome, error, error) {
	if state == nil {
		return agentprocess.Outcome{}, agentprocess.NewFailure(agentprocess.OperationResult, errors.New("root process produced no result")), waitErr
	}
	code := state.ExitCode()
	if code < 0 {
		return agentprocess.NewSignaledOutcome(), nil, nonExitFailure(waitErr)
	}
	outcome, err := agentprocess.NewExitedOutcome(int64(uint32(code)))
	return outcome, err, nonExitFailure(waitErr)
}

func nonExitFailure(err error) error {
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		return nil
	}
	return err
}

func (owned *windowsProcess) Done() <-chan struct{} {
	if owned == nil {
		return nil
	}
	return owned.done
}

func (owned *windowsProcess) Result() (agentprocess.Outcome, error) {
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

func (owned *windowsProcess) RequestStop(ctx context.Context) error {
	return owned.terminate(ctx, windowsStopExitCode, agentprocess.OperationRequestStop, false)
}

func (owned *windowsProcess) ForceKill(ctx context.Context) error {
	return owned.terminate(ctx, windowsKillExitCode, agentprocess.OperationForceKill, true)
}

func (owned *windowsProcess) terminate(ctx context.Context, code uint32, operation agentprocess.Operation, force bool) error {
	if err := validateContext(ctx, operation); err != nil {
		return err
	}
	if owned == nil {
		return agentprocess.NewFailure(operation, errors.New("process is unavailable"))
	}
	owned.signalMu.Lock()
	defer owned.signalMu.Unlock()
	owned.mu.Lock()
	if owned.closed || (force && owned.killSent) || (!force && owned.stopSent) {
		owned.mu.Unlock()
		return nil
	}
	job, assigned, process := owned.job, owned.assigned, owned.command.Process
	owned.mu.Unlock()
	var err error
	if assigned {
		err = windows.TerminateJobObject(job, code)
	} else {
		err = process.Kill()
	}
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return agentprocess.NewFailure(operation, err)
	}
	if force {
		owned.killSent = true
	} else {
		owned.stopSent = true
	}
	return nil
}

func (owned *windowsProcess) Wait(ctx context.Context) error {
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

func newJob() (windows.Handle, windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), // #nosec G103 -- exact Windows structure.
		uint32(unsafe.Sizeof(limits)),    // #nosec G115 -- static structure size.
	)
	if err != nil {
		return 0, 0, errors.Join(err, closeHandle(job))
	}
	completionPort, err := windows.CreateIoCompletionPort(windows.InvalidHandle, 0, 0, 1)
	if err != nil {
		return 0, 0, errors.Join(err, closeHandle(job))
	}
	association := jobCompletionPortAssociation{
		CompletionKey:  windowsCompletionKey,
		CompletionPort: completionPort,
	}
	_, err = windows.SetInformationJobObject(
		job, windows.JobObjectAssociateCompletionPortInformation,
		uintptr(unsafe.Pointer(&association)), // #nosec G103 -- exact Windows structure.
		uint32(unsafe.Sizeof(association)),    // #nosec G115 -- static structure size.
	)
	if err != nil {
		return 0, 0, errors.Join(err, closeHandle(completionPort), closeHandle(job))
	}
	return job, completionPort, nil
}

type jobCompletionPortAssociation struct {
	CompletionKey  uintptr
	CompletionPort windows.Handle
}

func waitJobEmpty(completionPort windows.Handle) error {
	for range windowsCompletionPolls {
		var message uint32
		var key uintptr
		var overlapped *windows.Overlapped
		err := windows.GetQueuedCompletionStatus(
			completionPort, &message, &key, &overlapped, windowsCompletionPollMilliseconds,
		)
		if err != nil {
			if errors.Is(err, windows.WAIT_TIMEOUT) {
				continue
			}
			return err
		}
		if key == windowsCompletionKey && message == windowsActiveProcessZero {
			return nil
		}
	}
	return errors.New("native process job did not become empty")
}

func closeHandle(handle windows.Handle) error {
	if handle == 0 || handle == windows.InvalidHandle {
		return nil
	}
	return windows.CloseHandle(handle)
}

var _ agentprocess.Process = (*windowsProcess)(nil)

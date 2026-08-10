package nativeprocess

import (
	"context"
	"errors"

	agentprocess "github.com/spice-framework/spice-agent/process"
)

type platformStart func(agentprocess.Spec) (agentprocess.Process, error)

// Launcher is the experiment-owned native implementation of preview5's public
// process contract. It invokes no shell and consumes only an immutable Spec.
type Launcher struct{ start platformStart }

// NewLauncher constructs the platform adapter.
func NewLauncher() *Launcher { return &Launcher{start: startPlatformProcess} }

// Start validates the spec and transfers every successfully created process
// even when later containment setup reports an error.
func (launcher *Launcher) Start(ctx context.Context, spec agentprocess.Spec) (agentprocess.Process, error) {
	if launcher == nil || launcher.start == nil {
		return nil, agentprocess.NewFailure(agentprocess.OperationLaunch, errors.New("native launcher is unavailable"))
	}
	if ctx == nil {
		return nil, agentprocess.NewFailure(agentprocess.OperationLaunch, errors.New("launch context is required"))
	}
	if err := ctx.Err(); err != nil {
		return nil, agentprocess.NewFailure(agentprocess.OperationLaunch, err)
	}
	if err := spec.Validate(); err != nil {
		return nil, agentprocess.NewFailure(agentprocess.OperationLaunch, err)
	}
	owned, err := launcher.start(spec.Clone())
	if owned == nil {
		if err == nil {
			err = errors.New("platform launcher returned no process")
		}
		return nil, agentprocess.NewFailure(agentprocess.OperationLaunch, err)
	}
	if err != nil {
		return owned, agentprocess.NewFailure(agentprocess.OperationLaunch, err)
	}
	if err = ctx.Err(); err != nil {
		return owned, agentprocess.NewFailure(agentprocess.OperationLaunch, err)
	}
	return owned, nil
}

func validateContext(ctx context.Context, operation agentprocess.Operation) error {
	if ctx == nil {
		return agentprocess.NewFailure(operation, errors.New("process operation context is required"))
	}
	if err := ctx.Err(); err != nil {
		return agentprocess.NewFailure(operation, err)
	}
	return nil
}

type terminalFailure struct{ cause error }

func (*terminalFailure) Error() string   { return "native process containment cleanup failed" }
func (*terminalFailure) Retryable() bool { return false }
func (failure *terminalFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

func containmentFailure(cause error) error {
	if cause == nil {
		return nil
	}
	return agentprocess.NewFailure(agentprocess.OperationWait, &terminalFailure{cause: cause})
}

var _ agentprocess.Launcher = (*Launcher)(nil)

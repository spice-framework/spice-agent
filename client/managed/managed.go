package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/spice-framework/spice-agent/client"
)

var (
	// ErrEndpointNotFound is returned only after secure discovery proves that no
	// usable endpoint metadata exists. It is the sole condition that authorizes
	// managed startup.
	ErrEndpointNotFound = errors.New("local daemon endpoint was not found")
	// ErrStartupTimeout reports that a launched daemon did not publish and serve
	// a usable endpoint within the configured startup budget.
	ErrStartupTimeout = errors.New("local daemon startup timed out")
	// ErrClosed reports use after the managed connector has closed.
	ErrClosed = errors.New("managed connector is closed")
)

// Discovery resolves current-user endpoint state into a configured connector.
// It must return ErrEndpointNotFound only after safely handling stale metadata;
// malformed, untrusted, or incompatible metadata is a hard error.
type Discovery interface {
	Discover(context.Context) (client.Connector, error)
}

// Starter launches one local daemon candidate. Returning nil means launch was
// accepted, not that the endpoint is already ready.
type Starter interface {
	Start(context.Context) error
}

// StartupLock serializes discovery recheck and process launch across clients.
type StartupLock interface {
	Acquire(context.Context) (StartupLease, error)
}

// StartupLease releases one acquired startup lock. Release must be idempotent
// and nonblocking so cleanup cannot escape the bounded startup operation.
type StartupLease interface {
	Release() error
}

// Config supplies platform-owned managed-start boundaries.
type Config struct {
	Discovery      Discovery
	Starter        Starter
	StartupLock    StartupLock
	StartupTimeout time.Duration
	RetryInterval  time.Duration
}

// Connector implements client.Connector with bounded attach-or-start policy.
type Connector struct {
	discovery      Discovery
	starter        Starter
	startupLock    StartupLock
	startupTimeout time.Duration
	retryInterval  time.Duration

	mu     sync.RWMutex
	closed bool
}

// String prevents dependency implementations from leaking endpoint material.
func (*Connector) String() string { return "managed.Connector([REDACTED])" }

// GoString prevents %#v from recursively formatting dependency state.
func (*Connector) GoString() string { return "managed.Connector([REDACTED])" }

// Format prevents every fmt verb from traversing dependency implementations.
func (*Connector) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "managed.Connector([REDACTED])")
}

// MarshalJSON makes accidental structured serialization visibly redacted.
func (*Connector) MarshalJSON() ([]byte, error) {
	return json.Marshal("managed.Connector([REDACTED])")
}

// New constructs a managed connector without performing I/O.
func New(config Config) (*Connector, error) {
	if config.Discovery == nil || config.Starter == nil || config.StartupLock == nil {
		return nil, errors.New("managed connector requires discovery, starter, and startup lock")
	}
	if config.StartupTimeout <= 0 || config.RetryInterval <= 0 ||
		config.RetryInterval > config.StartupTimeout {
		return nil, errors.New("managed connector timing is invalid")
	}
	return &Connector{
		discovery: config.Discovery, starter: config.Starter, startupLock: config.StartupLock,
		startupTimeout: config.StartupTimeout, retryInterval: config.RetryInterval,
	}, nil
}

// Initialize attaches to an existing endpoint or performs one serialized,
// bounded launch after secure discovery proves the endpoint is absent.
func (connector *Connector) Initialize(
	ctx context.Context,
	request client.InitializeRequest,
) (session client.Session, resultErr error) {
	if connector == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, errors.New("managed initialization context is required")
	}
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("managed initialization request: %w", err)
	}
	if err := connector.available(); err != nil {
		return nil, err
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	resolved, err := connector.discovery.Discover(ctx)
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	if err == nil {
		return initializeResolved(ctx, resolved, request)
	}
	if !exactEndpointAbsence(err) {
		return nil, fmt.Errorf("discover local daemon: %w", err)
	}

	startupContext, cancelStartup := context.WithTimeoutCause(ctx, connector.startupTimeout, ErrStartupTimeout)
	defer cancelStartup()
	lease, err := connector.startupLock.Acquire(startupContext)
	if err != nil {
		var releaseErr error
		if lease != nil {
			releaseErr = lease.Release()
		}
		return nil, errors.Join(
			classifyStartupContext(startupContext, fmt.Errorf("acquire daemon startup lock: %w", err)),
			releaseErr,
		)
	}
	if lease == nil {
		return nil, errors.New("daemon startup lock returned a nil lease")
	}
	defer func() {
		if releaseErr := lease.Release(); releaseErr != nil {
			var closeErr error
			if session != nil {
				closeErr = session.Close()
				session = nil
			}
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("release daemon startup lock: %w", releaseErr),
				closeErr,
			)
		}
	}()
	if err = context.Cause(startupContext); err != nil {
		return nil, classifyStartupContext(startupContext, err)
	}

	resolved, err = connector.discovery.Discover(startupContext)
	if cause := context.Cause(startupContext); cause != nil {
		return nil, classifyStartupContext(startupContext, cause)
	}
	if err == nil {
		return initializeResolved(startupContext, resolved, request)
	}
	if !exactEndpointAbsence(err) {
		return nil, fmt.Errorf("rediscover local daemon: %w", err)
	}
	if err = context.Cause(startupContext); err != nil {
		return nil, classifyStartupContext(startupContext, err)
	}
	if err = connector.starter.Start(startupContext); err != nil {
		return nil, classifyStartupContext(startupContext, fmt.Errorf("start local daemon: %w", err))
	}
	return connector.awaitStarted(startupContext, request)
}

func (connector *Connector) awaitStarted(
	ctx context.Context,
	request client.InitializeRequest,
) (client.Session, error) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, classifyStartupContext(ctx, ctx.Err())
		case <-timer.C:
		}
		resolved, err := connector.discovery.Discover(ctx)
		if cause := context.Cause(ctx); cause != nil {
			return nil, classifyStartupContext(ctx, cause)
		}
		if err == nil {
			session, initializeErr := initializeResolved(ctx, resolved, request)
			if initializeErr == nil {
				return session, nil
			}
			if !transientStartupError(initializeErr) {
				return nil, initializeErr
			}
		} else if !exactEndpointAbsence(err) {
			return nil, fmt.Errorf("discover started daemon: %w", err)
		}
		timer.Reset(connector.retryInterval)
	}
}

func exactEndpointAbsence(err error) bool {
	// A joined or wrapped absence is intentionally a hard failure: managed
	// startup is authorized only when discovery returns this sentinel alone.
	return err == ErrEndpointNotFound //nolint:errorlint // Exact identity is the security boundary.
}

func initializeResolved(
	ctx context.Context,
	connector client.Connector,
	request client.InitializeRequest,
) (client.Session, error) {
	if connector == nil {
		return nil, errors.New("endpoint discovery returned a nil connector")
	}
	session, err := connector.Initialize(ctx, request)
	if cause := context.Cause(ctx); cause != nil {
		var closeErr error
		if session != nil {
			closeErr = session.Close()
		}
		return nil, errors.Join(cause, err, closeErr)
	}
	if err != nil {
		var closeErr error
		if session != nil {
			closeErr = session.Close()
		}
		return nil, errors.Join(fmt.Errorf("initialize local daemon: %w", err), closeErr)
	}
	if session == nil {
		return nil, errors.New("local daemon returned a nil session")
	}
	return session, nil
}

func transientStartupError(err error) bool {
	var statusFailure *client.StatusError
	return errors.As(err, &statusFailure) && statusFailure.Code() == client.ErrorUnavailable &&
		statusFailure.Retryable()
}

func classifyStartupContext(ctx context.Context, fallback error) error {
	if err := context.Cause(ctx); err != nil {
		if errors.Is(err, ErrStartupTimeout) {
			return errors.Join(ErrStartupTimeout, context.DeadlineExceeded)
		}
		return err
	}
	return fallback
}

func (connector *Connector) available() error {
	connector.mu.RLock()
	defer connector.mu.RUnlock()
	if connector.closed {
		return ErrClosed
	}
	return nil
}

// Close prevents future initialization. It does not close sessions already
// returned to callers and never stops a user-scoped daemon.
func (connector *Connector) Close() error {
	if connector == nil {
		return nil
	}
	connector.mu.Lock()
	connector.closed = true
	connector.mu.Unlock()
	return nil
}

var _ client.Connector = (*Connector)(nil)

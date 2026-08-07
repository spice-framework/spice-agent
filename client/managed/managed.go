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
	// ErrCandidateExited reports that the child launched by this connector
	// terminated before a usable initialized endpoint was available.
	ErrCandidateExited = errors.New("managed daemon candidate exited before startup completed")
	// ErrShutdownTimeout reports that an owned candidate did not join within the
	// connector's bounded shutdown budget.
	ErrShutdownTimeout = errors.New("managed daemon shutdown timed out")
)

// Discovery resolves current-user endpoint state into a configured connector.
// It must return ErrEndpointNotFound only after safely handling stale metadata;
// malformed, untrusted, or incompatible metadata is a hard error.
type Discovery interface {
	Discover(context.Context) (client.Connector, error)
}

// Candidate is the exact child process lifetime returned by Starter. Done
// closes exactly once at process exit. Result is safe after Done closes and
// reports the final process outcome. BeginShutdown is idempotent and
// nonblocking. Wait joins process resources and must honor its context.
type Candidate interface {
	Done() <-chan struct{}
	Result() error
	BeginShutdown() error
	Wait(context.Context) error
}

// Starter launches one local daemon candidate whose lifetime is independent
// of the launch context. A non-nil candidate returned with an error is still
// owned by the caller and will be shut down and joined.
type Starter interface {
	Start(context.Context) (Candidate, error)
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
	Discovery       Discovery
	Starter         Starter
	StartupLock     StartupLock
	StartupTimeout  time.Duration
	RetryInterval   time.Duration
	ShutdownTimeout time.Duration
}

// Connector implements client.Connector with bounded attach-or-start policy.
type Connector struct {
	discovery       Discovery
	starter         Starter
	startupLock     StartupLock
	startupTimeout  time.Duration
	retryInterval   time.Duration
	shutdownTimeout time.Duration

	initializeGate chan struct{}

	mu           sync.Mutex
	closed       bool
	activeCancel context.CancelCauseFunc
	owned        Candidate
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
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = config.StartupTimeout
	}
	if config.StartupTimeout <= 0 || config.RetryInterval <= 0 || config.ShutdownTimeout <= 0 ||
		config.RetryInterval > config.StartupTimeout {
		return nil, errors.New("managed connector timing is invalid")
	}
	return &Connector{
		discovery: config.Discovery, starter: config.Starter, startupLock: config.StartupLock,
		startupTimeout: config.StartupTimeout, retryInterval: config.RetryInterval,
		shutdownTimeout: config.ShutdownTimeout, initializeGate: newInitializeGate(),
	}, nil
}

func newInitializeGate() chan struct{} {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return gate
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
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	operationContext, finish, err := connector.beginInitialize(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		session, resultErr = finish(session, resultErr)
	}()
	ctx = operationContext
	resolved, err := connector.discovery.Discover(ctx)
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	if err == nil {
		return initializeResolved(ctx, resolved, request)
	}
	if !exactEndpointAbsence(err) {
		return nil, opaqueFailure("discover local daemon failed", err)
	}
	return connector.initializeAfterAbsence(ctx, request)
}

func (connector *Connector) initializeAfterAbsence(
	ctx context.Context,
	request client.InitializeRequest,
) (session client.Session, resultErr error) {
	startupContext, cancelStartup := context.WithTimeoutCause(ctx, connector.startupTimeout, ErrStartupTimeout)
	defer cancelStartup()
	lease, err := connector.startupLock.Acquire(startupContext)
	if err != nil {
		var releaseErr error
		if lease != nil {
			releaseErr = lease.Release()
		}
		return nil, opaqueFailure(
			"acquire daemon startup lock failed",
			classifyStartupContext(startupContext, opaqueFailure("acquire daemon startup lock failed", err)),
			releaseErr,
		)
	}
	if lease == nil {
		return nil, errors.New("daemon startup lock returned a nil lease")
	}
	var launchedCandidate Candidate
	defer func() {
		if releaseErr := lease.Release(); releaseErr != nil {
			var closeErr, cleanupErr error
			if session != nil {
				closeErr = session.Close()
				session = nil
			}
			if launchedCandidate != nil {
				cleanupErr = connector.cleanupOwnedCandidate(launchedCandidate)
				launchedCandidate = nil
			}
			resultErr = opaqueFailure(
				"managed startup cleanup failed",
				resultErr,
				releaseErr,
				closeErr,
				cleanupErr,
			)
		}
	}()
	if err = context.Cause(startupContext); err != nil {
		return nil, classifyStartupContext(startupContext, err)
	}

	resolved, err := connector.discovery.Discover(startupContext)
	if cause := context.Cause(startupContext); cause != nil {
		return nil, classifyStartupContext(startupContext, cause)
	}
	if err == nil {
		return initializeResolved(startupContext, resolved, request)
	}
	if !exactEndpointAbsence(err) {
		return nil, opaqueFailure("rediscover local daemon failed", err)
	}
	if err = context.Cause(startupContext); err != nil {
		return nil, classifyStartupContext(startupContext, err)
	}
	candidate, launched, err := connector.ensureCandidate(startupContext)
	if err != nil {
		return nil, classifyStartupContext(startupContext, err)
	}
	if launched {
		launchedCandidate = candidate
	}
	session, err = connector.awaitStarted(startupContext, request, candidate)
	if err == nil {
		return session, nil
	}
	if !launched && !errors.Is(err, ErrCandidateExited) {
		return nil, err
	}
	cleanupErr := connector.cleanupOwnedCandidate(candidate)
	launchedCandidate = nil
	return nil, opaqueFailure("managed daemon startup failed", err, cleanupErr)
}

func (connector *Connector) beginInitialize(
	ctx context.Context,
) (context.Context, func(client.Session, error) (client.Session, error), error) {
	select {
	case <-ctx.Done():
		return nil, nil, context.Cause(ctx)
	case <-connector.initializeGate:
	}
	connector.mu.Lock()
	if connector.closed {
		connector.mu.Unlock()
		connector.initializeGate <- struct{}{}
		return nil, nil, ErrClosed
	}
	operationContext, cancel := context.WithCancelCause(ctx)
	connector.activeCancel = cancel
	connector.mu.Unlock()
	return operationContext, func(session client.Session, resultErr error) (client.Session, error) {
		return connector.finishInitialize(operationContext, cancel, session, resultErr)
	}, nil
}

func (connector *Connector) finishInitialize(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	session client.Session,
	resultErr error,
) (client.Session, error) {
	connector.mu.Lock()
	cause := context.Cause(ctx)
	if cause == nil && connector.closed {
		cause = ErrClosed
	}
	connector.activeCancel = nil
	connector.mu.Unlock()
	cancel(nil)
	if cause != nil {
		var closeErr error
		if session != nil {
			closeErr = session.Close()
			session = nil
		}
		resultErr = opaqueFailure("managed initialization did not complete", cause, resultErr, closeErr)
	}
	connector.initializeGate <- struct{}{}
	return session, resultErr
}

func (connector *Connector) ensureCandidate(ctx context.Context) (Candidate, bool, error) {
	connector.mu.Lock()
	existing := connector.owned
	closed := connector.closed
	connector.mu.Unlock()
	if existing != nil {
		return existing, false, nil
	}
	if closed {
		return nil, false, ErrClosed
	}
	candidate, err := connector.starter.Start(ctx)
	if candidate == nil {
		if err != nil {
			return nil, false, opaqueFailure("start local daemon failed", err)
		}
		return nil, false, errors.New("local daemon starter returned a nil candidate")
	}
	if candidate.Done() == nil {
		_, cleanupErr := connector.shutdownCandidate(candidate, context.Background())
		return nil, false, opaqueFailure("local daemon candidate is invalid", cleanupErr)
	}
	connector.mu.Lock()
	if connector.owned != nil {
		connector.mu.Unlock()
		_, cleanupErr := connector.shutdownCandidate(candidate, context.Background())
		return nil, false, opaqueFailure("managed connector already owns a daemon candidate", cleanupErr)
	}
	connector.owned = candidate
	closed = connector.closed
	connector.mu.Unlock()
	if err != nil {
		cleanupErr := connector.cleanupOwnedCandidate(candidate)
		return nil, false, opaqueFailure("start local daemon failed", err, cleanupErr)
	}
	if closed {
		cleanupErr := connector.cleanupOwnedCandidate(candidate)
		return nil, false, opaqueFailure("managed connector closed during daemon launch", ErrClosed, cleanupErr)
	}
	return candidate, true, nil
}

func (connector *Connector) awaitStarted(
	ctx context.Context,
	request client.InitializeRequest,
	candidate Candidate,
) (client.Session, error) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		if cause := context.Cause(ctx); cause != nil {
			return nil, classifyStartupContext(ctx, cause)
		}
		if candidateJoined(candidate) {
			return nil, candidateExitFailure(candidate)
		}
		select {
		case <-ctx.Done():
			return nil, classifyStartupContext(ctx, ctx.Err())
		case <-candidate.Done():
			if cause := context.Cause(ctx); cause != nil {
				return nil, classifyStartupContext(ctx, cause)
			}
			return nil, candidateExitFailure(candidate)
		case <-timer.C:
		}
		if cause := context.Cause(ctx); cause != nil {
			return nil, classifyStartupContext(ctx, cause)
		}
		if candidateJoined(candidate) {
			return nil, candidateExitFailure(candidate)
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
			return nil, opaqueFailure("discover started daemon failed", err)
		}
		timer.Reset(connector.retryInterval)
	}
}

func candidateExitFailure(candidate Candidate) error {
	return opaqueFailure("managed daemon candidate exited", ErrCandidateExited, candidate.Result())
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
		return nil, opaqueFailure("managed initialization was canceled", cause, err, closeErr)
	}
	if err != nil {
		var closeErr error
		if session != nil {
			closeErr = session.Close()
		}
		return nil, opaqueFailure("initialize local daemon failed", err, closeErr)
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

func (connector *Connector) cleanupOwnedCandidate(candidate Candidate) error {
	joined, err := connector.shutdownCandidate(candidate, context.Background())
	if joined {
		connector.mu.Lock()
		connector.owned = nil
		connector.mu.Unlock()
	}
	return err
}

func (connector *Connector) shutdownCandidate(
	candidate Candidate,
	parent context.Context,
) (bool, error) {
	if candidate == nil {
		return true, nil
	}
	ctx, cancel := context.WithTimeoutCause(parent, connector.shutdownTimeout, ErrShutdownTimeout)
	defer cancel()
	beginErr := candidate.BeginShutdown()
	waitErr := candidate.Wait(ctx)
	joined := candidateJoined(candidate)
	if !joined {
		cause := context.Cause(ctx)
		if cause == nil {
			cause = ErrShutdownTimeout
		}
		return false, opaqueFailure("managed daemon shutdown did not join", cause, waitErr, beginErr)
	}
	return true, opaqueFailureOrNil("managed daemon shutdown failed", beginErr, waitErr, candidate.Result())
}

func candidateJoined(candidate Candidate) bool {
	if candidate == nil || candidate.Done() == nil {
		return false
	}
	select {
	case <-candidate.Done():
		return true
	default:
		return false
	}
}

// Shutdown closes admission, cancels in-flight initialization, and boundedly
// shuts down and joins only the exact candidate launched by this connector.
// A daemon discovered before launch is external and is never stopped.
func (connector *Connector) Shutdown(ctx context.Context) error {
	if connector == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("managed shutdown context is required")
	}
	shutdownContext, cancel := context.WithTimeoutCause(ctx, connector.shutdownTimeout, ErrShutdownTimeout)
	defer cancel()
	connector.mu.Lock()
	connector.closed = true
	activeCancel := connector.activeCancel
	connector.mu.Unlock()
	if activeCancel != nil {
		activeCancel(ErrClosed)
	}
	select {
	case <-shutdownContext.Done():
		return classifyShutdownContext(shutdownContext)
	case <-connector.initializeGate:
	}
	defer func() { connector.initializeGate <- struct{}{} }()
	connector.mu.Lock()
	candidate := connector.owned
	connector.mu.Unlock()
	joined, err := connector.shutdownCandidate(candidate, shutdownContext)
	if joined {
		connector.mu.Lock()
		connector.owned = nil
		connector.mu.Unlock()
	}
	return err
}

func classifyShutdownContext(ctx context.Context) error {
	cause := context.Cause(ctx)
	if errors.Is(cause, ErrShutdownTimeout) {
		return opaqueFailure("managed daemon shutdown timed out", ErrShutdownTimeout, context.DeadlineExceeded)
	}
	return cause
}

// Close is the context-free compatibility alias for bounded Shutdown. It does
// not close sessions already returned to callers.
func (connector *Connector) Close() error {
	if connector == nil {
		return nil
	}
	return connector.Shutdown(context.Background())
}

type redactedFailure struct {
	message string
	causes  []error
}

func (failure *redactedFailure) Error() string { return failure.message }

func (failure *redactedFailure) Unwrap() []error { return failure.causes }

func opaqueFailure(message string, causes ...error) error {
	filtered := make([]error, 0, len(causes))
	for _, cause := range causes {
		if cause != nil {
			filtered = append(filtered, cause)
		}
	}
	return &redactedFailure{message: message, causes: filtered}
}

func opaqueFailureOrNil(message string, causes ...error) error {
	for _, cause := range causes {
		if cause != nil {
			return opaqueFailure(message, causes...)
		}
	}
	return nil
}

var _ client.Connector = (*Connector)(nil)

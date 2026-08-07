package managed_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
	"github.com/spice-framework/spice-agent/client/managed"
)

func TestConnectorAttachesWithoutStartup(t *testing.T) {
	t.Parallel()
	session := &fixtureSession{}
	discovery := &fixtureDiscovery{results: []discoveryResult{{connector: fixtureConnector{session: session}}}}
	starter := &fixtureStarter{}
	lock := &fixtureLock{}
	connector := newManagedFixture(t, discovery, starter, lock)

	got, err := connector.Initialize(t.Context(), initializeRequestFixture(t))
	if err != nil || got != session || starter.calls.Load() != 0 || lock.calls.Load() != 0 {
		t.Fatalf("attach = %#v, %v; starts=%d locks=%d", got, err, starter.calls.Load(), lock.calls.Load())
	}
}

func TestConnectorSerializesRechecksAndStartsOnce(t *testing.T) {
	t.Parallel()
	session := &fixtureSession{}
	discovery := &fixtureDiscovery{results: []discoveryResult{
		{err: managed.ErrEndpointNotFound},
		{err: managed.ErrEndpointNotFound},
		{err: managed.ErrEndpointNotFound},
		{connector: fixtureConnector{session: session}},
	}}
	starter := &fixtureStarter{}
	lock := &fixtureLock{}
	connector := newManagedFixture(t, discovery, starter, lock)

	got, err := connector.Initialize(t.Context(), initializeRequestFixture(t))
	if err != nil || got != session || starter.calls.Load() != 1 || lock.calls.Load() != 1 || lock.lease.releases.Load() != 1 {
		t.Fatalf(
			"managed start = %#v, %v; starts=%d locks=%d releases=%d",
			got, err, starter.calls.Load(), lock.calls.Load(), lock.lease.releases.Load(),
		)
	}
}

func TestConnectorUsesDaemonStartedByLockWinner(t *testing.T) {
	t.Parallel()
	session := &fixtureSession{}
	discovery := &fixtureDiscovery{results: []discoveryResult{
		{err: managed.ErrEndpointNotFound},
		{connector: fixtureConnector{session: session}},
	}}
	starter := &fixtureStarter{}
	connector := newManagedFixture(t, discovery, starter, &fixtureLock{})

	got, err := connector.Initialize(t.Context(), initializeRequestFixture(t))
	if err != nil || got != session || starter.calls.Load() != 0 {
		t.Fatalf("lock winner reuse = %#v, %v; starts=%d", got, err, starter.calls.Load())
	}
}

func TestConnectorNeverStartsOverExistingFailure(t *testing.T) {
	t.Parallel()
	failure := errors.New("incompatible daemon")
	for name, discovery := range map[string]*fixtureDiscovery{
		"discovery":      {results: []discoveryResult{{err: failure}}},
		"joined absence": {results: []discoveryResult{{err: errors.Join(managed.ErrEndpointNotFound, failure)}}},
		"initialize":     {results: []discoveryResult{{connector: fixtureConnector{err: failure}}}},
		"recheck":        {results: []discoveryResult{{err: managed.ErrEndpointNotFound}, {err: failure}}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			starter := &fixtureStarter{}
			connector := newManagedFixture(t, discovery, starter, &fixtureLock{})
			if _, err := connector.Initialize(t.Context(), initializeRequestFixture(t)); !errors.Is(err, failure) {
				t.Fatalf("existing failure = %v", err)
			}
			if starter.calls.Load() != 0 {
				t.Fatalf("started %d daemons over existing endpoint", starter.calls.Load())
			}
		})
	}
}

func TestConnectorReleasesLeaseReturnedWithAcquireError(t *testing.T) {
	t.Parallel()
	acquireFailure := errors.New("acquire failed")
	releaseFailure := errors.New("release failed")
	lock := &fixtureLock{err: acquireFailure, returnLeaseOnError: true, lease: fixtureLease{err: releaseFailure}}
	connector := newManagedFixture(t, &fixtureDiscovery{}, &fixtureStarter{}, lock)
	_, err := connector.Initialize(t.Context(), initializeRequestFixture(t))
	if !errors.Is(err, acquireFailure) || !errors.Is(err, releaseFailure) || lock.lease.releases.Load() != 1 {
		t.Fatalf("acquire/release failure = %v; releases=%d", err, lock.lease.releases.Load())
	}
}

func TestConnectorCancellationClosesLateSessionAndMasksDiscoveryFailure(t *testing.T) {
	t.Parallel()
	for name, discovery := range map[string]*cancelingDiscovery{
		"discovery failure": {err: errors.New("masked discovery failure")},
		"late session":      {connector: fixtureConnector{session: &fixtureSession{}}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			discovery.cancel = cancel
			connector := newManagedFixture(t, discovery, &fixtureStarter{}, &fixtureLock{})
			session, err := connector.Initialize(ctx, initializeRequestFixture(t))
			if session != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled discovery result = %#v, %v", session, err)
			}
			if late, ok := discovery.connector.(fixtureConnector); ok {
				lateSession, sessionOK := late.session.(*fixtureSession)
				if !sessionOK {
					t.Fatalf("late session type = %T", late.session)
				}
				if lateSession.closed.Load() != 0 {
					t.Fatal("connector was invoked after discovery canceled the context")
				}
			}
		})
	}

	late := &fixtureSession{}
	ctx, cancel := context.WithCancel(context.Background())
	connector := newManagedFixture(
		t,
		&fixtureDiscovery{results: []discoveryResult{{connector: cancelingConnector{session: late, cancel: cancel}}}},
		&fixtureStarter{}, &fixtureLock{},
	)
	session, err := connector.Initialize(ctx, initializeRequestFixture(t))
	if session != nil || !errors.Is(err, context.Canceled) || late.closed.Load() != 1 {
		t.Fatalf("late canceled session = %#v, %v; closes=%d", session, err, late.closed.Load())
	}
}

func TestConnectorBoundsStartupAndPropagatesCancellation(t *testing.T) {
	t.Parallel()
	for name, setup := range map[string]func(*testing.T) (*managed.Connector, context.Context, context.CancelFunc){
		"timeout": func(t *testing.T) (*managed.Connector, context.Context, context.CancelFunc) {
			t.Helper()
			connector, err := managed.New(managed.Config{
				Discovery: &fixtureDiscovery{}, Starter: &fixtureStarter{}, StartupLock: &fixtureLock{},
				StartupTimeout: 15 * time.Millisecond, RetryInterval: time.Millisecond,
			})
			if err != nil {
				t.Fatal(err)
			}
			return connector, context.Background(), func() {}
		},
		"cancel": func(t *testing.T) (*managed.Connector, context.Context, context.CancelFunc) {
			t.Helper()
			connector := newManagedFixture(t, &fixtureDiscovery{}, &fixtureStarter{}, &fixtureLock{})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return connector, ctx, func() {}
		},
		"caller deadline": func(t *testing.T) (*managed.Connector, context.Context, context.CancelFunc) {
			t.Helper()
			connector := newManagedFixture(t, &fixtureDiscovery{}, &fixtureStarter{}, &fixtureLock{})
			ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
			return connector, ctx, cancel
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			connector, ctx, cancel := setup(t)
			defer cancel()
			_, err := connector.Initialize(ctx, initializeRequestFixture(t))
			if name == "timeout" && (!errors.Is(err, managed.ErrStartupTimeout) || !errors.Is(err, context.DeadlineExceeded)) {
				t.Fatalf("startup timeout = %v", err)
			}
			if name == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("startup cancellation = %v", err)
			}
			if name == "caller deadline" &&
				(!errors.Is(err, context.DeadlineExceeded) || errors.Is(err, managed.ErrStartupTimeout)) {
				t.Fatalf("caller deadline = %v", err)
			}
		})
	}
}

func TestConnectorRetriesOnlyRetryableUnavailableAfterLaunch(t *testing.T) {
	t.Parallel()
	retryable := statusErrorFixture(t, client.ErrorUnavailable, true)
	nonretryable := statusErrorFixture(t, client.ErrorUnavailable, false)
	for name, first := range map[string]error{"retryable": retryable, "nonretryable": nonretryable} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			session := &fixtureSession{}
			discovery := &fixtureDiscovery{results: []discoveryResult{
				{err: managed.ErrEndpointNotFound},
				{err: managed.ErrEndpointNotFound},
				{connector: fixtureConnector{err: first}},
				{connector: fixtureConnector{session: session}},
			}}
			connector := newManagedFixture(t, discovery, &fixtureStarter{}, &fixtureLock{})
			got, err := connector.Initialize(t.Context(), initializeRequestFixture(t))
			if name == "retryable" && (err != nil || got != session) {
				t.Fatalf("retryable startup = %#v, %v", got, err)
			}
			if name == "nonretryable" && !errors.Is(err, nonretryable) {
				t.Fatalf("nonretryable startup = %v", err)
			}
		})
	}
}

func TestConnectorReleaseFailureClosesReturnedSession(t *testing.T) {
	t.Parallel()
	releaseFailure := errors.New("release failed")
	session := &fixtureSession{}
	discovery := &fixtureDiscovery{results: []discoveryResult{
		{err: managed.ErrEndpointNotFound}, {connector: fixtureConnector{session: session}},
	}}
	lock := &fixtureLock{lease: fixtureLease{err: releaseFailure}}
	connector := newManagedFixture(t, discovery, &fixtureStarter{}, lock)
	got, err := connector.Initialize(t.Context(), initializeRequestFixture(t))
	if got != nil || !errors.Is(err, releaseFailure) || session.closed.Load() != 1 {
		t.Fatalf("release failure = %#v, %v; closes=%d", got, err, session.closed.Load())
	}
}

func TestConnectorPreservesPrimaryAndReleaseFailures(t *testing.T) {
	t.Parallel()
	primaryFailure := errors.New("existing endpoint failed")
	releaseFailure := errors.New("release failed")
	discovery := &fixtureDiscovery{results: []discoveryResult{
		{err: managed.ErrEndpointNotFound}, {err: primaryFailure},
	}}
	lock := &fixtureLock{lease: fixtureLease{err: releaseFailure}}
	connector := newManagedFixture(t, discovery, &fixtureStarter{}, lock)
	_, err := connector.Initialize(t.Context(), initializeRequestFixture(t))
	if !errors.Is(err, primaryFailure) || !errors.Is(err, releaseFailure) {
		t.Fatalf("combined startup/release failure = %v", err)
	}
}

func TestConnectorRejectsInvalidConfigurationAndClose(t *testing.T) {
	t.Parallel()
	if connector, err := managed.New(managed.Config{}); err == nil || connector != nil {
		t.Fatal("empty managed configuration succeeded")
	}
	connector := newManagedFixture(t, &fixtureDiscovery{}, &fixtureStarter{}, &fixtureLock{})
	if err := connector.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := connector.Initialize(t.Context(), initializeRequestFixture(t)); !errors.Is(err, managed.ErrClosed) {
		t.Fatalf("closed initialization = %v", err)
	}
	if err := connector.Close(); err != nil {
		t.Fatalf("idempotent close = %v", err)
	}
	var nilConnector *managed.Connector
	if err := nilConnector.Close(); err != nil {
		t.Fatalf("nil close = %v", err)
	}
	if _, err := nilConnector.Initialize(t.Context(), initializeRequestFixture(t)); !errors.Is(err, managed.ErrClosed) {
		t.Fatalf("nil initialization = %v", err)
	}
}

func TestConnectorRedactsDependencyState(t *testing.T) {
	t.Parallel()
	const secret = "endpoint-secret-canary"
	connector := newManagedFixture(
		t, secretDiscovery{secret: secret}, secretStarter{secret: secret}, secretLock{secret: secret},
	)
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%p", "%T"} {
		formatted := fmt.Sprintf(format, connector)
		if strings.Contains(formatted, secret) ||
			(format != "%p" && format != "%T" && !strings.Contains(formatted, "REDACTED")) {
			t.Fatalf("managed connector format %q = %q", format, formatted)
		}
	}
	encoded, err := json.Marshal(connector)
	if err != nil || strings.Contains(string(encoded), secret) || !strings.Contains(string(encoded), "REDACTED") {
		t.Fatalf("managed connector JSON = %q, %v", encoded, err)
	}
	var output bytes.Buffer
	slog.New(slog.NewJSONHandler(&output, nil)).Info("managed", "connector", connector)
	if strings.Contains(output.String(), secret) {
		t.Fatal("managed connector structured log exposed dependency state")
	}
}

type discoveryResult struct {
	connector client.Connector
	err       error
}

type fixtureDiscovery struct {
	mu      sync.Mutex
	results []discoveryResult
	index   int
}

func (discovery *fixtureDiscovery) Discover(context.Context) (client.Connector, error) {
	discovery.mu.Lock()
	defer discovery.mu.Unlock()
	if len(discovery.results) == 0 {
		return nil, managed.ErrEndpointNotFound
	}
	index := min(discovery.index, len(discovery.results)-1)
	discovery.index++
	return discovery.results[index].connector, discovery.results[index].err
}

type fixtureStarter struct {
	calls atomic.Int32
	err   error
}

func (starter *fixtureStarter) Start(context.Context) error {
	starter.calls.Add(1)
	return starter.err
}

type fixtureLock struct {
	calls              atomic.Int32
	lease              fixtureLease
	err                error
	returnLeaseOnError bool
}

func (lock *fixtureLock) Acquire(context.Context) (managed.StartupLease, error) {
	lock.calls.Add(1)
	if lock.err != nil {
		if lock.returnLeaseOnError {
			return &lock.lease, lock.err
		}
		return nil, lock.err
	}
	return &lock.lease, nil
}

type fixtureLease struct {
	releases atomic.Int32
	err      error
}

func (lease *fixtureLease) Release() error {
	lease.releases.Add(1)
	return lease.err
}

type fixtureConnector struct {
	session client.Session
	err     error
}

type cancelingDiscovery struct {
	connector client.Connector
	err       error
	cancel    context.CancelFunc
}

func (discovery *cancelingDiscovery) Discover(context.Context) (client.Connector, error) {
	discovery.cancel()
	return discovery.connector, discovery.err
}

type cancelingConnector struct {
	session client.Session
	cancel  context.CancelFunc
}

func (connector cancelingConnector) Initialize(context.Context, client.InitializeRequest) (client.Session, error) {
	connector.cancel()
	return connector.session, nil
}

func (connector fixtureConnector) Initialize(context.Context, client.InitializeRequest) (client.Session, error) {
	return connector.session, connector.err
}

type fixtureSession struct {
	client.Session
	closed atomic.Int32
}

type secretDiscovery struct{ secret string }

func (secretDiscovery) Discover(context.Context) (client.Connector, error) {
	return nil, managed.ErrEndpointNotFound
}

type secretStarter struct{ secret string }

func (secretStarter) Start(context.Context) error { return nil }

type secretLock struct{ secret string }

func (secretLock) Acquire(context.Context) (managed.StartupLease, error) {
	return &fixtureLease{}, nil
}

func (session *fixtureSession) Close() error {
	session.closed.Add(1)
	return nil
}

func newManagedFixture(
	t *testing.T,
	discovery managed.Discovery,
	starter managed.Starter,
	lock managed.StartupLock,
) *managed.Connector {
	t.Helper()
	connector, err := managed.New(managed.Config{
		Discovery: discovery, Starter: starter, StartupLock: lock,
		StartupTimeout: time.Second, RetryInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return connector
}

func statusErrorFixture(t *testing.T, code client.ErrorCode, retryable bool) error {
	t.Helper()
	facts, err := client.NewErrorFacts("daemon unavailable", retryable, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := client.NewStatusError(code, facts)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func initializeRequestFixture(t *testing.T) client.InitializeRequest {
	t.Helper()
	protocol, err := client.NewProtocolVersion(1, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	rangeValue, err := client.NewProtocolRange(protocol, protocol)
	if err != nil {
		t.Fatal(err)
	}
	build, err := client.NewBuild("test-client", "v0.1.0", "commit", "go1.26.5")
	if err != nil {
		t.Fatal(err)
	}
	limits, err := client.NewLimits(1<<20, 1024, 128, 1<<20, 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	return mustInitializeRequest(t, rangeValue, build, limits)
}

func mustInitializeRequest(
	t *testing.T,
	protocol client.ProtocolRange,
	build client.Build,
	limits client.Limits,
) client.InitializeRequest {
	t.Helper()
	request, err := client.NewLegacyInitializeRequest(protocol, build, []string{"events"}, nil, limits)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

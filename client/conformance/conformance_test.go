package conformance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
)

func TestRunAcceptsInitialLifecycleProfile(t *testing.T) {
	t.Parallel()
	connector := &fixtureConnector{}
	err := Run(t.Context(), connector, Config{
		Initialize:       initializeRequestFixture(t),
		Waiting:          waitingRequestFixture(t, "waiting-operation"),
		OperationTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if connector.initializations != 2 {
		t.Fatalf("initializations = %d, want 2", connector.initializations)
	}
}

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	if err := Run(nil, nil, Config{}); err == nil { //nolint:staticcheck // Deliberate nil public boundary.
		t.Fatal("nil context and connector succeeded")
	}
	if err := Run(context.Background(), nil, Config{}); err == nil {
		t.Fatal("nil connector succeeded")
	}
	if err := Run(context.Background(), rejectingConnector{}, Config{
		OperationTimeout: time.Millisecond,
	}); err == nil {
		t.Fatal("unbounded timeout succeeded")
	}
	if err := Run(context.Background(), rejectingConnector{}, Config{}); err == nil {
		t.Fatal("zero initialize request succeeded")
	}
	if err := Run(context.Background(), rejectingConnector{}, Config{
		Initialize: initializeRequestFixture(t),
	}); err == nil {
		t.Fatal("zero waiting request succeeded")
	}
}

func TestRunPropagatesOwnedCloseErrors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		failures fixtureCloseFailures
	}{
		{name: "event stream", failures: fixtureCloseFailures{event: errCloseSentinel}},
		{name: "interaction stream", failures: fixtureCloseFailures{interaction: errCloseSentinel}},
		{name: "session", failures: fixtureCloseFailures{session: errCloseSentinel}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			connector := &fixtureConnector{closeFailures: test.failures}
			err := Run(t.Context(), connector, Config{
				Initialize:       initializeRequestFixture(t),
				Waiting:          waitingRequestFixture(t, "waiting-close-error"),
				OperationTimeout: time.Second,
			})
			if !errors.Is(err, errCloseSentinel) {
				t.Fatalf("Run error = %v, want close sentinel", err)
			}
		})
	}
}

var errCloseSentinel = errors.New("fixture close sentinel")

func TestValidateConnectionRejectsZeroAndReconnectRequests(t *testing.T) {
	t.Parallel()
	if err := validateConnection(client.InitializeRequest{}, client.Connection{}); err == nil {
		t.Fatal("zero connection contract succeeded")
	}
	request := initializeRequestFixture(t)
	claim, err := client.NewReconnectClaim("conformance-client", 1)
	if err != nil {
		t.Fatal(err)
	}
	reconnect, err := client.NewLegacyReconnectRequest(
		request.Protocol(), request.Client(), request.SupportedCapabilities(),
		request.RequiredCapabilities(), request.RequestedLimits(), claim,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateConnection(reconnect, client.Connection{}); err == nil {
		t.Fatal("reconnect validation succeeded")
	}
}

type rejectingConnector struct{}

func (rejectingConnector) Initialize(context.Context, client.InitializeRequest) (client.Session, error) {
	return nil, context.Canceled
}

type fixtureConnector struct {
	mu              sync.Mutex
	current         *fixtureSession
	initializations int
	closeFailures   fixtureCloseFailures
}

type fixtureCloseFailures struct {
	event       error
	interaction error
	session     error
}

func (connector *fixtureConnector) Initialize(
	ctx context.Context,
	request client.InitializeRequest,
) (client.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	connector.mu.Lock()
	defer connector.mu.Unlock()
	epoch := uint64(1)
	if claim, reconnect := request.Reconnect(); reconnect {
		if connector.current == nil || claim.ClientID() != connector.current.connection.ClientID() {
			return nil, errors.New("unknown reconnect identity")
		}
		epoch = claim.NextOwnershipEpoch()
		_ = connector.current.Close()
	}
	session := &fixtureSession{
		connection:    connectionFixture(request, epoch),
		closeFailures: connector.closeFailures,
	}
	connector.current = session
	connector.initializations++
	return session, nil
}

func connectionFixture(request client.InitializeRequest, epoch uint64) client.Connection {
	limits := request.RequestedLimits()
	health, err := client.NewHealth(client.HealthReady, nil, 0, limits)
	if err != nil {
		panic(err)
	}
	reference, err := client.NewDefinitionRef("conformance", "revision-1")
	if err != nil {
		panic(err)
	}
	definition, err := client.NewDefinition(reference, "scripted", 2)
	if err != nil {
		panic(err)
	}
	catalog, err := client.NewCatalog("catalog-1", []client.Definition{definition}, limits)
	if err != nil {
		panic(err)
	}
	server, err := client.NewBuild("conformance-server", "test", "commit", "go1.26.5")
	if err != nil {
		panic(err)
	}
	connection, err := client.NewConnection(client.ConnectionSpec{
		Protocol: request.Protocol().Maximum(), Server: server,
		Capabilities: request.RequiredCapabilities(), Limits: limits, Health: health,
		ClientID: "conformance-client", OwnershipEpoch: epoch, Catalog: catalog,
	})
	if err != nil {
		panic(err)
	}
	return connection
}

type fixtureSession struct {
	mu            sync.Mutex
	connection    client.Connection
	closed        bool
	interaction   *fixtureInteractionStream
	closeFailures fixtureCloseFailures
	activeRun     client.RunRef
}

func (session *fixtureSession) Connection() client.Connection { return session.connection }

func (session *fixtureSession) Start(ctx context.Context, request client.StartRequest) (client.StartResult, error) {
	if err := session.operationError(ctx); err != nil {
		return client.StartResult{}, err
	}
	runID := "conformance-run"
	if request.Operation().String() != "client-conformance-start" {
		runID = "conformance-waiting-run"
	}
	run, err := client.NewRunRef(runID)
	if err != nil {
		return client.StartResult{}, err
	}
	if runID == "conformance-waiting-run" {
		session.mu.Lock()
		session.activeRun = run
		session.mu.Unlock()
	}
	return client.NewStartResult(run, 1, "conformance-plan", false)
}

func (session *fixtureSession) Events(
	ctx context.Context,
	cursor client.Cursor,
	_ client.EventStreamOptions,
) (client.EventStream, error) {
	if err := session.operationError(ctx); err != nil {
		return nil, err
	}
	kind := client.EventRunCompleted
	if cursor.Run().ID() == "conformance-waiting-run" {
		kind = client.EventRunCancelled
	}
	detail := client.NoEventDetail()
	if kind == client.EventRunCancelled {
		var err error
		detail, err = client.NewStatusDetail("cancelled")
		if err != nil {
			return nil, err
		}
	}
	event, err := client.NewEvent(cursor.Run(), 1, time.Unix(1, 0), kind, detail)
	if err != nil {
		return nil, err
	}
	frame, err := client.NewEventFrame(event)
	if err != nil {
		return nil, err
	}
	return &fixtureEventStream{frame: frame, closeErr: session.closeFailures.event}, nil
}

func (session *fixtureSession) Interactions(
	ctx context.Context,
	_ client.InteractionStreamOptions,
) (client.InteractionStream, error) {
	if err := session.operationError(ctx); err != nil {
		return nil, err
	}
	update, err := client.NewInteractionSnapshot(0, nil, session.connection.Limits())
	if err != nil {
		return nil, err
	}
	first, err := client.NewInteractionFrame(update)
	if err != nil {
		return nil, err
	}
	control, err := client.NewInteractionControl(0, 0, false, true)
	if err != nil {
		return nil, err
	}
	second, err := client.NewInteractionControlFrame(control)
	if err != nil {
		return nil, err
	}
	stream := &fixtureInteractionStream{
		frames: []client.InteractionFrame{first, second}, closed: make(chan struct{}),
		closeErr: session.closeFailures.interaction,
	}
	session.mu.Lock()
	session.interaction = stream
	session.mu.Unlock()
	return stream, nil
}

func (session *fixtureSession) Health(ctx context.Context) (client.Health, error) {
	if err := session.operationError(ctx); err != nil {
		return client.Health{}, err
	}
	session.mu.Lock()
	active := uint64(0)
	if session.activeRun.ID() != "" {
		active = 1
	}
	session.mu.Unlock()
	return client.NewHealth(client.HealthReady, nil, active, session.connection.Limits())
}

func (session *fixtureSession) Close() error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return nil
	}
	session.closed = true
	if session.interaction != nil {
		_ = session.interaction.Close()
	}
	return session.closeFailures.session
}

func (session *fixtureSession) operationError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return client.ErrClosed
	}
	return nil
}

func (session *fixtureSession) Cancel(ctx context.Context, request client.CancelRequest) (client.CancelResult, error) {
	if err := session.operationError(ctx); err != nil {
		return client.CancelResult{}, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if request.Run() != session.activeRun {
		return client.CancelResult{}, errors.New("unknown active run")
	}
	session.activeRun = client.RunRef{}
	return client.NewCancelResult(true, false, 0)
}

func (*fixtureSession) Respond(context.Context, client.RespondRequest) (client.RespondResult, error) {
	panic("unexpected Respond")
}

func (*fixtureSession) Suspend(context.Context, client.RunMutation) (client.SuspendResult, error) {
	panic("unexpected Suspend")
}

func (*fixtureSession) Resume(context.Context, client.RunMutation) (client.ResumeResult, error) {
	panic("unexpected Resume")
}

func (*fixtureSession) Export(context.Context, client.RunRef) (client.Snapshot, error) {
	panic("unexpected Export")
}

func (*fixtureSession) Import(context.Context, client.ImportRequest) (client.ImportResult, error) {
	panic("unexpected Import")
}

type fixtureEventStream struct {
	mu       sync.Mutex
	frame    client.EventFrame
	closed   bool
	read     bool
	closeErr error
	nextErr  error
}

func (stream *fixtureEventStream) Next(ctx context.Context) (client.EventFrame, error) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return client.EventFrame{}, err
	}
	if stream.closed {
		return client.EventFrame{}, client.ErrClosed
	}
	if stream.read {
		return client.EventFrame{}, errors.New("event fixture exhausted")
	}
	stream.read = true
	return stream.frame, nil
}

func (stream *fixtureEventStream) Close() error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.closed = true
	return stream.closeErr
}

type fixtureInteractionStream struct {
	mu       sync.Mutex
	frames   []client.InteractionFrame
	closed   chan struct{}
	once     sync.Once
	closeErr error
}

func (stream *fixtureInteractionStream) Next(ctx context.Context) (client.InteractionFrame, error) {
	stream.mu.Lock()
	if len(stream.frames) > 0 {
		frame := stream.frames[0]
		stream.frames = stream.frames[1:]
		stream.mu.Unlock()
		return frame, nil
	}
	stream.mu.Unlock()
	select {
	case <-ctx.Done():
		return client.InteractionFrame{}, ctx.Err()
	case <-stream.closed:
		return client.InteractionFrame{}, client.ErrClosed
	}
}

func (stream *fixtureInteractionStream) Close() error {
	stream.once.Do(func() { close(stream.closed) })
	return stream.closeErr
}

func initializeRequestFixture(t *testing.T) client.InitializeRequest {
	t.Helper()
	version, err := client.NewProtocolVersion(1, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	protocol, err := client.NewProtocolRange(version, version)
	if err != nil {
		t.Fatal(err)
	}
	build, err := client.NewBuild("client-conformance", "test", "commit", "go1.26.5")
	if err != nil {
		t.Fatal(err)
	}
	limits, err := client.NewLimits(1<<20, 16, 16, 1<<20, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	request, err := client.NewLegacyInitializeRequest(protocol, build, []string{"events"}, []string{"events"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func waitingRequestFixture(t *testing.T, operationValue string) client.StartRequest {
	t.Helper()
	operation, err := client.NewOperationID(operationValue)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := client.NewDefinitionRef("conformance", "revision-1")
	if err != nil {
		t.Fatal(err)
	}
	input, err := client.NewInput("conformance-waiting-message", "wait for cancellation")
	if err != nil {
		t.Fatal(err)
	}
	request, err := client.NewStartRequest(operation, reference, input)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

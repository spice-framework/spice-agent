package conformance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/spice-framework/spice-agent/client"
)

const (
	defaultOperationTimeout = 5 * time.Second
	minimumOperationTimeout = 100 * time.Millisecond
	maximumOperationTimeout = 30 * time.Second
	maximumEventFrames      = 1024
)

// Config describes one disposable client conformance session. Initialize must
// be a fresh-owner request. Waiting must start a fixture run that remains
// active until Session.Cancel is called. OperationTimeout bounds every blocking
// operation; zero selects five seconds.
type Config struct {
	Initialize       client.InitializeRequest
	Waiting          client.StartRequest
	OperationTimeout time.Duration
}

func validateConnection(request client.InitializeRequest, connection client.Connection) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("client conformance initialize request: %w", err)
	}
	if _, reconnect := request.Reconnect(); reconnect {
		return errors.New("client conformance validation requires a fresh-owner request")
	}
	if err := connection.Validate(); err != nil {
		return fmt.Errorf("client conformance connection: %w", err)
	}
	if !protocolContains(request.Protocol(), connection.Protocol()) {
		return errors.New("client conformance negotiated protocol is outside the requested range")
	}
	capabilities := connection.Capabilities()
	for _, required := range request.RequiredCapabilities() {
		if _, found := slices.BinarySearch(capabilities, required); !found {
			return errors.New("client conformance connection omitted a required capability")
		}
	}
	if !limitsWithin(connection.Limits(), request.RequestedLimits()) {
		return errors.New("client conformance negotiated limits exceed the request")
	}
	if connection.Health().Limits() != connection.Limits() {
		return errors.New("client conformance connection and health limits differ")
	}
	if len(connection.Catalog().Definitions()) == 0 {
		return errors.New("client conformance connection has no definitions")
	}
	return nil
}

// Run exercises initialization, health, one complete run, event replay,
// interaction snapshot/tail controls, cancellation, reconnect fencing, and
// idempotent local close. It is destructive to the supplied fixture ownership
// epoch and must be given a fresh Connector for every invocation.
func Run(ctx context.Context, connector client.Connector, config Config) error {
	if ctx == nil || connector == nil {
		return errors.New("client conformance context and connector are required")
	}
	if _, reconnect := config.Initialize.Reconnect(); reconnect {
		return errors.New("client conformance run requires a fresh-owner initialize request")
	}
	timeout := config.OperationTimeout
	if timeout == 0 {
		timeout = defaultOperationTimeout
	}
	if timeout < minimumOperationTimeout || timeout > maximumOperationTimeout {
		return errors.New("client conformance operation timeout is outside supported bounds")
	}
	if err := config.Initialize.Validate(); err != nil {
		return fmt.Errorf("client conformance initialize request: %w", err)
	}
	if err := config.Waiting.Validate(); err != nil {
		return fmt.Errorf("client conformance waiting start request: %w", err)
	}
	if config.Waiting.Operation().String() == "client-conformance-start" {
		return errors.New("client conformance waiting start operation is reserved")
	}
	return runLifecycle(ctx, connector, config.Initialize, config.Waiting, timeout)
}

func runLifecycle(
	ctx context.Context,
	connector client.Connector,
	request client.InitializeRequest,
	waiting client.StartRequest,
	timeout time.Duration,
) (result error) {
	initial, err := initialize(ctx, connector, request, timeout)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, closeContract("initial session", initial))
	}()
	if err = validateConnection(request, initial.Connection()); err != nil {
		return err
	}
	if err = requireReadyHealth(ctx, initial, timeout); err != nil {
		return err
	}
	if err = runOneTurn(ctx, initial, timeout); err != nil {
		return err
	}
	if err = runCancellation(ctx, initial, waiting, timeout); err != nil {
		return err
	}

	updates, err := openAndPrimeInteractions(ctx, initial, timeout)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, closeContract("interaction stream", updates))
	}()
	nextResult := make(chan error, 1)
	blockedRead, cancelBlockedRead := context.WithTimeout(ctx, timeout)
	defer cancelBlockedRead()
	go func() {
		_, nextErr := updates.Next(blockedRead)
		nextResult <- nextErr
	}()

	reconnect, err := reconnectRequest(request, initial.Connection())
	if err != nil {
		return err
	}
	reconnected, err := initialize(ctx, connector, reconnect, timeout)
	if err != nil {
		return fmt.Errorf("client conformance reconnect: %w", err)
	}
	defer func() {
		result = errors.Join(result, closeContract("reconnected session", reconnected))
	}()
	if err = validateReconnect(initial.Connection(), reconnected.Connection()); err != nil {
		return err
	}
	if _, healthErr := initial.Health(ctx); !errors.Is(healthErr, client.ErrClosed) {
		return fmt.Errorf("client conformance old session health did not return ErrClosed: %w", healthErr)
	}
	if err = awaitClosedRead(nextResult, timeout); err != nil {
		return err
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, healthErr := reconnected.Health(cancelled); !errors.Is(healthErr, context.Canceled) {
		return fmt.Errorf("client conformance cancelled health did not return context cancellation: %w", healthErr)
	}
	if err = closeContract("reconnected session", reconnected); err != nil {
		return err
	}
	if err = closeContract("reconnected session repeated close", reconnected); err != nil {
		return err
	}
	if _, healthErr := reconnected.Health(ctx); !errors.Is(healthErr, client.ErrClosed) {
		return fmt.Errorf("client conformance closed session health did not return ErrClosed: %w", healthErr)
	}
	return nil
}

func initialize(
	ctx context.Context,
	connector client.Connector,
	request client.InitializeRequest,
	timeout time.Duration,
) (client.Session, error) {
	operation, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	session, err := connector.Initialize(operation, request)
	if err != nil {
		return nil, fmt.Errorf("client conformance initialize: %w", err)
	}
	if session == nil {
		return nil, errors.New("client conformance initialize returned a nil session")
	}
	return session, nil
}

func requireReadyHealth(ctx context.Context, session client.Session, timeout time.Duration) error {
	operation, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	health, err := session.Health(operation)
	if err != nil {
		return fmt.Errorf("client conformance health: %w", err)
	}
	if err = health.Validate(); err != nil {
		return fmt.Errorf("client conformance health value: %w", err)
	}
	if health.State() != client.HealthReady {
		return fmt.Errorf("client conformance health state is %q, want ready", health.State())
	}
	return nil
}

func runOneTurn(ctx context.Context, session client.Session, timeout time.Duration) (result error) {
	definitions := session.Connection().Catalog().Definitions()
	operationID, err := client.NewOperationID("client-conformance-start")
	if err != nil {
		return err
	}
	input, err := client.NewInput("client-conformance-message", "complete the conformance turn")
	if err != nil {
		return err
	}
	request, err := client.NewStartRequest(operationID, definitions[0].Ref(), input)
	if err != nil {
		return err
	}
	operation, cancel := context.WithTimeout(ctx, timeout)
	started, err := session.Start(operation, request)
	cancel()
	if err != nil {
		return fmt.Errorf("client conformance start: %w", err)
	}
	cursor, err := client.NewCursor(started.Run(), 0)
	if err != nil {
		return err
	}
	options, err := client.NewEventStreamOptions(
		min(session.Connection().Limits().ReplayEvents(), uint32(maximumEventFrames)),
		true,
		session.Connection().Limits(),
	)
	if err != nil {
		return err
	}
	operation, cancel = context.WithTimeout(ctx, timeout)
	stream, err := session.Events(operation, cursor, options)
	if err != nil {
		cancel()
		return fmt.Errorf("client conformance events: %w", err)
	}
	defer func() {
		result = errors.Join(result, closeContract("completed event stream", stream))
		cancel()
	}()
	for range maximumEventFrames {
		frame, nextErr := stream.Next(operation)
		if nextErr != nil {
			return fmt.Errorf("client conformance event: %w", nextErr)
		}
		value, ok := frame.Event()
		if ok && value.Kind() == client.EventRunCompleted {
			return nil
		}
	}
	return errors.New("client conformance event stream exceeded its frame bound")
}

func runCancellation(
	ctx context.Context,
	session client.Session,
	request client.StartRequest,
	timeout time.Duration,
) (result error) {
	operation, cancel := context.WithTimeout(ctx, timeout)
	started, err := session.Start(operation, request)
	cancel()
	if err != nil {
		return fmt.Errorf("client conformance waiting start: %w", err)
	}
	cursor, err := client.NewCursor(started.Run(), 0)
	if err != nil {
		return err
	}
	options, err := client.NewEventStreamOptions(
		min(session.Connection().Limits().ReplayEvents(), uint32(maximumEventFrames)),
		true,
		session.Connection().Limits(),
	)
	if err != nil {
		return err
	}
	operation, cancel = context.WithTimeout(ctx, timeout)
	stream, err := session.Events(operation, cursor, options)
	if err != nil {
		cancel()
		return fmt.Errorf("client conformance cancelled events: %w", err)
	}
	defer func() {
		result = errors.Join(result, closeContract("cancelled event stream", stream))
		cancel()
	}()
	cancelOperation, err := client.NewOperationID("client-conformance-cancel")
	if err != nil {
		return err
	}
	cancelRequest, err := client.NewCancelRequest(
		started.Run(), cancelOperation, "client conformance cancellation",
	)
	if err != nil {
		return err
	}
	mutation, cancelMutation := context.WithTimeout(ctx, timeout)
	cancelled, err := session.Cancel(mutation, cancelRequest)
	cancelMutation()
	if err != nil {
		return fmt.Errorf("client conformance cancel: %w", err)
	}
	if !cancelled.Requested() || cancelled.AlreadyTerminal() || cancelled.TerminalSequence() != 0 {
		return errors.New("client conformance cancel did not request a nonterminal run cancellation")
	}
	for range maximumEventFrames {
		frame, nextErr := stream.Next(operation)
		if nextErr != nil {
			return fmt.Errorf("client conformance cancelled event: %w", nextErr)
		}
		value, ok := frame.Event()
		if !ok {
			continue
		}
		switch value.Kind() {
		case client.EventRunCancelled:
			return requireDrainedHealth(ctx, session, timeout)
		case client.EventRunCompleted, client.EventRunFailed:
			return fmt.Errorf("client conformance waiting run terminated as %q, want cancelled", value.Kind())
		default:
			continue
		}
	}
	return errors.New("client conformance cancelled event stream exceeded its frame bound")
}

func requireDrainedHealth(ctx context.Context, session client.Session, timeout time.Duration) error {
	operation, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		health, err := session.Health(operation)
		if err != nil {
			return fmt.Errorf("client conformance drained health: %w", err)
		}
		if err = health.Validate(); err != nil {
			return fmt.Errorf("client conformance drained health value: %w", err)
		}
		if health.State() == client.HealthReady && health.ActiveRuns() == 0 {
			return nil
		}
		select {
		case <-operation.Done():
			return fmt.Errorf(
				"client conformance active runs did not drain from state %q with %d active runs: %w",
				health.State(), health.ActiveRuns(), operation.Err(),
			)
		case <-ticker.C:
		}
	}
}

func openAndPrimeInteractions(
	ctx context.Context,
	session client.Session,
	timeout time.Duration,
) (client.InteractionStream, error) {
	operation, cancel := context.WithTimeout(ctx, timeout)
	stream, err := session.Interactions(operation, client.NewInteractionStreamOptions(true))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("client conformance interactions: %w", err)
	}
	first, err := stream.Next(operation)
	if err != nil {
		cancel()
		return nil, errors.Join(
			fmt.Errorf("client conformance interaction snapshot: %w", err),
			closeContract("interaction stream", stream),
		)
	}
	second, err := stream.Next(operation)
	cancel()
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("client conformance interaction control: %w", err),
			closeContract("interaction stream", stream),
		)
	}
	if first.Kind() != client.InteractionFrameUpdate || second.Kind() != client.InteractionFrameControl {
		return nil, errors.Join(
			errors.New("client conformance interactions omitted the complete snapshot/control prefix"),
			closeContract("interaction stream", stream),
		)
	}
	return stream, nil
}

type contractCloser interface {
	Close() error
}

func closeContract(label string, closer contractCloser) error {
	if err := closer.Close(); err != nil {
		return fmt.Errorf("client conformance %s close: %w", label, err)
	}
	return nil
}

func reconnectRequest(
	initial client.InitializeRequest,
	connection client.Connection,
) (client.InitializeRequest, error) {
	claim, err := client.NewReconnectClaim(connection.ClientID(), connection.OwnershipEpoch())
	if err != nil {
		return client.InitializeRequest{}, err
	}
	if _, hasAttempt := initial.AttemptID(); !hasAttempt {
		return client.NewLegacyReconnectRequest(
			initial.Protocol(), initial.Client(), initial.SupportedCapabilities(),
			initial.RequiredCapabilities(), initial.RequestedLimits(), claim,
		)
	}
	attempt, err := client.NewInitializationAttemptID()
	if err != nil {
		return client.InitializeRequest{}, err
	}
	return client.NewReconnectRequestWithAttempt(
		initial.Protocol(), initial.Client(), initial.SupportedCapabilities(),
		initial.RequiredCapabilities(), initial.RequestedLimits(), claim, attempt,
	)
}

func validateReconnect(previous, current client.Connection) error {
	if err := current.Validate(); err != nil {
		return fmt.Errorf("client conformance reconnected connection: %w", err)
	}
	if current.ClientID() != previous.ClientID() ||
		current.OwnershipEpoch() != previous.OwnershipEpoch()+1 {
		return errors.New("client conformance reconnect did not preserve identity and increment ownership")
	}
	return nil
}

func awaitClosedRead(result <-chan error, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-result:
		if !errors.Is(err, client.ErrClosed) {
			return fmt.Errorf("client conformance old stream read did not return ErrClosed: %w", err)
		}
		return nil
	case <-timer.C:
		return errors.New("client conformance reconnect did not join the old stream")
	}
}

func protocolContains(current client.ProtocolRange, version client.ProtocolVersion) bool {
	return compareVersion(current.Minimum(), version) <= 0 && compareVersion(version, current.Maximum()) <= 0
}

func compareVersion(left, right client.ProtocolVersion) int {
	for _, pair := range [][2]uint32{
		{left.Major(), right.Major()},
		{left.Minor(), right.Minor()},
		{left.Patch(), right.Patch()},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

func limitsWithin(actual, requested client.Limits) bool {
	return actual.MessageBytes() <= requested.MessageBytes() &&
		actual.CollectionItems() <= requested.CollectionItems() &&
		actual.ReplayEvents() <= requested.ReplayEvents() &&
		actual.ReplayBytes() <= requested.ReplayBytes() &&
		actual.ConcurrentStreams() <= requested.ConcurrentStreams() &&
		actual.ActiveRuns() <= requested.ActiveRuns()
}

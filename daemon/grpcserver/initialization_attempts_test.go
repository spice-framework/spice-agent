package grpcserver

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func TestInitializationAttemptFingerprintExcludesOnlyAttemptIdentity(t *testing.T) {
	t.Parallel()
	request := grpcInitializeAttemptRequest(clientLimitsFixture(t), attemptID(1))
	first, err := fingerprintInitializeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	request.InitializationAttemptId = attemptID(2)
	second, err := fingerprintInitializeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("attempt identity changed the semantic request fingerprint")
	}
	request.Client.Component = "different-client"
	different, err := fingerprintInitializeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if first == different {
		t.Fatal("semantic request mutation did not change the fingerprint")
	}
}

func TestInitializationAttemptCommitReplaysExactResponseAndRejectsConflict(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 1, 1)
	session := freshRegistrySession(t, store)
	id := attemptID(3)
	response := registryAttemptResponse(session, id)
	if err := registry.installFresh(session, response); err != nil {
		t.Fatal(err)
	}
	request := grpcInitializeAttemptRequest(clientLimitsFixture(t), id)
	fingerprint, err := fingerprintInitializeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	lease, replay, err := registry.reserveInitializationAttempt(t.Context(), id, fingerprint)
	if err != nil || replay != nil || lease == nil {
		t.Fatalf("initial reservation = %#v, %#v, %v", lease, replay, err)
	}
	if err = lease.commit(response, false); err != nil {
		t.Fatal(err)
	}
	_, replay, err = registry.reserveInitializationAttempt(t.Context(), id, fingerprint)
	if err != nil || !proto.Equal(replay, response) {
		t.Fatalf("replay = %#v, %v", replay, err)
	}
	replay.ClientId = "caller-mutation"
	_, secondReplay, err := registry.reserveInitializationAttempt(t.Context(), id, fingerprint)
	if err != nil || secondReplay.GetClientId() != session.ClientID() {
		t.Fatalf("defensive replay = %#v, %v", secondReplay, err)
	}
	conflicting := request
	conflicting.Client = proto.CloneOf(request.Client)
	conflicting.Client.Component = "other-client"
	conflictFingerprint, err := fingerprintInitializeRequest(conflicting)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = registry.reserveInitializationAttempt(t.Context(), id, conflictFingerprint); !errors.Is(
		err, errInitializationAttemptConflict,
	) {
		t.Fatalf("conflicting attempt = %v", err)
	}
}

func TestInitializationAttemptDuplicatesCoalesceAndWaitersAreBounded(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 1, 1)
	session := freshRegistrySession(t, store)
	id := attemptID(4)
	response := registryAttemptResponse(session, id)
	if err := registry.installFresh(session, response); err != nil {
		t.Fatal(err)
	}
	request := grpcInitializeAttemptRequest(clientLimitsFixture(t), id)
	fingerprint, err := fingerprintInitializeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := registry.reserveInitializationAttempt(t.Context(), id, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	results := make(chan *enginev1.InitializeResponse, maximumInitializationWaitersPerAttempt)
	errorsFound := make(chan error, maximumInitializationWaitersPerAttempt)
	for range maximumInitializationWaitersPerAttempt {
		go func() {
			_, replay, reserveErr := registry.reserveInitializationAttempt(ctx, id, fingerprint)
			results <- replay
			errorsFound <- reserveErr
		}()
	}
	waitForAttemptWaiters(t, registry, string(id), maximumInitializationWaitersPerAttempt)
	if _, _, overflow := registry.reserveInitializationAttempt(t.Context(), id, fingerprint); !errors.Is(
		overflow, errNegotiatedSessionWaiterLimit,
	) {
		t.Fatalf("overflow waiter = %v", overflow)
	}
	if err = owner.commit(response, false); err != nil {
		t.Fatal(err)
	}
	for range maximumInitializationWaitersPerAttempt {
		if waiterErr := <-errorsFound; waiterErr != nil {
			t.Fatal(waiterErr)
		}
		if replay := <-results; !proto.Equal(replay, response) {
			t.Fatalf("coalesced replay = %#v", replay)
		}
	}
}

func TestInitializationAttemptCancellationAbortAndPendingCapacity(t *testing.T) {
	t.Parallel()
	_, registry := newNegotiatedRegistryFixture(t, 1, 1)
	request := grpcInitializeAttemptRequest(clientLimitsFixture(t), attemptID(5))
	fingerprint, err := fingerprintInitializeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := registry.reserveInitializationAttempt(t.Context(), request.GetInitializationAttemptId(), fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	other := grpcInitializeAttemptRequest(clientLimitsFixture(t), attemptID(6))
	otherFingerprint, err := fingerprintInitializeRequest(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, reserveErr := registry.reserveInitializationAttempt(
		t.Context(), other.GetInitializationAttemptId(), otherFingerprint,
	); !errors.Is(reserveErr, errInitializationAttemptCapacity) {
		t.Fatalf("pending capacity = %v", reserveErr)
	}

	waitContext, cancel := context.WithCancel(t.Context())
	waiterDone := make(chan error, 1)
	go func() {
		_, _, waitErr := registry.reserveInitializationAttempt(
			waitContext, request.GetInitializationAttemptId(), fingerprint,
		)
		waiterDone <- waitErr
	}()
	waitForAttemptWaiters(t, registry, string(request.GetInitializationAttemptId()), 1)
	cancel()
	if waitErr := <-waiterDone; !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("canceled waiter = %v", waitErr)
	}
	owner.abort()
	replacement, replay, err := registry.reserveInitializationAttempt(
		t.Context(), request.GetInitializationAttemptId(), fingerprint,
	)
	if err != nil || replacement == nil || replay != nil {
		t.Fatalf("reservation after pre-commit abort = %#v, %#v, %v", replacement, replay, err)
	}
	replacement.abort()
}

func TestInitializationAttemptCancellationSeparatesPreAndPostCommit(t *testing.T) {
	t.Parallel()
	t.Run("fresh pre-commit abort", func(t *testing.T) {
		t.Parallel()
		store, registry := newNegotiatedRegistryFixture(t, 1, 1)
		id := attemptID(13)
		request := grpcInitializeAttemptRequest(clientLimitsFixture(t), id)
		fingerprint, err := fingerprintInitializeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		lease, _, err := registry.reserveInitializationAttempt(t.Context(), id, fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		called := false
		response, transactionErr := registry.initializeFreshAttempt(ctx, lease, func() (
			daemon.Session, *enginev1.InitializeResponse, error,
		) {
			called = true
			session, freshErr := store.Fresh()
			return session, registryAttemptResponse(session, id), freshErr
		})
		if !errors.Is(transactionErr, context.Canceled) || response != nil || called {
			t.Fatalf("pre-commit cancellation = %#v, called %t, %v", response, called, transactionErr)
		}
		lease.abort()
		replacement, replay, err := registry.reserveInitializationAttempt(t.Context(), id, fingerprint)
		if err != nil || replacement == nil || replay != nil {
			t.Fatalf("reservation after cancellation = %#v, %#v, %v", replacement, replay, err)
		}
		replacement.abort()
	})

	t.Run("fresh post-commit replay", func(t *testing.T) {
		t.Parallel()
		store, registry := newNegotiatedRegistryFixture(t, 1, 1)
		id := attemptID(14)
		request := grpcInitializeAttemptRequest(clientLimitsFixture(t), id)
		fingerprint, err := fingerprintInitializeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		lease, _, err := registry.reserveInitializationAttempt(t.Context(), id, fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		response, transactionErr := registry.initializeFreshAttempt(ctx, lease, func() (
			daemon.Session, *enginev1.InitializeResponse, error,
		) {
			session, freshErr := store.Fresh()
			cancel()
			return session, registryAttemptResponse(session, id), freshErr
		})
		if !errors.Is(transactionErr, context.Canceled) || response == nil {
			t.Fatalf("post-commit cancellation = %#v, %v", response, transactionErr)
		}
		_, replay, err := registry.reserveInitializationAttempt(t.Context(), id, fingerprint)
		if err != nil || !proto.Equal(replay, response) {
			t.Fatalf("post-commit replay = %#v, %v", replay, err)
		}
	})

	t.Run("reconnect post-commit replay", func(t *testing.T) {
		t.Parallel()
		store, registry := newNegotiatedRegistryFixture(t, 1, 1)
		first := freshRegistrySession(t, store)
		if err := registry.installFresh(first, registryInitializeResponse(first)); err != nil {
			t.Fatal(err)
		}
		id := attemptID(15)
		response := registryAttemptResponseForOwnership(first.ClientID(), first.Epoch()+1, id)
		lease, _, err := reserveRegistryAttempt(registry, id, response)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		committed, transactionErr := registry.initializeReconnectAttempt(
			ctx, lease, first.ClientID(), first.Epoch(), response,
			func() (daemon.Session, error) {
				next, reconnectErr := store.ReconnectContext(ctx, first.ClientID(), first.Epoch())
				cancel()
				return next, reconnectErr
			},
		)
		if !errors.Is(transactionErr, context.Canceled) || committed == nil {
			t.Fatalf("post-CAS cancellation = %#v, %v", committed, transactionErr)
		}
		request := grpcInitializeAttemptRequest(clientLimitsFromWire(response.GetLimits()), id)
		request.ReconnectClaim = &enginev1.ReconnectClaim{
			ClientId: first.ClientID(), ExpectedOwnershipEpoch: first.Epoch(),
		}
		fingerprint, err := fingerprintInitializeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		_, replay, err := registry.reserveInitializationAttempt(t.Context(), id, fingerprint)
		if err != nil || !proto.Equal(replay, committed) {
			t.Fatalf("post-CAS replay = %#v, %v", replay, err)
		}
	})
}

func TestInitializationAttemptRetentionKeepsCreationAndLatestReconnect(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 1, 1)
	current := freshRegistrySession(t, store)
	creationID := attemptID(7)
	creationResponse := registryAttemptResponse(current, creationID)
	if err := registry.installFresh(current, creationResponse); err != nil {
		t.Fatal(err)
	}
	commitRegistryAttempt(t, registry, creationID, creationResponse, false)
	var previousReconnect string
	for sequence := byte(8); sequence <= 9; sequence++ {
		id := attemptID(sequence)
		nextResponse := registryAttemptResponseForOwnership(current.ClientID(), current.Epoch()+1, id)
		lease, _, err := reserveRegistryAttempt(registry, id, nextResponse)
		if err != nil {
			t.Fatal(err)
		}
		var nextSession daemon.Session
		next, err := registry.initializeReconnect(
			t.Context(), current.ClientID(), current.Epoch(), nextResponse,
			func() (daemon.Session, error) {
				var reconnectErr error
				nextSession, reconnectErr = store.ReconnectContext(t.Context(), current.ClientID(), current.Epoch())
				return nextSession, reconnectErr
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err = lease.commit(next, true); err != nil {
			t.Fatal(err)
		}
		current = nextSession
		registry.mu.Lock()
		if previousReconnect != "" {
			if _, retained := registry.attempts[previousReconnect]; retained {
				registry.mu.Unlock()
				t.Fatal("superseded reconnect attempt remained retained")
			}
		}
		if len(registry.attempts) != 2 {
			registry.mu.Unlock()
			t.Fatalf("retained attempt count = %d", len(registry.attempts))
		}
		registry.mu.Unlock()
		previousReconnect = string(id)
	}
	registry.mu.Lock()
	_, creationRetained := registry.attempts[string(creationID)]
	registry.mu.Unlock()
	if !creationRetained {
		t.Fatal("creation attempt was not retained with the active session")
	}
}

func TestReconnectAttemptReplayIsAtomicWithPublishedOwnership(t *testing.T) {
	t.Parallel()
	store, registry := newNegotiatedRegistryFixture(t, 1, 1)
	first := freshRegistrySession(t, store)
	if err := registry.installFresh(first, registryInitializeResponse(first)); err != nil {
		t.Fatal(err)
	}
	id := attemptID(12)
	response := registryAttemptResponseForOwnership(first.ClientID(), first.Epoch()+1, id)
	lease, _, err := reserveRegistryAttempt(registry, id, response)
	if err != nil {
		t.Fatal(err)
	}
	storeAdvanced := make(chan struct{})
	releaseOperation := make(chan struct{})
	transactionDone := make(chan error, 1)
	go func() {
		_, transactionErr := registry.initializeReconnectAttempt(
			t.Context(), lease, first.ClientID(), first.Epoch(), response,
			func() (daemon.Session, error) {
				next, reconnectErr := store.ReconnectContext(t.Context(), first.ClientID(), first.Epoch())
				close(storeAdvanced)
				<-releaseOperation
				return next, reconnectErr
			},
		)
		transactionDone <- transactionErr
	}()
	receiveInitializationTestValue(t, storeAdvanced)

	type observedCommit struct {
		response *enginev1.InitializeResponse
		linked   bool
		terminal bool
		timedOut bool
	}
	observed := make(chan observedCommit, 1)
	go func() {
		deadline := time.NewTimer(time.Second)
		defer deadline.Stop()
		for {
			select {
			case <-deadline.C:
				observed <- observedCommit{timedOut: true}
				return
			default:
			}
			registry.mu.Lock()
			entry := registry.entries[first.ClientID()]
			if entry.session.Epoch() == first.Epoch()+1 {
				record := registry.attempts[string(id)]
				value := observedCommit{
					linked: entry.reconnectAttempt == string(id),
				}
				if record != nil {
					value.terminal = record.terminal
					value.response = proto.CloneOf(record.response)
				}
				registry.mu.Unlock()
				observed <- value
				return
			}
			registry.mu.Unlock()
			time.Sleep(time.Millisecond)
		}
	}()
	close(releaseOperation)
	commit := receiveInitializationTestValue(t, observed)
	if commit.timedOut || !commit.linked || !commit.terminal || !proto.Equal(commit.response, response) {
		t.Fatalf("published ownership lacked atomic replay = %#v", commit)
	}
	if err = receiveInitializationTestValue(t, transactionDone); err != nil {
		t.Fatal(err)
	}

	// An immediately following reconnect may advance ownership only after the
	// prior response is already replayable; it cannot observe an attachment gap.
	thirdResponse := registryResponseForOwnership(first.ClientID(), first.Epoch()+2)
	if _, err = registry.initializeReconnect(
		t.Context(), first.ClientID(), first.Epoch()+1, thirdResponse,
		func() (daemon.Session, error) {
			return store.ReconnectContext(t.Context(), first.ClientID(), first.Epoch()+1)
		},
	); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := fingerprintInitializeRequest(func() *enginev1.InitializeRequest {
		request := grpcInitializeAttemptRequest(clientLimitsFromWire(response.GetLimits()), id)
		request.ReconnectClaim = &enginev1.ReconnectClaim{
			ClientId: first.ClientID(), ExpectedOwnershipEpoch: first.Epoch(),
		}
		return request
	}())
	if err != nil {
		t.Fatal(err)
	}
	_, replay, err := registry.reserveInitializationAttempt(t.Context(), id, fingerprint)
	if err != nil || !proto.Equal(replay, response) {
		t.Fatalf("replay after immediate next reconnect = %#v, %v", replay, err)
	}
}

func TestServerInitializationAttemptReplaysFreshAndReconnectBeforeDescribe(t *testing.T) {
	t.Parallel()
	root, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	build, limits, health, definitions := wireFixtureValues(t)
	description, err := daemon.NewRunHostDescription(definitions, health)
	if err != nil {
		t.Fatal(err)
	}
	host := &grpcFixtureHost{description: description, health: health}
	sessions, err := daemon.NewSessionStore(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	server, err := newServer(serverDependencies{
		root: root, token: endpointTokenFixture(t, 31), host: host, sessions: sessions,
		build: build, capabilities: []string{"events"}, maximumSessions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	connection := serveGRPCFixture(t, server)
	api := enginev1.NewEngineServiceClient(connection)
	authorization, _ := serverTokenAuthorization(t, 31)
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs(AuthenticationMetadataKey, authorization))
	describeBaseline := host.describeCalls.Load()

	fresh := grpcInitializeAttemptRequest(limits, attemptID(10))
	initialized, err := api.Initialize(ctx, fresh)
	if err != nil || enginev1.ValidateInitializeResponseForRequest(fresh, initialized) != nil {
		t.Fatalf("fresh = %#v, %v", initialized, err)
	}
	initialized.ClientId = "caller-mutation"
	replayedFresh, err := api.Initialize(ctx, fresh)
	if err != nil || replayedFresh.GetClientId() == "caller-mutation" ||
		host.describeCalls.Load() != describeBaseline+1 {
		t.Fatalf("fresh replay = %#v, %v, describe calls %d", replayedFresh, err, host.describeCalls.Load())
	}

	reconnect := grpcInitializeAttemptRequest(limits, attemptID(11))
	reconnect.ReconnectClaim = &enginev1.ReconnectClaim{
		ClientId: replayedFresh.GetClientId(), ExpectedOwnershipEpoch: replayedFresh.GetOwnershipEpoch(),
	}
	reconnected, err := api.Initialize(ctx, reconnect)
	if err != nil || enginev1.ValidateInitializeResponseForRequest(reconnect, reconnected) != nil {
		t.Fatalf("reconnect = %#v, %v", reconnected, err)
	}
	replayedReconnect, err := api.Initialize(ctx, reconnect)
	if err != nil || !proto.Equal(replayedReconnect, reconnected) ||
		host.describeCalls.Load() != describeBaseline+2 {
		t.Fatalf("reconnect replay = %#v, %v, describe calls %d", replayedReconnect, err, host.describeCalls.Load())
	}

	conflict := proto.CloneOf(reconnect)
	conflict.Client.Component = "conflicting-client"
	conflicting, err := api.Initialize(ctx, conflict)
	if err != nil || conflicting.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_CONFLICT ||
		host.describeCalls.Load() != describeBaseline+2 {
		t.Fatalf("conflict = %#v, %v, describe calls %d", conflicting, err, host.describeCalls.Load())
	}
}

func grpcInitializeAttemptRequest(limits client.Limits, id []byte) *enginev1.InitializeRequest {
	request := grpcInitializeRequest(limits)
	request.Protocol = &commonv1.ProtocolRange{
		Minimum: &commonv1.ProtocolVersion{Major: 1, Minor: 3},
		Maximum: &commonv1.ProtocolVersion{Major: 1, Minor: 3},
	}
	request.InitializationAttemptId = slices.Clone(id)
	return request
}

func attemptID(value byte) []byte {
	id := make([]byte, enginev1.InitializationAttemptIDBytes)
	id[len(id)-1] = value
	return id
}

func registryAttemptResponse(session daemon.Session, id []byte) *enginev1.InitializeResponse {
	return registryAttemptResponseForOwnership(session.ClientID(), session.Epoch(), id)
}

func registryAttemptResponseForOwnership(clientID string, epoch uint64, id []byte) *enginev1.InitializeResponse {
	response := registryResponseForOwnership(clientID, epoch)
	response.Protocol = &commonv1.ProtocolVersion{Major: 1, Minor: 3}
	response.InitializationAttemptId = slices.Clone(id)
	return response
}

func reserveRegistryAttempt(
	registry *negotiatedSessionRegistry,
	id []byte,
	response *enginev1.InitializeResponse,
) (*initializationAttemptLease, *enginev1.InitializeResponse, error) {
	request := grpcInitializeAttemptRequest(clientLimitsFromWire(response.GetLimits()), id)
	if response.GetOwnershipEpoch() > 1 {
		request.ReconnectClaim = &enginev1.ReconnectClaim{
			ClientId: response.GetClientId(), ExpectedOwnershipEpoch: response.GetOwnershipEpoch() - 1,
		}
	}
	fingerprint, err := fingerprintInitializeRequest(request)
	if err != nil {
		return nil, nil, err
	}
	return registry.reserveInitializationAttempt(context.Background(), id, fingerprint)
}

func commitRegistryAttempt(
	t *testing.T,
	registry *negotiatedSessionRegistry,
	id []byte,
	response *enginev1.InitializeResponse,
	reconnect bool,
) {
	t.Helper()
	lease, replay, err := reserveRegistryAttempt(registry, id, response)
	if err != nil || replay != nil {
		t.Fatalf("reserve attempt = %#v, %v", replay, err)
	}
	if err = lease.commit(response, reconnect); err != nil {
		t.Fatal(err)
	}
}

func waitForAttemptWaiters(t *testing.T, registry *negotiatedSessionRegistry, id string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		registry.mu.Lock()
		record := registry.attempts[id]
		got := 0
		if record != nil {
			got = record.waiters
		}
		registry.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("attempt waiters did not reach %d", want)
}

func receiveInitializationTestValue[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("initialization attempt test operation timed out")
		var zero T
		return zero
	}
}

func clientLimitsFixture(t *testing.T) client.Limits {
	t.Helper()
	_, limits, _, _ := wireFixtureValues(t)
	return limits
}

func clientLimitsFromWire(value *commonv1.Limits) client.Limits {
	limits, _ := client.NewLimits(
		value.GetMaxMessageBytes(), value.GetMaxCollectionItems(), value.GetMaxReplayEvents(),
		value.GetMaxReplayBytes(), value.GetMaxConcurrentStreams(), value.GetMaxActiveRuns(),
	)
	return limits
}

func serverTokenAuthorization(t *testing.T, seed byte) (string, error) {
	t.Helper()
	token := endpointTokenFixture(t, seed)
	return token.AuthorizationValue()
}

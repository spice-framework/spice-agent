package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/client"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"github.com/spice-framework/spice-agent/tool"
)

func TestRunHostBoundaryRejectsEveryInvalidConfigurationClass(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)

	tests := []struct {
		name      string
		configure func(*RunHostConfig)
		authority hostAuthority
	}{
		{name: "nil root", configure: func(config *RunHostConfig) { config.Root = nil }, authority: fixture.authority},
		{name: "nil engine", configure: func(config *RunHostConfig) { config.Engine = nil }, authority: fixture.authority},
		{name: "nil authority", configure: func(*RunHostConfig) {}, authority: nil},
		{name: "nil sessions", configure: func(config *RunHostConfig) { config.Sessions = nil }, authority: fixture.authority},
		{name: "nil ledger", configure: func(config *RunHostConfig) { config.Ledger = nil }, authority: fixture.authority},
		{name: "nil pending hub", configure: func(config *RunHostConfig) { config.Pending = nil }, authority: fixture.authority},
		{name: "canceled root", configure: func(config *RunHostConfig) {
			root, cancel := context.WithCancel(context.Background())
			cancel()
			config.Root = root
		}, authority: fixture.authority},
		{name: "invalid limits", configure: func(config *RunHostConfig) { config.Limits = client.Limits{} }, authority: fixture.authority},
		{name: "invalid definitions", configure: func(config *RunHostConfig) { config.Definitions = DefinitionSet{} }, authority: fixture.authority},
		{name: "terminal count above bound", configure: func(config *RunHostConfig) { config.TerminalRuns = maximumTerminalRuns + 1 }, authority: fixture.authority},
		{name: "terminal bytes zero", configure: func(config *RunHostConfig) { config.TerminalBytes = 0 }, authority: fixture.authority},
		{name: "terminal bytes above bound", configure: func(config *RunHostConfig) { config.TerminalBytes = maximumTerminalBytes + 1 }, authority: fixture.authority},
		{name: "transition timeout below bound", configure: func(config *RunHostConfig) { config.TransitionTimeout = time.Nanosecond }, authority: fixture.authority},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			configuration := fixture.config
			test.configure(&configuration)
			if _, err := newRunHost(configuration, test.authority); err == nil {
				t.Fatal("invalid run host configuration succeeded")
			}
		})
	}

	if _, err := NewRunHost(fixture.config); err == nil {
		t.Fatal("public constructor accepted a nil concrete authority")
	}
}

func TestRunHostBoundaryPublicMethodsRejectInvalidAndClosedInputs(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	run, _ := client.NewRunRef("boundary-run")
	mutation, _ := client.NewRunMutation(run, mustBoundaryOperation(t, "boundary-mutation"))
	cancelRequest, _ := client.NewCancelRequest(run, mustBoundaryOperation(t, "boundary-cancel"), "boundary")
	respondRequest := boundaryRespondRequest(t, run, "boundary-respond")
	startRequest := hostStartRequest(t, "boundary-start", fixture.definition, "boundary-input")

	var closed *RunHost
	if _, err := closed.Start(t.Context(), fixture.session, startRequest); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("nil host start = %v", err)
	}
	if _, err := closed.Import(t.Context(), fixture.session, client.ImportRequest{}); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("nil host import = %v", err)
	}
	if _, err := closed.Suspend(t.Context(), fixture.session, mutation); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("nil host suspend = %v", err)
	}
	if _, err := closed.Resume(t.Context(), fixture.session, mutation); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("nil host resume = %v", err)
	}
	if _, err := closed.Cancel(t.Context(), fixture.session, cancelRequest); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("nil host cancel = %v", err)
	}
	if _, err := closed.Respond(t.Context(), fixture.session, respondRequest); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("nil host respond = %v", err)
	}
	if _, err := closed.Export(t.Context(), fixture.session, run); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("nil host export = %v", err)
	}
	if _, err := closed.Health(t.Context(), fixture.session); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("nil host health = %v", err)
	}
	if err := closed.Shutdown(t.Context()); err != nil {
		t.Fatalf("nil host shutdown = %v", err)
	}

	//nolint:staticcheck // Boundary coverage intentionally passes nil to verify defensive context handling.
	if _, err := fixture.host.Start(nil, fixture.session, startRequest); !errors.Is(err, context.Canceled) {
		t.Fatalf("nil start context = %v", err)
	}
	//nolint:staticcheck // Boundary coverage intentionally passes nil to verify defensive context handling.
	if _, err := fixture.host.Health(nil, fixture.session); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("nil health context = %v", err)
	}
	//nolint:staticcheck // Boundary coverage intentionally passes nil to verify defensive context handling.
	if err := fixture.host.Shutdown(nil); err == nil {
		t.Fatal("nil shutdown context succeeded")
	}
	if _, err := fixture.host.Export(t.Context(), fixture.session, client.RunRef{}); !errors.Is(err, ErrHostedRunUnavailable) {
		t.Fatalf("invalid export reference = %v", err)
	}
	if _, err := fixture.host.Start(t.Context(), fixture.session, client.StartRequest{}); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("invalid start request = %v", err)
	}
	if _, err := fixture.host.Import(t.Context(), fixture.session, client.ImportRequest{}); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("invalid import request = %v", err)
	}
	if _, err := fixture.host.Suspend(t.Context(), fixture.session, client.RunMutation{}); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("invalid suspend request = %v", err)
	}
	if _, err := fixture.host.Resume(t.Context(), fixture.session, client.RunMutation{}); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("invalid resume request = %v", err)
	}
	if _, err := fixture.host.Cancel(t.Context(), fixture.session, client.CancelRequest{}); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("invalid cancel request = %v", err)
	}
	if _, err := fixture.host.Respond(t.Context(), fixture.session, client.RespondRequest{}); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("invalid respond request = %v", err)
	}
}

func TestRunHostBoundaryRejectsNilSuccessfulImportTransaction(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	fixture.authority.allowImport = true
	fixture.authority.nilImport = true

	transaction, prepared, outcome := fixture.host.prepareImport(t.Context(), nil, agent.Snapshot{})
	if transaction != nil || prepared != nil {
		t.Fatalf("nil import preparation = transaction %#v, prepared %#v", transaction, prepared)
	}
	var marker runHostOutcome
	if err := json.Unmarshal(outcome.Payload(), &marker); err != nil || marker.Code != outcomeCodeAbandon ||
		marker.AbandonCode != outcomeCodeDependency {
		t.Fatalf("nil import preparation = %#v, %v", marker, err)
	}
	health, err := fixture.host.Health(t.Context(), fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(health.Reasons(), degradedAuthorityMissing) {
		t.Fatalf("nil import health reasons = %v", health.Reasons())
	}
}

func TestRunHostBoundaryCanceledAndStaleOperationsRemainRetryable(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 4)
	request := hostStartRequest(t, "retryable-context", fixture.definition, "retryable-input")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.host.Start(canceled, fixture.session, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled start = %v", err)
	}
	result, err := fixture.host.Start(t.Context(), fixture.session, request)
	if err != nil || result.DuplicateOperation() {
		t.Fatalf("retry canceled start = %#v, %v", result, err)
	}
	<-fixture.authority.issued
	waitForNoHostActive(t, fixture.host)

	staleRequest := hostStartRequest(t, "retryable-stale", fixture.definition, "stale-input")
	stale := Session{
		clientID: fixture.session.ClientID(),
		epoch:    fixture.session.Epoch() + 1,
		ctx:      t.Context(),
	}
	if _, err = fixture.host.Start(t.Context(), stale, staleRequest); !errors.Is(err, ErrStaleSession) {
		t.Fatalf("stale start = %v", err)
	}
	if _, err = fixture.host.Health(t.Context(), stale); !errors.Is(err, ErrStaleSession) {
		t.Fatalf("stale health = %v", err)
	}
	staleResult, err := fixture.host.Start(t.Context(), fixture.session, staleRequest)
	if err != nil || staleResult.DuplicateOperation() {
		t.Fatalf("retry stale start = %#v, %v", staleResult, err)
	}
	<-fixture.authority.issued
}

func TestRunHostBoundaryConflictingDuplicateDoesNotStartSecondRun(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 2, 4)
	first := hostStartRequest(t, "conflicting-operation", fixture.definition, "conflict-first")
	if _, err := fixture.host.Start(t.Context(), fixture.session, first); err != nil {
		t.Fatal(err)
	}
	<-fixture.authority.issued

	operation := mustBoundaryOperation(t, "conflicting-operation")
	reference, _ := client.NewDefinitionRef(fixture.definition.ID(), fixture.definition.Revision())
	input, _ := client.NewInput("conflict-second", "different input")
	second, _ := client.NewStartRequest(operation, reference, input)
	if _, err := fixture.host.Start(t.Context(), fixture.session, second); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("conflicting duplicate = %v", err)
	}
	if got := fixture.authority.starts.Load(); got != 1 {
		t.Fatalf("authority starts = %d, want 1", got)
	}
}

func TestRunHostBoundaryWrongOwnerIsUniformAcrossMutations(t *testing.T) {
	t.Parallel()
	provider := &sequenceHostProvider{firstStarted: make(chan struct{})}
	fixture := newRunHostFixture(t, provider, 1, 4)
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "owner-start", fixture.definition, "owner-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-provider.firstStarted
	other, err := fixture.sessions.Fresh()
	if err != nil {
		t.Fatal(err)
	}

	mutation := hostRunMutation(t, started.Run(), "wrong-owner-suspend")
	_, suspendErr := fixture.host.Suspend(t.Context(), other, mutation)
	resumeMutation := hostRunMutation(t, started.Run(), "wrong-owner-resume")
	_, resumeErr := fixture.host.Resume(t.Context(), other, resumeMutation)
	cancelRequest, _ := client.NewCancelRequest(
		started.Run(), mustBoundaryOperation(t, "wrong-owner-cancel"), "wrong owner",
	)
	_, cancelErr := fixture.host.Cancel(t.Context(), other, cancelRequest)
	_, respondErr := fixture.host.Respond(
		t.Context(), other, boundaryRespondRequest(t, started.Run(), "wrong-owner-respond"),
	)
	for name, publicErr := range map[string]error{
		"suspend": suspendErr,
		"resume":  resumeErr,
		"cancel":  cancelErr,
		"respond": respondErr,
	} {
		if !errors.Is(publicErr, ErrHostedRunUnavailable) || publicErr.Error() != ErrHostedRunUnavailable.Error() {
			t.Fatalf("%s wrong-owner error = %v", name, publicErr)
		}
	}

	ownerCancel, _ := client.NewCancelRequest(
		started.Run(), mustBoundaryOperation(t, "owner-cleanup"), "cleanup",
	)
	if _, err = fixture.host.Cancel(t.Context(), fixture.session, ownerCancel); err != nil {
		t.Fatal(err)
	}
	<-fixture.authority.issued
}

func TestRunHostBoundaryHealthUsesServerLimitsAndSortedFixedReasons(t *testing.T) {
	t.Parallel()
	provider := &sequenceHostProvider{firstStarted: make(chan struct{})}
	fixture := newRunHostFixture(t, provider, 2, 4)
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "health-start", fixture.definition, "health-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-provider.firstStarted
	fixture.host.degrade(degradedTerminalSnapshot)
	fixture.host.degrade(degradedAuthorityMissing)
	health, err := fixture.host.Health(t.Context(), fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	if health.State() != client.HealthDegraded || health.ActiveRuns() != 1 ||
		health.Limits().ActiveRuns() != fixture.config.Limits.ActiveRuns() {
		t.Fatalf("health = %#v", health)
	}
	wantReasons := []string{degradedAuthorityMissing, degradedTerminalSnapshot}
	gotReasons := health.Reasons()
	if len(gotReasons) != len(wantReasons) || gotReasons[0] != wantReasons[0] || gotReasons[1] != wantReasons[1] {
		t.Fatalf("sorted reasons = %v, want %v", gotReasons, wantReasons)
	}

	fixture.host.mu.Lock()
	fixture.host.closing = true
	fixture.host.mu.Unlock()
	stopping, err := fixture.host.Health(t.Context(), fixture.session)
	fixture.host.mu.Lock()
	fixture.host.closing = false
	fixture.host.mu.Unlock()
	if err != nil || stopping.State() != client.HealthStopping || len(stopping.Reasons()) != 0 {
		t.Fatalf("stopping health = %#v, %v", stopping, err)
	}

	cancelRequest, _ := client.NewCancelRequest(
		started.Run(), mustBoundaryOperation(t, "health-cleanup"), "cleanup",
	)
	if _, err = fixture.host.Cancel(t.Context(), fixture.session, cancelRequest); err != nil {
		t.Fatal(err)
	}
	<-fixture.authority.issued
}

func TestRunHostBoundaryTerminalEnvelopeFailureIsNotExportable(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	fixture.authority.issueErr = ErrRunAuthorityUnavailable
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "failed-envelope-start", fixture.definition, "failed-envelope-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitForNoHostActive(t, fixture.host)
	if _, err = fixture.host.Export(t.Context(), fixture.session, started.Run()); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("terminal export without authority envelope = %v", err)
	}
}

func TestRunHostBoundaryErrorVocabularyRoundTripsThroughLedgerOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
	}{
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "closed", err: ErrRunHostClosed},
		{name: "capacity", err: ErrRunHostCapacity},
		{name: "stale", err: ErrStaleSession},
		{name: "run unavailable", err: ErrHostedRunUnavailable},
		{name: "uncertain", err: ErrRunHostUncertain},
		{name: "dependency", err: ErrRunHostUnavailable},
		{name: "state", err: ErrRunHostState},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			outcome := failureRunHostOutcome(test.err)
			_, err := decodeRunHostOutcome(outcome)
			if !errors.Is(err, test.err) {
				t.Fatalf("decoded %v as %v", test.err, err)
			}
		})
	}

	success := successRunHostOutcome(runHostOutcome{RunID: "run", Sequence: 3})
	decoded, err := decodeRunHostOutcome(success)
	if err != nil || decoded.RunID != "run" || decoded.Sequence != 3 {
		t.Fatalf("success outcome = %#v, %v", decoded, err)
	}
	malformed, _ := NewOutcome(OutcomeFailure, []byte("{"))
	if _, err = decodeRunHostOutcome(malformed); !errors.Is(err, ErrRunHostUnavailable) {
		t.Fatalf("malformed outcome = %v", err)
	}
	unknown, _ := NewOutcome(OutcomeFailure, []byte(`{"code":"future"}`))
	if _, err = decodeRunHostOutcome(unknown); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("unknown outcome = %v", err)
	}

	if canonicalMutationDigest(math.NaN()) != CanonicalDigest([]byte("invalid canonical mutation")) {
		t.Fatal("non-JSON mutation did not use the deterministic invalid digest")
	}
}

func TestRunHostBoundaryMapsDependencyErrorsWithoutLeakingDetails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input error
		want  error
	}{
		{input: nil, want: nil},
		{input: context.Canceled, want: context.Canceled},
		{input: context.DeadlineExceeded, want: context.DeadlineExceeded},
		{input: ErrRunHostCapacity, want: ErrRunHostCapacity},
		{input: ErrHostedRunUnavailable, want: ErrHostedRunUnavailable},
		{input: ErrRunHostUncertain, want: ErrRunHostUncertain},
		{input: ErrRunHostUnavailable, want: ErrRunHostUnavailable},
		{input: ErrRunHostState, want: ErrRunHostState},
		{input: ErrRunHostClosed, want: ErrRunHostClosed},
		{input: ErrSessionStoreClosed, want: ErrRunHostClosed},
		{input: ErrPendingHubClosed, want: ErrRunHostClosed},
		{input: ErrStaleSession, want: ErrStaleSession},
		{input: ErrSessionGateCapacity, want: ErrRunHostCapacity},
		{input: ErrRunAuthorityUncertain, want: ErrRunHostUncertain},
		{input: ErrRunAuthorityUnavailable, want: ErrRunHostUnavailable},
		{input: ErrRunAuthorityBusy, want: ErrRunHostState},
		{input: ErrRunAuthorityState, want: ErrRunHostState},
		{input: ErrRunAuthorityVerification, want: ErrRunHostState},
		{input: ErrRunBindingCapacity, want: ErrRunHostCapacity},
		{input: ErrPendingCapacity, want: ErrRunHostCapacity},
		{input: ErrRunNotBound, want: ErrHostedRunUnavailable},
		{input: ErrInteractionNotPending, want: ErrHostedRunUnavailable},
		{input: errors.New("private dependency detail"), want: ErrRunHostState},
	}
	for _, test := range tests {
		got := publicRunHostError(test.input)
		if test.want == nil {
			if got != nil {
				t.Fatalf("nil error mapped to %v", got)
			}
			continue
		}
		if !errors.Is(got, test.want) || got.Error() != test.want.Error() {
			t.Fatalf("%v mapped to %v, want %v", test.input, got, test.want)
		}
	}
}

func TestRunHostBoundaryDefensiveHelperInputs(t *testing.T) {
	t.Parallel()
	if _, err := NewOutcome(OutcomeSuccess, make([]byte, maximumOutcomeBytes+1)); err == nil {
		t.Fatal("oversized idempotency outcome was accepted")
	}
	var abandoned *abandonedOperationError
	if abandoned.Unwrap() != nil {
		t.Fatal("nil abandonment wrapper exposed a cause")
	}
	if isIsolatedContextTermination(nil) {
		t.Fatal("nil error was classified as isolated cancellation")
	}
	//nolint:staticcheck // Boundary coverage intentionally passes nil to verify defensive context composition.
	merged, cancel := mergeContexts(nil, nil)
	if err := merged.Err(); err != nil {
		cancel()
		t.Fatalf("fresh nil-parent merge = %v", err)
	}
	cancel()
	if !errors.Is(merged.Err(), context.Canceled) {
		t.Fatalf("canceled nil-parent merge = %v", merged.Err())
	}
}

func TestRunHostBoundaryAbandonmentCodesPreserveRetryClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code string
		want error
	}{
		{code: outcomeCodeCanceled, want: context.Canceled},
		{code: outcomeCodeDeadline, want: context.DeadlineExceeded},
		{code: outcomeCodeClosed, want: ErrRunHostClosed},
		{code: outcomeCodeCapacity, want: ErrRunHostCapacity},
		{code: outcomeCodeStale, want: ErrStaleSession},
		{code: outcomeCodeRunMissing, want: ErrHostedRunUnavailable},
		{code: outcomeCodeDependency, want: ErrRunHostUnavailable},
		{code: outcomeCodeUncertain, want: ErrRunHostUncertain},
		{code: "future", want: ErrRunHostState},
	}
	for _, test := range tests {
		if got := runHostErrorForCode(test.code); !errors.Is(got, test.want) {
			t.Fatalf("abandonment code %q = %v, want %v", test.code, got, test.want)
		}
	}
}

func TestRunHostBoundaryLifecycleRollbackClassifiesAuthorityFailures(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	fixture.host.rollbackStartedAuthority(nil)

	uncertain := &boundaryHostActive{
		terminalErr: ErrRunAuthorityUncertain,
		closeErr:    ErrRunAuthorityUnavailable,
	}
	fixture.host.rollbackStartedAuthority(uncertain)
	if uncertain.terminalCalls != 1 || uncertain.closeCalls != 1 {
		t.Fatalf("uncertain rollback calls = terminal %d, close %d", uncertain.terminalCalls, uncertain.closeCalls)
	}
	unavailable := &boundaryHostActive{terminalErr: ErrRunAuthorityUnavailable}
	fixture.host.rollbackStartedAuthority(unavailable)

	health, err := fixture.host.Health(t.Context(), fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{
		degradedAuthorityUncertain,
		degradedAuthorityMissing,
		degradedLifecycleCleanup,
	} {
		if !containsString(health.Reasons(), reason) {
			t.Fatalf("rollback health reasons %v omit %q", health.Reasons(), reason)
		}
	}
}

func TestRunHostBoundarySuspendFailureRestoresLocalExecution(t *testing.T) {
	t.Parallel()
	firstRelease := make(chan struct{})
	secondRelease := make(chan struct{})
	provider := &twoTurnHostProvider{
		firstStarted: make(chan struct{}), firstRelease: firstRelease,
		secondStarted: make(chan struct{}), secondRelease: secondRelease,
	}
	fixture := newRunHostFixtureWithTools(
		t, provider, map[string]tool.Tool{"read": hostReadTool{}}, 1, 2,
	)
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "restore-after-suspend-start", fixture.definition, "restore-after-suspend-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-provider.firstStarted
	fixture.authority.issueErr = ErrRunAuthorityUnavailable

	result := make(chan error, 1)
	go func() {
		_, suspendErr := fixture.host.Suspend(
			context.Background(), fixture.session,
			hostRunMutation(t, started.Run(), "restore-after-suspend"),
		)
		result <- suspendErr
	}()
	waitForRunTransitionHeld(t, fixture.host, started.Run().ID())
	close(firstRelease)
	if err = <-result; !errors.Is(err, ErrRunHostUnavailable) {
		t.Fatalf("failed suspend = %v", err)
	}
	select {
	case <-provider.secondStarted:
	case <-time.After(time.Second):
		t.Fatal("local execution did not resume after safe snapshot issuance failure")
	}
	close(secondRelease)
	waitForNoHostActive(t, fixture.host)
	health, err := fixture.host.Health(t.Context(), fixture.session)
	if err != nil || !containsString(health.Reasons(), degradedAuthorityMissing) {
		t.Fatalf("suspend failure health = %#v, %v", health, err)
	}
}

func TestRunHostBoundaryUnexpectedLedgerExecutorFailureIsUncertain(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	_, _, err := fixture.host.doMutation(
		t.Context(), fixture.session, mustBoundaryOperation(t, "invalid-executor-outcome"),
		"boundary.invalid/v1", canonicalMutationDigest("input"),
		func(context.Context) Outcome { return Outcome{} },
	)
	if !errors.Is(err, ErrRunHostUncertain) {
		t.Fatalf("invalid executor outcome = %v", err)
	}
	health, healthErr := fixture.host.Health(t.Context(), fixture.session)
	if healthErr != nil || !containsString(health.Reasons(), degradedLifecycleCleanup) {
		t.Fatalf("executor failure health = %#v, %v", health, healthErr)
	}
}

func TestRunHostBoundaryReservationAndCleanupAreIdempotent(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 2, 2)
	var nilReservation *hostReservation
	if _, err := nilReservation.bind("run"); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("nil reservation bind = %v", err)
	}
	nilReservation.release()
	if _, err := (&hostReservation{}).bind("run"); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("unowned reservation bind = %v", err)
	}

	first, err := fixture.host.reserveSlot(fixture.session.ClientID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = first.bind(""); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("invalid run binding = %v", err)
	}
	binding, err := first.bind("boundary-reservation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = first.bind("boundary-reservation-again"); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("duplicate reservation binding = %v", err)
	}
	second, err := fixture.host.reserveSlot(fixture.session.ClientID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = second.bind("boundary-reservation"); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("duplicate run identity binding = %v", err)
	}
	binding.Release()
	if err = binding.WaitReleased(t.Context()); err != nil {
		t.Fatal(err)
	}
	first.release()
	first.release()
	second.release()

	released := false
	fixture.host.abortPreparedAndThen(nil, func() error {
		released = true
		return nil
	})
	if !released {
		t.Fatal("nil prepared run did not release downstream ownership")
	}
}

func TestRunHostBoundaryImportRejectsMalformedAndUnknownProtocolFields(t *testing.T) {
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	operation := mustBoundaryOperation(t, "malformed-import")
	for name, encoded := range map[string][]byte{
		"malformed protobuf":  {0xff},
		"unknown field":       {0xf8, 0x07, 0x01},
		"incomplete envelope": mustBoundaryProto(t, &enginev1.SnapshotEnvelope{RunId: "run"}),
	} {
		t.Run(name, func(t *testing.T) {
			snapshot, err := client.ParseSnapshot(encoded)
			if err != nil {
				t.Fatal(err)
			}
			request, err := client.NewImportRequest(operation, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = fixture.host.Import(t.Context(), fixture.session, request); !errors.Is(err, ErrRunHostState) {
				t.Fatalf("malformed import = %v", err)
			}
		})
	}
}

func mustBoundaryOperation(t *testing.T, value string) client.OperationID {
	t.Helper()
	operation, err := client.NewOperationID(value)
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func boundaryRespondRequest(t *testing.T, run client.RunRef, operationValue string) client.RespondRequest {
	t.Helper()
	value, err := client.NewStructuredText("accepted")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.NewInteractionResponse("boundary-interaction", value)
	if err != nil {
		t.Fatal(err)
	}
	request, err := client.NewRespondRequest(run, mustBoundaryOperation(t, operationValue), response)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func mustBoundaryProto(t *testing.T, value *enginev1.SnapshotEnvelope) []byte {
	t.Helper()
	encoded, err := marshalEnvelope(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

type boundaryHostActive struct {
	terminalErr   error
	closeErr      error
	terminalCalls int
	closeCalls    int
}

func (*boundaryHostActive) Resume(context.Context) error { return nil }

func (*boundaryHostActive) IssueSnapshotEnvelope(
	context.Context,
	agent.Snapshot,
) (*enginev1.SnapshotEnvelope, error) {
	return nil, ErrRunAuthorityUnavailable
}

func (active *boundaryHostActive) Terminal(context.Context, TerminalPhase) error {
	active.terminalCalls++
	return active.terminalErr
}

func (active *boundaryHostActive) Close() error {
	active.closeCalls++
	return active.closeErr
}

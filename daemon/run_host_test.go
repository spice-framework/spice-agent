package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/protobuf/proto"
)

func TestRunHostRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	if _, err := NewRunHost(RunHostConfig{}); err == nil {
		t.Fatal("zero run host configuration succeeded")
	}
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	configuration := fixture.config
	configuration.TerminalRuns = 0
	if _, err := newRunHost(configuration, fixture.authority); err == nil {
		t.Fatal("zero terminal cache count succeeded")
	}
	configuration = fixture.config
	configuration.TransitionTimeout = 31 * time.Second
	if _, err := newRunHost(configuration, fixture.authority); err == nil {
		t.Fatal("unbounded transition timeout succeeded")
	}
}

func TestRunHostStartsOnlyAfterAuthorityAndOutlivesRequest(t *testing.T) {
	t.Parallel()
	recorder := newHostRecorder()
	release := make(chan struct{})
	provider := &gatedHostProvider{recorder: recorder, started: make(chan struct{}), release: release}
	fixture := newRunHostFixture(t, provider, 2, 2)
	fixture.authority.recorder = recorder

	requestContext, cancelRequest := context.WithCancel(t.Context())
	request := hostStartRequest(t, "start-1", fixture.definition, "input-1")
	result, err := fixture.host.Start(requestContext, fixture.session, request)
	if err != nil {
		t.Fatal(err)
	}
	cancelRequest()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start after host returned")
	}
	close(release)
	snapshot := <-fixture.authority.issued
	if snapshot.Status() != agent.LifecycleCompleted {
		t.Fatalf("caller cancellation leaked into run root: %s", snapshot.Status())
	}
	events := recorder.values()
	if indexOf(events, "authority.start") < 0 || indexOf(events, "provider.stream") < 0 ||
		indexOf(events, "authority.start") > indexOf(events, "provider.stream") {
		t.Fatalf("activation order = %v", events)
	}

	duplicate, err := fixture.host.Start(t.Context(), fixture.session, request)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.DuplicateOperation() || duplicate.Run().ID() != result.Run().ID() ||
		fixture.authority.starts.Load() != 1 {
		t.Fatalf("duplicate start = %#v, authority starts %d", duplicate, fixture.authority.starts.Load())
	}

	waitForNoHostActive(t, fixture.host)
	exported, err := fixture.host.Export(t.Context(), fixture.session, result.Run())
	if err != nil || exported.SizeBytes() == 0 {
		t.Fatalf("terminal export = %d bytes, %v", exported.SizeBytes(), err)
	}
	other, err := fixture.sessions.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	_, wrongOwnerErr := fixture.host.Export(t.Context(), other, result.Run())
	missing, _ := client.NewRunRef("missing-run")
	_, missingErr := fixture.host.Export(t.Context(), fixture.session, missing)
	if !errors.Is(wrongOwnerErr, ErrHostedRunUnavailable) || !errors.Is(missingErr, ErrHostedRunUnavailable) ||
		wrongOwnerErr.Error() != missingErr.Error() {
		t.Fatalf("wrong-owner %v and missing %v were distinguishable", wrongOwnerErr, missingErr)
	}
}

func TestRunHostCapacityFailureCanRetrySameOperation(t *testing.T) {
	t.Parallel()
	provider := &sequenceHostProvider{firstStarted: make(chan struct{})}
	fixture := newRunHostFixture(t, provider, 1, 2)
	first := hostStartRequest(t, "capacity-first", fixture.definition, "input-first")
	firstResult, err := fixture.host.Start(t.Context(), fixture.session, first)
	if err != nil {
		t.Fatal(err)
	}
	<-provider.firstStarted

	second := hostStartRequest(t, "capacity-second", fixture.definition, "input-second")
	if _, err = fixture.host.Start(t.Context(), fixture.session, second); !errors.Is(err, ErrRunHostCapacity) {
		t.Fatalf("capacity failure = %v", err)
	}
	var capacity *RunHostCapacityError
	if !errors.As(err, &capacity) || capacity.Resource() != "active runs" ||
		capacity.Limit() != 1 || capacity.Observed() != 2 {
		t.Fatalf("typed capacity failure = %#v", capacity)
	}
	cancelOperation, _ := client.NewOperationID("cancel-capacity-first")
	cancelRequest, _ := client.NewCancelRequest(firstResult.Run(), cancelOperation, "free capacity")
	if _, err = fixture.host.Cancel(t.Context(), fixture.session, cancelRequest); err != nil {
		t.Fatal(err)
	}
	<-fixture.authority.issued
	waitForNoHostActive(t, fixture.host)

	secondResult, err := fixture.host.Start(t.Context(), fixture.session, second)
	if err != nil {
		t.Fatalf("retry abandoned capacity operation: %v", err)
	}
	if secondResult.DuplicateOperation() {
		t.Fatal("retry of abandoned capacity operation was marked duplicate")
	}
	<-fixture.authority.issued
}

func TestRunHostUncertainAuthorityStartNeverActivatesKernel(t *testing.T) {
	t.Parallel()
	recorder := newHostRecorder()
	provider := &recordingImmediateHostProvider{recorder: recorder}
	fixture := newRunHostFixture(t, provider, 1, 2)
	fixture.authority.startErr = ErrRunAuthorityUncertain
	_, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "uncertain-start", fixture.definition, "uncertain-input"),
	)
	if !errors.Is(err, ErrRunHostUncertain) {
		t.Fatalf("uncertain authority start = %v", err)
	}
	if indexOf(recorder.values(), "provider.stream") >= 0 {
		t.Fatal("kernel provider executed after uncertain authority start")
	}
	waitForNoHostActive(t, fixture.host)
	health, err := fixture.host.Health(t.Context(), fixture.session)
	if err != nil || health.State() != client.HealthDegraded ||
		!containsString(health.Reasons(), degradedAuthorityUncertain) {
		t.Fatalf("uncertain health = %#v, %v", health, err)
	}
}

func TestRunHostAuthorityContextFailureAbandonsAfterInertJoin(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	fixture.authority.startErr = context.DeadlineExceeded
	request := hostStartRequest(t, "authority-context-start", fixture.definition, "authority-context-input")
	if _, err := fixture.host.Start(t.Context(), fixture.session, request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("authority context failure = %v", err)
	}
	waitForNoHostActive(t, fixture.host)
	fixture.authority.startErr = nil
	result, err := fixture.host.Start(t.Context(), fixture.session, request)
	if err != nil || result.DuplicateOperation() {
		t.Fatalf("retry joined authority context failure = %#v, %v", result, err)
	}
	<-fixture.authority.issued
}

func TestRunHostPrecommitCleanupFailureIsUncertain(t *testing.T) {
	t.Parallel()
	dispatcher, err := stage.NewDispatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	planID, _ := stage.NewPlanID("failing-release-plan")
	source := failingReleasePlanSource{dispatcher: dispatcher, id: planID}
	fixture := newRunHostFixtureWithPlanSource(t, immediateHostProvider{}, source, 1, 2)
	fixture.pending().Close()
	_, err = fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "cleanup-failure-start", fixture.definition, "cleanup-failure-input"),
	)
	if !errors.Is(err, ErrRunHostUncertain) {
		t.Fatalf("precommit cleanup failure = %v", err)
	}
	health, err := fixture.host.Health(t.Context(), fixture.session)
	if err != nil || !containsString(health.Reasons(), degradedLifecycleCleanup) {
		t.Fatalf("cleanup failure health = %#v, %v", health, err)
	}
}

func TestRunHostTerminalCacheEvictsOldestCompletion(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 2, 1)
	first, err := fixture.host.Start(
		t.Context(), fixture.session, hostStartRequest(t, "terminal-first", fixture.definition, "input-first"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-fixture.authority.issued
	second, err := fixture.host.Start(
		t.Context(), fixture.session, hostStartRequest(t, "terminal-second", fixture.definition, "input-second"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-fixture.authority.issued
	waitForNoHostActive(t, fixture.host)
	fixture.host.mu.Lock()
	retained := fixture.host.terminalOrder[0]
	fixture.host.mu.Unlock()
	retainedRun := first.Run()
	evictedRun := second.Run()
	if retained == second.Run().ID() {
		retainedRun, evictedRun = second.Run(), first.Run()
	}
	if _, err = fixture.host.Export(t.Context(), fixture.session, evictedRun); !errors.Is(err, ErrHostedRunUnavailable) {
		t.Fatalf("evicted terminal export = %v", err)
	}
	if _, err = fixture.host.Export(t.Context(), fixture.session, retainedRun); err != nil {
		t.Fatalf("retained terminal export = %v", err)
	}
}

func TestRunHostCancelWaitsForTerminalPublication(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	fixture.authority.issueEntered = make(chan struct{})
	fixture.authority.issueRelease = make(chan struct{})
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "terminal-cancel-start", fixture.definition, "terminal-cancel-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-fixture.authority.issueEntered
	operation, _ := client.NewOperationID("terminal-cancel")
	request, _ := client.NewCancelRequest(started.Run(), operation, "terminal race")
	result := make(chan struct {
		value client.CancelResult
		err   error
	}, 1)
	go func() {
		value, cancelErr := fixture.host.Cancel(context.Background(), fixture.session, request)
		result <- struct {
			value client.CancelResult
			err   error
		}{value, cancelErr}
	}()
	entry := waitForLedgerOperation(
		t, fixture.host.ledger, fixture.session.ClientID(), operation.String(),
	)
	select {
	case <-entry.done:
		t.Fatal("cancel completed while terminal publication owned the run transition")
	default:
	}
	close(fixture.authority.issueRelease)
	cancelled := <-result
	if cancelled.err != nil || !cancelled.value.AlreadyTerminal() || cancelled.value.TerminalSequence() == 0 {
		t.Fatalf("terminal cancel = %#v, %v", cancelled.value, cancelled.err)
	}
}

func TestRunHostTerminalSequenceSurvivesEnvelopeFailure(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	fixture.authority.issueErr = ErrRunAuthorityUnavailable
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "terminal-issue-failure", fixture.definition, "terminal-issue-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitForNoHostActive(t, fixture.host)
	operation, _ := client.NewOperationID("terminal-issue-cancel")
	request, _ := client.NewCancelRequest(started.Run(), operation, "observe terminal")
	result, err := fixture.host.Cancel(t.Context(), fixture.session, request)
	if err != nil || !result.AlreadyTerminal() || result.TerminalSequence() == 0 {
		t.Fatalf("terminal sequence after issue failure = %#v, %v", result, err)
	}
	health, err := fixture.host.Health(t.Context(), fixture.session)
	if err != nil || !containsString(health.Reasons(), degradedAuthorityMissing) {
		t.Fatalf("issue failure health = %#v, %v", health, err)
	}
}

func TestRunHostCancelDoesNotTakeGateWhileSuspendWaits(t *testing.T) {
	t.Parallel()
	firstRelease := make(chan struct{})
	provider := &twoTurnHostProvider{
		firstStarted: make(chan struct{}), firstRelease: firstRelease,
		secondStarted: make(chan struct{}), secondRelease: make(chan struct{}),
	}
	fixture := newRunHostFixtureWithTools(t, provider, map[string]tool.Tool{"read": hostReadTool{}}, 1, 2)
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "serialized-cancel-start", fixture.definition, "serialized-cancel-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-provider.firstStarted
	suspendResult := make(chan error, 1)
	go func() {
		_, suspendErr := fixture.host.Suspend(
			context.Background(), fixture.session,
			hostRunMutation(t, started.Run(), "serialized-suspend"),
		)
		suspendResult <- suspendErr
	}()
	waitForRunTransitionHeld(t, fixture.host, started.Run().ID())
	cancelResult := make(chan error, 1)
	go func() {
		operation, _ := client.NewOperationID("serialized-cancel")
		request, _ := client.NewCancelRequest(started.Run(), operation, "serialized")
		_, cancelErr := fixture.host.Cancel(context.Background(), fixture.session, request)
		cancelResult <- cancelErr
	}()
	gateContext, cancelGate := context.WithTimeout(t.Context(), time.Second)
	lease, err := fixture.sessions.AcquireMutationCommit(
		gateContext, fixture.session.ClientID(), fixture.session.Epoch(),
	)
	cancelGate()
	if err != nil {
		t.Fatalf("cancel acquired gate before per-run transition: %v", err)
	}
	lease.Close()
	close(firstRelease)
	if err = <-suspendResult; err != nil {
		t.Fatal(err)
	}
	if err = <-cancelResult; err != nil {
		t.Fatal(err)
	}
	<-fixture.authority.issued // suspended
	<-fixture.authority.envelopes
	<-fixture.authority.issued // cancelled terminal
}

func TestRunHostFailedSuspendRestorationIsUncertain(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "restore-failure-start", fixture.definition, "restore-failure-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-fixture.authority.issued
	waitForNoHostActive(t, fixture.host)
	fixture.host.mu.Lock()
	terminal, found := fixture.host.terminal[started.Run().ID()]
	fixture.host.mu.Unlock()
	if !found || terminal == nil {
		t.Fatal("completed run has no terminal snapshot")
	}
	if err = fixture.host.restoreSuspendedRun(&hostedRun{run: terminal.run}); !errors.Is(err, ErrRunHostUncertain) {
		t.Fatalf("failed restoration = %v", err)
	}
	health, err := fixture.host.Health(t.Context(), fixture.session)
	if err != nil || !containsString(health.Reasons(), degradedLifecycleCleanup) {
		t.Fatalf("failed restoration health = %#v, %v", health, err)
	}
}

func TestRunHostShutdownFencesAdmissionSynchronously(t *testing.T) {
	t.Parallel()
	authorityRelease := make(chan struct{})
	fixture := newRunHostFixture(t, immediateHostProvider{}, 2, 2)
	fixture.authority.startEntered = make(chan struct{})
	fixture.authority.startRelease = authorityRelease
	started := make(chan error, 1)
	go func() {
		_, err := fixture.host.Start(
			context.Background(), fixture.session,
			hostStartRequest(t, "shutdown-inflight", fixture.definition, "input-inflight"),
		)
		started <- err
	}()
	<-fixture.authority.startEntered
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fixture.host.Shutdown(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("bounded shutdown wait = %v", err)
	}
	if _, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "shutdown-rejected", fixture.definition, "input-rejected"),
	); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("post-shutdown admission = %v", err)
	}
	close(authorityRelease)
	if err := <-started; err != nil && !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("in-flight start resolution = %v", err)
	}
	if err := fixture.host.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRunHostRespondIsOwnedAndIdempotent(t *testing.T) {
	t.Parallel()
	provider := &sequenceHostProvider{firstStarted: make(chan struct{})}
	fixture := newRunHostFixture(t, provider, 1, 2)
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "respond-start", fixture.definition, "respond-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-provider.firstStarted
	fixture.host.mu.Lock()
	hosted := fixture.host.active[started.Run().ID()]
	fixture.host.mu.Unlock()
	request, err := interaction.NewRequest(
		interaction.ID("approval-1"), "approval", "Continue?", json.RawMessage(`{"type":"string"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	interactionResult := make(chan interaction.Response, 1)
	interactionError := make(chan error, 1)
	go func() {
		response, responseErr := hosted.run.Interact(context.Background(), request)
		interactionResult <- response
		interactionError <- responseErr
	}()
	waitForPendingCount(t, fixture.pending(), 1)
	operation, _ := client.NewOperationID("respond-operation")
	value, _ := client.NewStructuredText("yes")
	response, _ := client.NewInteractionResponse("approval-1", value)
	respondRequest, _ := client.NewRespondRequest(started.Run(), operation, response)
	responded, err := fixture.host.Respond(t.Context(), fixture.session, respondRequest)
	if err != nil || !responded.Accepted() || responded.DuplicateOperation() {
		t.Fatalf("respond = %#v, %v", responded, err)
	}
	if err = <-interactionError; err != nil {
		t.Fatal(err)
	}
	if got := string((<-interactionResult).Value()); got != `"yes"` {
		t.Fatalf("interaction response = %s", got)
	}
	duplicate, err := fixture.host.Respond(t.Context(), fixture.session, respondRequest)
	if err != nil || !duplicate.DuplicateOperation() {
		t.Fatalf("duplicate respond = %#v, %v", duplicate, err)
	}
	cancelOperation, _ := client.NewOperationID("respond-cancel")
	cancelRequest, _ := client.NewCancelRequest(started.Run(), cancelOperation, "done")
	if _, err = fixture.host.Cancel(t.Context(), fixture.session, cancelRequest); err != nil {
		t.Fatal(err)
	}
	<-fixture.authority.issued
}

func TestRunHostSuspendResumeAndPreparedImportOrdering(t *testing.T) {
	t.Parallel()
	recorder := newHostRecorder()
	firstRelease := make(chan struct{})
	secondRelease := make(chan struct{})
	sourceProvider := &twoTurnHostProvider{
		recorder: recorder, firstStarted: make(chan struct{}), firstRelease: firstRelease,
		secondStarted: make(chan struct{}), secondRelease: secondRelease,
	}
	source := newRunHostFixtureWithTools(
		t, sourceProvider, map[string]tool.Tool{"read": hostReadTool{}}, 2, 2,
	)
	source.authority.recorder = recorder
	started, err := source.host.Start(
		t.Context(), source.session,
		hostStartRequest(t, "suspend-start", source.definition, "suspend-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-sourceProvider.firstStarted
	runMutation := hostRunMutation(t, started.Run(), "suspend-operation")
	suspendedResult := make(chan struct {
		value client.SuspendResult
		err   error
	}, 1)
	go func() {
		value, suspendErr := source.host.Suspend(context.Background(), source.session, runMutation)
		suspendedResult <- struct {
			value client.SuspendResult
			err   error
		}{value, suspendErr}
	}()
	waitForRunTransitionHeld(t, source.host, started.Run().ID())
	// The per-client commit fence remains available while Suspend waits for the
	// model/tool safe boundary.
	lease, err := source.sessions.AcquireMutationCommit(t.Context(), source.session.ClientID(), source.session.Epoch())
	if err != nil {
		t.Fatalf("suspend held mutation gate across model wait: %v", err)
	}
	lease.Close()
	close(firstRelease)
	suspended := <-suspendedResult
	if suspended.err != nil || !suspended.value.Suspended() {
		t.Fatalf("suspend = %#v, %v", suspended.value, suspended.err)
	}
	suspendedSnapshot := <-source.authority.issued
	if suspendedSnapshot.Status() != agent.LifecycleSuspended ||
		suspendedSnapshot.LastSequence() != suspended.value.BoundarySequence() {
		t.Fatalf("issued suspend snapshot = %s at %d", suspendedSnapshot.Status(), suspendedSnapshot.LastSequence())
	}
	envelope := <-source.authority.envelopes
	if _, err = source.host.Export(t.Context(), source.session, started.Run()); err != nil {
		t.Fatalf("export suspended envelope: %v", err)
	}
	duplicateSuspend, err := source.host.Suspend(t.Context(), source.session, runMutation)
	if err != nil || !duplicateSuspend.DuplicateOperation() {
		t.Fatalf("duplicate suspend = %#v, %v", duplicateSuspend, err)
	}
	// This private authority seam proves host ordering and sequence continuity
	// while the captured SUSPENDED claim is still current. Durable HMAC/replay
	// rejection remains covered by RunAuthority's real-store tests.
	importRecorder := newHostRecorder()
	importProvider := &recordingImmediateHostProvider{recorder: importRecorder}
	target := newRunHostFixtureWithTools(
		t, importProvider, map[string]tool.Tool{"read": hostReadTool{}}, 2, 2,
	)
	target.authority.recorder = importRecorder
	target.authority.allowImport = true
	operation, _ := client.NewOperationID("import-operation")
	wireImport := &enginev1.ImportSnapshotRequest{
		ClientId: target.session.ClientID(), OwnershipEpoch: target.session.Epoch(),
		ClientOperationId: operation.String(), Snapshot: envelope,
	}
	if err = enginev1.ValidateImportSnapshotRequestStructure(wireImport, runHostWireLimits()); err != nil {
		t.Fatalf("wire import structure: %v", err)
	}
	importRequest, err := client.NewImportRequest(operation, marshalClientSnapshot(t, wireImport.GetSnapshot()))
	if err != nil {
		t.Fatal(err)
	}
	imported, err := target.host.Import(t.Context(), target.session, importRequest)
	if err != nil {
		t.Fatal(err)
	}
	if imported.Run().ID() != started.Run().ID() || imported.NextSequence() != suspended.value.BoundarySequence()+1 {
		t.Fatalf("import continuity = run %q sequence %d", imported.Run().ID(), imported.NextSequence())
	}
	<-target.authority.issued
	importEvents := importRecorder.values()
	if indexOf(importEvents, "authority.import.activate") < 0 || indexOf(importEvents, "provider.stream") < 0 ||
		indexOf(importEvents, "authority.import.activate") > indexOf(importEvents, "provider.stream") {
		t.Fatalf("import activation order = %v", importEvents)
	}

	abandonTarget := newRunHostFixtureWithTools(
		t, &recordingImmediateHostProvider{recorder: newHostRecorder()},
		map[string]tool.Tool{"read": hostReadTool{}}, 2, 2,
	)
	abandonTarget.authority.allowImport = true
	prepareContext, cancelPrepare := context.WithCancel(context.Background())
	abandonTarget.authority.prepareImportHook = cancelPrepare
	abandonOperation, _ := client.NewOperationID("import-preconsume-abandon")
	abandonRequest, _ := client.NewImportRequest(abandonOperation, marshalClientSnapshot(t, envelope))
	if _, err = abandonTarget.host.Import(prepareContext, abandonTarget.session, abandonRequest); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-consume canceled import = %v", err)
	}
	abandonTarget.authority.prepareImportHook = nil
	retriedImport, err := abandonTarget.host.Import(t.Context(), abandonTarget.session, abandonRequest)
	if err != nil || retriedImport.DuplicateOperation() {
		t.Fatalf("retry abandoned import = %#v, %v", retriedImport, err)
	}
	<-abandonTarget.authority.issued

	abortFailureTarget := newRunHostFixtureWithTools(
		t, &recordingImmediateHostProvider{recorder: newHostRecorder()},
		map[string]tool.Tool{"read": hostReadTool{}}, 2, 2,
	)
	abortFailureTarget.authority.allowImport = true
	abortContext, cancelAbortPrepare := context.WithCancel(context.Background())
	abortFailureTarget.authority.prepareImportHook = cancelAbortPrepare
	abortFailureTarget.authority.importAbortErr = ErrRunAuthorityUnavailable
	abortOperation, _ := client.NewOperationID("import-abort-failure")
	abortRequest, _ := client.NewImportRequest(abortOperation, marshalClientSnapshot(t, envelope))
	if _, err = abortFailureTarget.host.Import(abortContext, abortFailureTarget.session, abortRequest); !errors.Is(err, ErrRunHostUncertain) {
		t.Fatalf("pre-consume abort failure = %v", err)
	}
	health, healthErr := abortFailureTarget.host.Health(t.Context(), abortFailureTarget.session)
	if healthErr != nil || health.State() != client.HealthDegraded {
		t.Fatalf("abort failure health = %#v, %v", health, healthErr)
	}

	busyTarget := newRunHostFixtureWithTools(
		t, &recordingImmediateHostProvider{recorder: newHostRecorder()},
		map[string]tool.Tool{"read": hostReadTool{}}, 2, 2,
	)
	busyTarget.authority.allowImport = true
	busyTarget.authority.prepareImportErr = ErrRunAuthorityBusy
	busyOperation, _ := client.NewOperationID("import-busy-abandon")
	busyRequest, _ := client.NewImportRequest(busyOperation, marshalClientSnapshot(t, envelope))
	if _, err = busyTarget.host.Import(t.Context(), busyTarget.session, busyRequest); !errors.Is(err, ErrRunHostState) {
		t.Fatalf("busy import = %v", err)
	}
	busyTarget.authority.prepareImportErr = nil
	busyImport, err := busyTarget.host.Import(t.Context(), busyTarget.session, busyRequest)
	if err != nil || busyImport.DuplicateOperation() {
		t.Fatalf("retry busy import = %#v, %v", busyImport, err)
	}
	<-busyTarget.authority.issued

	resume := hostRunMutation(t, started.Run(), "resume-operation")
	resumed, err := source.host.Resume(t.Context(), source.session, resume)
	if err != nil || resumed.NextSequence() != suspended.value.BoundarySequence()+1 {
		t.Fatalf("resume = %#v, %v", resumed, err)
	}
	select {
	case <-sourceProvider.secondStarted:
	case <-time.After(time.Second):
		t.Fatal("provider did not continue after authority resume")
	}
	events := recorder.values()
	if indexOf(events, "authority.resume") > indexOf(events, "provider.second") ||
		indexOf(events, "authority.resume") < 0 {
		t.Fatalf("resume order = %v", events)
	}
	duplicateResume, err := source.host.Resume(t.Context(), source.session, resume)
	if err != nil || !duplicateResume.DuplicateOperation() {
		t.Fatalf("duplicate resume = %#v, %v", duplicateResume, err)
	}
	close(secondRelease)
	<-source.authority.issued
	<-source.authority.envelopes
}

type runHostFixture struct {
	host       *RunHost
	config     RunHostConfig
	authority  *recordingHostAuthority
	sessions   *SessionStore
	session    Session
	definition Definition
}

func (fixture *runHostFixture) pending() *PendingHub { return fixture.config.Pending }

func newRunHostFixture(t *testing.T, provider model.Provider, activeRuns uint32, terminalRuns int) *runHostFixture {
	t.Helper()
	return newRunHostFixtureWithTools(t, provider, nil, activeRuns, terminalRuns)
}

func newRunHostFixtureWithTools(
	t *testing.T,
	provider model.Provider,
	tools map[string]tool.Tool,
	activeRuns uint32,
	terminalRuns int,
) *runHostFixture {
	t.Helper()
	return newRunHostFixtureWithToolsAndTurns(t, provider, tools, activeRuns, terminalRuns, 2)
}

func newRunHostFixtureWithToolsAndTurns(
	t *testing.T,
	provider model.Provider,
	tools map[string]tool.Tool,
	activeRuns uint32,
	terminalRuns int,
	maxTurns uint32,
) *runHostFixture {
	t.Helper()
	pending, err := NewPendingHub(DefaultPendingLimits())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := stage.NewDispatcher(tools)
	if err != nil {
		t.Fatal(err)
	}
	source, err := stage.NewStaticToolPlanSource(dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	return newRunHostFixtureWithPlanSourceAndPendingTurns(
		t, provider, pending, source, activeRuns, terminalRuns, maxTurns,
	)
}

func newRunHostFixtureWithPlanSource(
	t *testing.T,
	provider model.Provider,
	source stage.ToolPlanSource,
	activeRuns uint32,
	terminalRuns int,
) *runHostFixture {
	t.Helper()
	pending, err := NewPendingHub(DefaultPendingLimits())
	if err != nil {
		t.Fatal(err)
	}
	return newRunHostFixtureWithPlanSourceAndPending(t, provider, pending, source, activeRuns, terminalRuns)
}

func newRunHostFixtureWithPlanSourceAndPending(
	t *testing.T,
	provider model.Provider,
	pending *PendingHub,
	source stage.ToolPlanSource,
	activeRuns uint32,
	terminalRuns int,
) *runHostFixture {
	t.Helper()
	return newRunHostFixtureWithPlanSourceAndPendingTurns(
		t, provider, pending, source, activeRuns, terminalRuns, 2,
	)
}

func newRunHostFixtureWithPlanSourceAndPendingTurns(
	t *testing.T,
	provider model.Provider,
	pending *PendingHub,
	source stage.ToolPlanSource,
	activeRuns uint32,
	terminalRuns int,
	maxTurns uint32,
) *runHostFixture {
	t.Helper()
	options := agent.DefaultEngineOptions()
	options.SnapshotCompatibilityIdentity = "run-host-tests:v1"
	engine, err := agent.NewEngineWithToolPlanSourceAndInteractionBroker(
		provider, source, pending, &agent.AtomicIDSource{},
		func() time.Time { return time.Unix(1, 0).UTC() }, nil, nil, options,
	)
	if err != nil {
		t.Fatal(err)
	}
	definitionValue, err := agent.NewDefinition("fixture", "scripted", maxTurns)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := NewDefinition("fixture", "revision-1", definitionValue)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := NewDefinitionSet([]Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := NewSessionStore(t.Context(), 4)
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := NewLedger(4, 64)
	if err != nil {
		t.Fatal(err)
	}
	limits, err := client.NewLimits(1<<20, 128, 128, 1<<20, 8, activeRuns)
	if err != nil {
		t.Fatal(err)
	}
	authority := newRecordingHostAuthority(t)
	configuration := RunHostConfig{
		Root: t.Context(), Engine: engine, Sessions: sessions, Ledger: ledger,
		Pending: pending, Definitions: definitions, Limits: limits,
		TerminalRuns: terminalRuns, TerminalBytes: 1 << 20, TransitionTimeout: time.Second,
	}
	host, err := newRunHost(configuration, authority)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &runHostFixture{
		host: host, config: configuration, authority: authority,
		sessions: sessions, session: session, definition: definition,
	}
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if shutdownErr := host.Shutdown(shutdownContext); shutdownErr != nil {
			t.Errorf("shutdown run host: %v", shutdownErr)
		}
	})
	return fixture
}

type failingReleasePlanSource struct {
	dispatcher stage.ToolDispatcher
	id         stage.PlanID
}

func (source failingReleasePlanSource) LeaseCurrent(ctx context.Context) (*stage.ToolPlanLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return stage.NewToolPlanLease(source.id, source.dispatcher, func() error {
		return errors.New("fixture release failed")
	})
}

func (source failingReleasePlanSource) LeaseGeneration(
	ctx context.Context,
	id stage.PlanID,
) (*stage.ToolPlanLease, error) {
	if id != source.id {
		return nil, errors.New("fixture plan is unavailable")
	}
	return source.LeaseCurrent(ctx)
}

func hostStartRequest(t *testing.T, operationValue string, definition Definition, messageID string) client.StartRequest {
	t.Helper()
	operation, _ := client.NewOperationID(operationValue)
	reference, _ := client.NewDefinitionRef(definition.ID(), definition.Revision())
	input, _ := client.NewInput(messageID, "hello")
	request, err := client.NewStartRequest(operation, reference, input)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

type hostRecorder struct {
	mu     sync.Mutex
	events []string
}

func newHostRecorder() *hostRecorder { return &hostRecorder{} }

func (recorder *hostRecorder) add(value string) {
	if recorder == nil {
		return
	}
	recorder.mu.Lock()
	recorder.events = append(recorder.events, value)
	recorder.mu.Unlock()
}

func (recorder *hostRecorder) values() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.events...)
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}

type immediateHostProvider struct{}

func (immediateHostProvider) Stream(context.Context, model.Request) (model.Stream, error) {
	completed, _ := model.Completed(model.NewUsage(1, 1))
	return &hostEventStream{events: []model.StreamEvent{completed}}, nil
}

type recordingImmediateHostProvider struct{ recorder *hostRecorder }

func (provider *recordingImmediateHostProvider) Stream(context.Context, model.Request) (model.Stream, error) {
	provider.recorder.add("provider.stream")
	completed, _ := model.Completed(model.NewUsage(1, 1))
	return &hostEventStream{events: []model.StreamEvent{completed}}, nil
}

type twoTurnHostProvider struct {
	recorder      *hostRecorder
	calls         atomic.Int32
	firstStarted  chan struct{}
	firstRelease  <-chan struct{}
	secondStarted chan struct{}
	secondRelease <-chan struct{}
	secondTool    bool
}

func (provider *twoTurnHostProvider) Stream(context.Context, model.Request) (model.Stream, error) {
	completed, _ := model.Completed(model.NewUsage(1, 1))
	if provider.calls.Add(1) == 1 {
		provider.recorder.add("provider.first")
		close(provider.firstStarted)
		call, _ := tool.NewCall(tool.CallID("call-1"), "read", json.RawMessage(`{}`))
		callEvent, _ := model.ToolCallEvent(call)
		return &hostEventStream{events: []model.StreamEvent{callEvent, completed}, release: provider.firstRelease}, nil
	}
	provider.recorder.add("provider.second")
	close(provider.secondStarted)
	events := []model.StreamEvent{completed}
	if provider.secondTool {
		call, _ := tool.NewCall(tool.CallID("call-2"), "read", json.RawMessage(`{}`))
		callEvent, _ := model.ToolCallEvent(call)
		events = []model.StreamEvent{callEvent, completed}
	}
	return &hostEventStream{events: events, release: provider.secondRelease}, nil
}

type hostReadTool struct{}

func (hostReadTool) Definition() tool.Definition {
	definition, _ := tool.NewDefinition(
		"read", "Read fixture data.", json.RawMessage(`{}`),
		tool.EffectReadOnly, tool.ReplaySafe, tool.CapabilityFilesystemRead,
	)
	return definition
}

func (hostReadTool) Execute(_ context.Context, call tool.Call, _ tool.Reporter) (tool.Result, error) {
	return tool.NewResult(call.ID(), json.RawMessage(`{"ok":true}`))
}

type gatedHostProvider struct {
	recorder *hostRecorder
	started  chan struct{}
	release  <-chan struct{}
	once     sync.Once
}

func (provider *gatedHostProvider) Stream(context.Context, model.Request) (model.Stream, error) {
	provider.recorder.add("provider.stream")
	provider.once.Do(func() { close(provider.started) })
	completed, _ := model.Completed(model.NewUsage(1, 1))
	return &hostEventStream{events: []model.StreamEvent{completed}, release: provider.release}, nil
}

type sequenceHostProvider struct {
	calls        atomic.Int32
	firstStarted chan struct{}
}

func (provider *sequenceHostProvider) Stream(context.Context, model.Request) (model.Stream, error) {
	completed, _ := model.Completed(model.NewUsage(1, 1))
	if provider.calls.Add(1) == 1 {
		close(provider.firstStarted)
		return &hostEventStream{blockUntilCancel: true}, nil
	}
	return &hostEventStream{events: []model.StreamEvent{completed}}, nil
}

type hostEventStream struct {
	events           []model.StreamEvent
	index            int
	release          <-chan struct{}
	blockUntilCancel bool
}

func (stream *hostEventStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	if stream.blockUntilCancel {
		<-ctx.Done()
		return model.StreamEvent{}, ctx.Err()
	}
	if stream.release != nil {
		select {
		case <-ctx.Done():
			return model.StreamEvent{}, ctx.Err()
		case <-stream.release:
			stream.release = nil
		}
	}
	if stream.index == len(stream.events) {
		return model.StreamEvent{}, io.EOF
	}
	value := stream.events[stream.index]
	stream.index++
	return value, nil
}

func (*hostEventStream) Close() error { return nil }

type recordingHostAuthority struct {
	recorder          *hostRecorder
	codec             *enginev1.HMACSnapshotAuthority
	issued            chan agent.Snapshot
	envelopes         chan *enginev1.SnapshotEnvelope
	starts            atomic.Int32
	startEntered      chan struct{}
	startRelease      <-chan struct{}
	startOnce         sync.Once
	allowImport       bool
	startErr          error
	prepareImportErr  error
	prepareImportHook func()
	nilImport         bool
	importAbortErr    error
	issueErr          error
	resumeErr         error
	consumeErr        error
	issueEntered      chan struct{}
	issueRelease      chan struct{}
	issueOnce         sync.Once
}

func newRecordingHostAuthority(t *testing.T) *recordingHostAuthority {
	t.Helper()
	codec, err := enginev1.NewHMACSnapshotAuthority(make([]byte, 32), 1, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	return &recordingHostAuthority{
		codec: codec, issued: make(chan agent.Snapshot, 32),
		envelopes: make(chan *enginev1.SnapshotEnvelope, 32),
	}
}

func (authority *recordingHostAuthority) Start(ctx context.Context, _ string) (hostActiveAuthority, error) {
	authority.starts.Add(1)
	if authority.startEntered != nil {
		authority.startOnce.Do(func() { close(authority.startEntered) })
	}
	if authority.startRelease != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-authority.startRelease:
		}
	}
	if authority.startErr != nil {
		return nil, authority.startErr
	}
	authority.recorder.add("authority.start")
	return &recordingHostActive{owner: authority}, nil
}

func (authority *recordingHostAuthority) PrepareImport(
	context.Context,
	*enginev1.SnapshotEnvelope,
) (hostImportAuthority, error) {
	if authority.prepareImportHook != nil {
		authority.prepareImportHook()
	}
	if authority.prepareImportErr != nil {
		return nil, authority.prepareImportErr
	}
	if !authority.allowImport {
		return nil, ErrRunAuthorityState
	}
	if authority.nilImport {
		return nil, nil //nolint:nilnil // adversarial fake proves the host rejects an invalid nil-success dependency result.
	}
	authority.recorder.add("authority.import.prepare")
	return &recordingHostImport{owner: authority}, nil
}

func (*recordingHostAuthority) Close() error { return nil }

type recordingHostActive struct{ owner *recordingHostAuthority }

func (active *recordingHostActive) Resume(context.Context) error {
	if active.owner.resumeErr != nil {
		return active.owner.resumeErr
	}
	active.owner.recorder.add("authority.resume")
	return nil
}

func (active *recordingHostActive) IssueSnapshotEnvelope(
	ctx context.Context,
	snapshot agent.Snapshot,
) (*enginev1.SnapshotEnvelope, error) {
	if active.owner.issueEntered != nil {
		active.owner.issueOnce.Do(func() { close(active.owner.issueEntered) })
	}
	if active.owner.issueRelease != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-active.owner.issueRelease:
		}
	}
	if active.owner.issueErr != nil {
		return nil, active.owner.issueErr
	}
	active.owner.issued <- snapshot
	lifecycle, ok := snapshotLifecycle(snapshot.Status())
	if !ok {
		return nil, ErrRunAuthorityState
	}
	payload, err := snapshot.MarshalBinary()
	if err != nil {
		return nil, err
	}
	envelope, err := enginev1.NewSnapshotEnvelope(
		ctx, active.owner.codec, snapshot.RunID(), snapshot.LastSequence(), lifecycle, payload,
	)
	if err == nil {
		active.owner.envelopes <- cloneEnvelope(envelope)
	}
	return envelope, err
}

func (*recordingHostActive) Terminal(context.Context, TerminalPhase) error { return nil }
func (*recordingHostActive) Close() error                                  { return nil }

type recordingHostImport struct{ owner *recordingHostAuthority }

func (transaction *recordingHostImport) VerifySnapshot(
	ctx context.Context,
	input enginev1.SnapshotAuthorityInput,
	claim *enginev1.SnapshotAuthority,
) error {
	return transaction.owner.codec.VerifySnapshot(ctx, input, claim)
}

func (transaction *recordingHostImport) Consume(context.Context) error {
	if transaction.owner.consumeErr != nil {
		return transaction.owner.consumeErr
	}
	transaction.owner.recorder.add("authority.import.consume")
	return nil
}

func (transaction *recordingHostImport) Activate(context.Context) (hostActiveAuthority, error) {
	transaction.owner.recorder.add("authority.import.activate")
	return &recordingHostActive{owner: transaction.owner}, nil
}

func (transaction *recordingHostImport) Abort() error { return transaction.owner.importAbortErr }

func waitForNoHostActive(t *testing.T, host *RunHost) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		host.mu.Lock()
		active := host.activeReserved
		host.mu.Unlock()
		if active == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("active run count = %d, want 0", active)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForRunTransitionHeld(t *testing.T, host *RunHost, runID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		host.mu.Lock()
		value := host.active[runID]
		host.mu.Unlock()
		if value != nil && !value.transition.TryLock() {
			return
		}
		if value != nil {
			value.transition.Unlock()
		}
		if time.Now().After(deadline) {
			t.Fatal("run transition was not acquired")
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForLedgerOperation(t *testing.T, ledger *Ledger, clientID, operationID string) *operationEntry {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	key := operationKey{clientID: clientID, operationID: operationID}
	for {
		ledger.mu.Lock()
		entry := ledger.entries[key]
		ledger.mu.Unlock()
		if entry != nil {
			return entry
		}
		if time.Now().After(deadline) {
			t.Fatal("operation was not admitted to the idempotency ledger")
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForPendingCount(t *testing.T, hub *PendingHub, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		hub.mu.Lock()
		pending := hub.pendingCount
		hub.mu.Unlock()
		if pending == count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending interaction count = %d, want %d", pending, count)
		}
		time.Sleep(time.Millisecond)
	}
}

func hostRunMutation(t *testing.T, run client.RunRef, operationValue string) client.RunMutation {
	t.Helper()
	operation, _ := client.NewOperationID(operationValue)
	request, err := client.NewRunMutation(run, operation)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func marshalClientSnapshot(t *testing.T, envelope *enginev1.SnapshotEnvelope) client.Snapshot {
	t.Helper()
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ParseSnapshot(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func runHostWireLimits() *commonv1.Limits {
	return &commonv1.Limits{
		MaxMessageBytes:    uint64(enginev1.MaximumSnapshotEnvelopeBytes + 1024),
		MaxCollectionItems: 1, MaxReplayEvents: 1, MaxReplayBytes: 1,
		MaxConcurrentStreams: 1, MaxActiveRuns: 1,
	}
}

func containsString(values []string, target string) bool {
	return slices.Contains(values, target)
}

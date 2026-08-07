package daemon

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

func TestRunHostTransitionWaitHonorsRequestCancellation(t *testing.T) {
	t.Parallel()
	firstRelease := make(chan struct{})
	provider := &twoTurnHostProvider{
		firstStarted: make(chan struct{}), firstRelease: firstRelease,
		secondStarted: make(chan struct{}), secondRelease: make(chan struct{}),
	}
	fixture := newRunHostFixtureWithTools(
		t, provider, map[string]tool.Tool{"read": hostReadTool{}}, 1, 2,
	)
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "transition-context-start", fixture.definition, "transition-context-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-provider.firstStarted
	suspendDone := make(chan error, 1)
	go func() {
		_, suspendErr := fixture.host.Suspend(
			context.Background(), fixture.session,
			hostRunMutation(t, started.Run(), "transition-context-suspend"),
		)
		suspendDone <- suspendErr
	}()
	waitForRunTransitionHeld(t, fixture.host, started.Run().ID())

	exportContext, cancelExport := context.WithTimeout(t.Context(), 20*time.Millisecond)
	_, exportErr := fixture.host.Export(exportContext, fixture.session, started.Run())
	cancelExport()
	if !errors.Is(exportErr, context.DeadlineExceeded) {
		t.Fatalf("export waiting for transition = %v", exportErr)
	}

	operation, _ := client.NewOperationID("transition-context-cancel")
	cancelRequest, _ := client.NewCancelRequest(started.Run(), operation, "cancel while transition is held")
	cancelContext, stopCancel := context.WithCancel(context.Background())
	cancelDone := make(chan error, 1)
	go func() {
		_, cancelErr := fixture.host.Cancel(cancelContext, fixture.session, cancelRequest)
		cancelDone <- cancelErr
	}()
	waitForLedgerOperation(t, fixture.host.ledger, fixture.session.ClientID(), operation.String())
	stopCancel()
	if cancelErr := <-cancelDone; !errors.Is(cancelErr, context.Canceled) {
		t.Fatalf("cancel waiting for transition = %v", cancelErr)
	}

	close(firstRelease)
	if suspendErr := <-suspendDone; suspendErr != nil {
		t.Fatal(suspendErr)
	}
	<-fixture.authority.issued
	if result, cancelErr := fixture.host.Cancel(t.Context(), fixture.session, cancelRequest); cancelErr != nil || !result.Requested() {
		t.Fatalf("retry canceled transition wait = %#v, %v", result, cancelErr)
	}
	<-fixture.authority.issued
}

func TestRunHostPrecommitSetupObservesSessionAndHostLifetime(t *testing.T) {
	t.Parallel()
	for _, stop := range []string{"reconnect", "shutdown"} {
		t.Run(stop, func(t *testing.T) {
			t.Parallel()
			dispatcher, err := stage.NewDispatcher(nil)
			if err != nil {
				t.Fatal(err)
			}
			planID, _ := stage.NewPlanID("context-plan-" + stop)
			source := &blockingFirstPlanSource{
				dispatcher: dispatcher, id: planID, entered: make(chan struct{}),
			}
			fixture := newRunHostFixtureWithPlanSource(t, immediateHostProvider{}, source, 1, 2)
			request := hostStartRequest(t, "context-setup-"+stop, fixture.definition, "context-setup-input-"+stop)
			startDone := make(chan error, 1)
			go func() {
				_, startErr := fixture.host.Start(context.Background(), fixture.session, request)
				startDone <- startErr
			}()
			<-source.entered

			switch stop {
			case "reconnect":
				reconnected, reconnectErr := fixture.sessions.Reconnect(
					fixture.session.ClientID(), fixture.session.Epoch(),
				)
				if reconnectErr != nil {
					t.Fatal(reconnectErr)
				}
				if startErr := <-startDone; !errors.Is(startErr, context.Canceled) {
					t.Fatalf("session-fenced setup = %v", startErr)
				}
				result, retryErr := fixture.host.Start(t.Context(), reconnected, request)
				if retryErr != nil || result.DuplicateOperation() {
					t.Fatalf("retry session-fenced setup = %#v, %v", result, retryErr)
				}
				<-fixture.authority.issued
			case "shutdown":
				shutdownContext, cancelShutdown := context.WithTimeout(t.Context(), time.Second)
				shutdownErr := fixture.host.Shutdown(shutdownContext)
				cancelShutdown()
				if shutdownErr != nil {
					t.Fatal(shutdownErr)
				}
				if startErr := <-startDone; !errors.Is(startErr, context.Canceled) {
					t.Fatalf("host-canceled setup = %v", startErr)
				}
			}
		})
	}
}

func TestRunHostRetriesProvenPrewriteAuthorityFailures(t *testing.T) {
	t.Parallel()
	startTarget := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	startTarget.authority.startErr = ErrRunAuthorityUnavailable
	startRequest := hostStartRequest(
		t, "prewrite-start", startTarget.definition, "prewrite-start-input",
	)
	if _, err := startTarget.host.Start(t.Context(), startTarget.session, startRequest); !errors.Is(err, ErrRunHostUnavailable) {
		t.Fatalf("prewrite start failure = %v", err)
	}
	startTarget.authority.startErr = nil
	started, err := startTarget.host.Start(t.Context(), startTarget.session, startRequest)
	if err != nil || started.DuplicateOperation() {
		t.Fatalf("retry prewrite start = %#v, %v", started, err)
	}
	<-startTarget.authority.issued

	source, sourceRun, envelope, boundary := suspendedHostEnvelope(t)
	prepareTarget := newRunHostFixtureWithTools(
		t, immediateHostProvider{}, map[string]tool.Tool{"read": hostReadTool{}}, 1, 2,
	)
	prepareTarget.authority.allowImport = true
	prepareTarget.authority.prepareImportErr = ErrRunAuthorityUnavailable
	prepareOperation, _ := client.NewOperationID("prewrite-prepare-import")
	prepareRequest, _ := client.NewImportRequest(prepareOperation, marshalClientSnapshot(t, envelope))
	if _, err = prepareTarget.host.Import(t.Context(), prepareTarget.session, prepareRequest); !errors.Is(err, ErrRunHostUnavailable) {
		t.Fatalf("prewrite prepare import = %v", err)
	}
	prepareTarget.authority.prepareImportErr = nil
	preparedImport, err := prepareTarget.host.Import(t.Context(), prepareTarget.session, prepareRequest)
	if err != nil || preparedImport.DuplicateOperation() {
		t.Fatalf("retry prewrite prepare import = %#v, %v", preparedImport, err)
	}
	<-prepareTarget.authority.issued

	consumeTarget := newRunHostFixtureWithTools(
		t, immediateHostProvider{}, map[string]tool.Tool{"read": hostReadTool{}}, 1, 2,
	)
	consumeTarget.authority.allowImport = true
	consumeTarget.authority.consumeErr = ErrRunAuthorityUnavailable
	consumeOperation, _ := client.NewOperationID("prewrite-consume-import")
	consumeRequest, _ := client.NewImportRequest(consumeOperation, marshalClientSnapshot(t, envelope))
	if _, err = consumeTarget.host.Import(t.Context(), consumeTarget.session, consumeRequest); !errors.Is(err, ErrRunHostUnavailable) {
		t.Fatalf("prewrite consume import = %v", err)
	}
	consumeTarget.authority.consumeErr = nil
	consumedImport, err := consumeTarget.host.Import(t.Context(), consumeTarget.session, consumeRequest)
	if err != nil || consumedImport.DuplicateOperation() {
		t.Fatalf("retry prewrite consume import = %#v, %v", consumedImport, err)
	}
	<-consumeTarget.authority.issued

	resumeRequest := hostRunMutation(t, sourceRun, "prewrite-resume")
	source.authority.resumeErr = ErrRunAuthorityUnavailable
	if _, err = source.host.Resume(t.Context(), source.session, resumeRequest); !errors.Is(err, ErrRunHostUnavailable) {
		t.Fatalf("prewrite resume = %v", err)
	}
	source.authority.resumeErr = nil
	resumed, err := source.host.Resume(t.Context(), source.session, resumeRequest)
	if err != nil || resumed.DuplicateOperation() || resumed.NextSequence() != boundary+1 {
		t.Fatalf("retry prewrite resume = %#v, %v", resumed, err)
	}
	close(source.secondRelease)
	<-source.authority.issued
}

func TestRunHostRetriesPrewriteSuspendIssuance(t *testing.T) {
	t.Parallel()
	firstRelease := make(chan struct{})
	secondRelease := make(chan struct{})
	provider := &twoTurnHostProvider{
		firstStarted: make(chan struct{}), firstRelease: firstRelease,
		secondStarted: make(chan struct{}), secondRelease: secondRelease,
		secondTool: true,
	}
	fixture := newRunHostFixtureWithToolsAndTurns(
		t, provider, map[string]tool.Tool{"read": hostReadTool{}}, 1, 2, 3,
	)
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "prewrite-suspend-start", fixture.definition, "prewrite-suspend-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-provider.firstStarted
	request := hostRunMutation(t, started.Run(), "prewrite-suspend")
	fixture.authority.issueErr = ErrRunAuthorityUnavailable
	firstDone := make(chan error, 1)
	go func() {
		_, suspendErr := fixture.host.Suspend(context.Background(), fixture.session, request)
		firstDone <- suspendErr
	}()
	waitForRunTransitionHeld(t, fixture.host, started.Run().ID())
	close(firstRelease)
	if suspendErr := <-firstDone; !errors.Is(suspendErr, ErrRunHostUnavailable) {
		t.Fatalf("prewrite suspend issuance = %v", suspendErr)
	}
	<-provider.secondStarted
	fixture.authority.issueErr = nil
	retryDone := make(chan struct {
		result client.SuspendResult
		err    error
	}, 1)
	go func() {
		result, suspendErr := fixture.host.Suspend(context.Background(), fixture.session, request)
		retryDone <- struct {
			result client.SuspendResult
			err    error
		}{result: result, err: suspendErr}
	}()
	waitForRunTransitionHeld(t, fixture.host, started.Run().ID())
	close(secondRelease)
	retried := <-retryDone
	if retried.err != nil || retried.result.DuplicateOperation() || !retried.result.Suspended() {
		t.Fatalf("retry prewrite suspend = %#v, %v", retried.result, retried.err)
	}
	<-fixture.authority.issued
	cancelOperation, _ := client.NewOperationID("prewrite-suspend-cleanup")
	cancelRequest, _ := client.NewCancelRequest(started.Run(), cancelOperation, "cleanup")
	if _, err = fixture.host.Cancel(t.Context(), fixture.session, cancelRequest); err != nil {
		t.Fatal(err)
	}
	<-fixture.authority.issued
}

type suspendedHost struct {
	*runHostFixture
	secondRelease chan struct{}
}

func suspendedHostEnvelope(t *testing.T) (*suspendedHost, client.RunRef, *enginev1.SnapshotEnvelope, uint64) {
	t.Helper()
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
		hostStartRequest(t, "prewrite-source-start", fixture.definition, "prewrite-source-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-provider.firstStarted
	suspendDone := make(chan struct {
		result client.SuspendResult
		err    error
	}, 1)
	go func() {
		result, suspendErr := fixture.host.Suspend(
			context.Background(), fixture.session,
			hostRunMutation(t, started.Run(), "prewrite-source-suspend"),
		)
		suspendDone <- struct {
			result client.SuspendResult
			err    error
		}{result: result, err: suspendErr}
	}()
	waitForRunTransitionHeld(t, fixture.host, started.Run().ID())
	close(firstRelease)
	suspended := <-suspendDone
	if suspended.err != nil {
		t.Fatal(suspended.err)
	}
	<-fixture.authority.issued
	envelope := <-fixture.authority.envelopes
	return &suspendedHost{runHostFixture: fixture, secondRelease: secondRelease},
		started.Run(), envelope, suspended.result.BoundarySequence()
}

type blockingFirstPlanSource struct {
	dispatcher stage.ToolDispatcher
	id         stage.PlanID
	entered    chan struct{}
	once       sync.Once
	calls      atomic.Uint32
}

func (source *blockingFirstPlanSource) LeaseCurrent(ctx context.Context) (*stage.ToolPlanLease, error) {
	if source.calls.Add(1) == 1 {
		source.once.Do(func() { close(source.entered) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return stage.NewToolPlanLease(source.id, source.dispatcher, func() error { return nil })
}

func (source *blockingFirstPlanSource) LeaseGeneration(
	ctx context.Context,
	id stage.PlanID,
) (*stage.ToolPlanLease, error) {
	if id != source.id {
		return nil, errors.New("fixture plan generation is unavailable")
	}
	return source.LeaseCurrent(ctx)
}

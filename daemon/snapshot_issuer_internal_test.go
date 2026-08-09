package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/daemon/internal/runauthority"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/stage"
)

type failingSnapshotAuthority struct {
	issueErr       error
	signErr        error
	calls          int
	preflight      func(int) error
	preflightCalls int
}

func (authority *failingSnapshotAuthority) SignSnapshot(
	context.Context,
	enginev1.SnapshotAuthorityInput,
) (*enginev1.SnapshotAuthority, error) {
	authority.calls++
	return nil, authority.signErr
}

func (authority *failingSnapshotAuthority) SnapshotIssueError() error {
	return authority.issueErr
}

func (authority *failingSnapshotAuthority) SnapshotIssuePreflight(string) error {
	authority.preflightCalls++
	if authority.preflight == nil {
		return nil
	}
	return authority.preflight(authority.preflightCalls)
}

func TestSnapshotEnvelopeIssuerPreservesSafeDurabilityClassification(t *testing.T) {
	snapshot := internalKernelSnapshot(t, "classified-run", agent.LifecycleSuspended)
	for _, test := range []struct {
		name      string
		issueErr  error
		cancelled bool
		wantErr   error
	}{
		{"state", runauthority.ErrState, false, ErrRunAuthorityState},
		{"cancelled state", runauthority.ErrState, true, context.Canceled},
		{"uncertain", runauthority.ErrUncertain, false, ErrRunAuthorityUncertain},
		{"cancelled uncertain", runauthority.ErrUncertain, true, ErrRunAuthorityUncertain},
		{"unavailable", runauthority.ErrUnavailable, false, ErrRunAuthorityUnavailable},
		{"cancelled unavailable", runauthority.ErrUnavailable, true, ErrRunAuthorityUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			authority := &failingSnapshotAuthority{
				issueErr: test.issueErr,
				signErr:  errors.New("snapshot-issuer-secret"),
			}
			_, err := issueSnapshotEnvelope(ctx, snapshot, authority)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("issuer error = %v, want %v", err, test.wantErr)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("issuer error exposed signer detail: %v", err)
			}
			if test.cancelled && authority.calls != 0 {
				t.Fatalf("pre-cancelled issuer called signer %d times", authority.calls)
			}
		})
	}
}

func TestSnapshotEnvelopeIssuerDoesNotLetCancellationMaskConcurrentClose(t *testing.T) {
	snapshot := internalKernelSnapshot(t, "close-race", agent.LifecycleSuspended)
	ctx, cancel := context.WithCancel(context.Background())
	authority := &failingSnapshotAuthority{issueErr: runauthority.ErrState}
	authority.preflight = func(call int) error {
		if call == 1 {
			cancel()
			return nil
		}
		return runauthority.ErrState
	}
	_, err := issueSnapshotEnvelope(ctx, snapshot, authority)
	if !errors.Is(err, ErrRunAuthorityState) {
		t.Fatalf("concurrent close/cancellation error = %v", err)
	}
	if authority.calls != 0 || authority.preflightCalls != 2 {
		t.Fatalf("signer calls = %d, preflight calls = %d", authority.calls, authority.preflightCalls)
	}
}

func internalKernelSnapshot(t *testing.T, runID string, status agent.LifecycleStatus) agent.Snapshot {
	t.Helper()
	definition, err := agent.NewDefinition("test", "scripted", 3)
	if err != nil {
		t.Fatal(err)
	}
	planID, err := stage.NewPlanID("generation-1")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := agent.NewPlanIdentity([]string{"provider:scripted"}, "compatibility-1", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", planID, nil)
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := message.NewID("message-1")
	if err != nil {
		t.Fatal(err)
	}
	part, err := message.Text("hello")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := message.New(messageID, message.RoleUser, part)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := agent.NewSnapshot(runID, definition, 1, []message.Message{initial}, plan, 7, status)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

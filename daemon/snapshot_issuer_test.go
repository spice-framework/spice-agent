package daemon_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/stage"
	"google.golang.org/protobuf/proto"
)

func TestActiveRunIssuesDeterministicSuspendedSnapshotEnvelope(t *testing.T) {
	authority := newRunAuthority(t, filepath.Join(authorityTestRoot(t), "issuer"))
	active, err := authority.Start(t.Context(), "issue-suspended")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := kernelSnapshot(t, "issue-suspended", agent.LifecycleSuspended)

	const issuers = 32
	results := make(chan *enginev1.SnapshotEnvelope, issuers)
	errorsSeen := make(chan error, issuers)
	var group sync.WaitGroup
	for range issuers {
		group.Go(func() {
			envelope, issueErr := active.IssueSnapshotEnvelope(context.Background(), snapshot)
			results <- envelope
			errorsSeen <- issueErr
		})
	}
	group.Wait()
	close(results)
	close(errorsSeen)
	for issueErr := range errorsSeen {
		if issueErr != nil {
			t.Fatalf("issue suspended snapshot: %v", issueErr)
		}
	}
	var expected []byte
	for envelope := range results {
		if err = enginev1.ValidateSnapshotEnvelope(envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.GetLifecycle() != enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED {
			t.Fatalf("snapshot lifecycle = %s", envelope.GetLifecycle())
		}
		parsed, parseErr := agent.ParseSnapshot(envelope.GetPayload())
		if parseErr != nil || parsed.RunID() != snapshot.RunID() || parsed.Status() != snapshot.Status() {
			t.Fatalf("snapshot payload = %#v, %v", parsed, parseErr)
		}
		wire, marshalErr := proto.MarshalOptions{Deterministic: true}.Marshal(envelope)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if expected == nil {
			expected = wire
		} else if !bytes.Equal(wire, expected) {
			t.Fatal("identical suspended snapshot issuance was not byte deterministic")
		}
	}
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestActiveRunSnapshotIssuanceValidationCancellationAndInvalidation(t *testing.T) {
	authority := newRunAuthority(t, filepath.Join(authorityTestRoot(t), "issuer"))
	active, err := authority.Start(t.Context(), "issue-boundary")
	if err != nil {
		t.Fatal(err)
	}
	valid := kernelSnapshot(t, "issue-boundary", agent.LifecycleSuspended)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = active.IssueSnapshotEnvelope(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled issuance = %v", err)
	}
	envelope, err := active.IssueSnapshotEnvelope(t.Context(), valid)
	if err != nil {
		t.Fatalf("retry after pre-cancelled issuance = %v", err)
	}
	if err = active.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = authority.PrepareImport(t.Context(), envelope); !errors.Is(err, daemon.ErrRunAuthorityVerification) {
		t.Fatalf("old claim after resume = %v", err)
	}

	other, err := authority.Start(t.Context(), "issue-invalid")
	if err != nil {
		t.Fatal(err)
	}
	for name, snapshot := range map[string]agent.Snapshot{
		"zero":         {},
		"wrong run ID": kernelSnapshot(t, "other-run", agent.LifecycleSuspended),
	} {
		if _, issueErr := other.IssueSnapshotEnvelope(t.Context(), snapshot); !errors.Is(issueErr, daemon.ErrRunAuthorityState) {
			t.Errorf("%s snapshot issuance = %v", name, issueErr)
		}
	}
	preCancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, issueErr := other.IssueSnapshotEnvelope(
		preCancelled,
		kernelSnapshot(t, "other-run", agent.LifecycleSuspended),
	); !errors.Is(issueErr, daemon.ErrRunAuthorityState) {
		t.Fatalf("pre-cancelled wrong-run snapshot issuance = %v", issueErr)
	}
	if err = other.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = other.IssueSnapshotEnvelope(t.Context(), kernelSnapshot(t, "issue-invalid", agent.LifecycleSuspended)); !errors.Is(err, daemon.ErrRunAuthorityState) {
		t.Fatalf("closed run issuance = %v", err)
	}
	var nilRun *daemon.ActiveRun
	if _, err = nilRun.IssueSnapshotEnvelope(t.Context(), valid); !errors.Is(err, daemon.ErrRunAuthorityState) {
		t.Fatalf("nil run issuance = %v", err)
	}
}

func TestActiveRunIssuesTerminalTombstones(t *testing.T) {
	authority := newRunAuthority(t, filepath.Join(authorityTestRoot(t), "issuer"))
	for _, test := range []struct {
		name      string
		status    agent.LifecycleStatus
		lifecycle enginev1.SnapshotLifecycle
	}{
		{"completed", agent.LifecycleCompleted, enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_COMPLETED},
		{"failed", agent.LifecycleFailed, enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_FAILED},
		{"cancelled", agent.LifecycleCancelled, enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_CANCELLED},
	} {
		t.Run(test.name, func(t *testing.T) {
			runID := "issue-terminal-" + test.name
			active, startErr := authority.Start(t.Context(), runID)
			if startErr != nil {
				t.Fatal(startErr)
			}
			envelope, issueErr := active.IssueSnapshotEnvelope(t.Context(), kernelSnapshot(t, runID, test.status))
			if issueErr != nil {
				t.Fatal(issueErr)
			}
			if envelope.GetLifecycle() != test.lifecycle {
				t.Fatalf("terminal lifecycle = %s", envelope.GetLifecycle())
			}
			if _, issueErr = active.IssueSnapshotEnvelope(t.Context(), kernelSnapshot(t, runID, test.status)); !errors.Is(issueErr, daemon.ErrRunAuthorityState) {
				t.Fatalf("repeated terminal issuance = %v", issueErr)
			}
			if _, issueErr = authority.PrepareImport(t.Context(), envelope); !errors.Is(issueErr, daemon.ErrRunAuthorityVerification) {
				t.Fatalf("terminal import = %v", issueErr)
			}
			if _, issueErr = authority.Start(t.Context(), runID); !errors.Is(issueErr, daemon.ErrRunAuthorityState) {
				t.Fatalf("terminal identity reuse = %v", issueErr)
			}
			if issueErr = active.Close(); issueErr != nil {
				t.Fatalf("terminal close = %v", issueErr)
			}
		})
	}
}

func kernelSnapshot(t *testing.T, runID string, status agent.LifecycleStatus) agent.Snapshot {
	t.Helper()
	return kernelSnapshotWith(t, runID, status, 7, "hello")
}

func kernelSnapshotWith(
	t *testing.T,
	runID string,
	status agent.LifecycleStatus,
	lastSequence uint64,
	text string,
) agent.Snapshot {
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
	part, err := message.Text(text)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := message.New(messageID, message.RoleUser, part)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := agent.NewSnapshot(runID, definition, 1, []message.Message{initial}, plan, lastSequence, status)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

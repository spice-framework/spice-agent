package daemon_test

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
)

func TestActiveRunIssuesRealCompletedAgentSnapshot(t *testing.T) {
	completed, err := model.Completed(model.NewUsage(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := stage.NewDispatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := agent.NewEngine(
		agentIssuerProvider{events: []model.StreamEvent{completed}},
		dispatcher,
		&agent.AtomicIDSource{},
		func() time.Time { return time.Unix(1, 0).UTC() },
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := engine.Close(context.Background()); closeErr != nil {
			t.Errorf("close agent engine: %v", closeErr)
		}
	})
	definition, err := agent.NewDefinition("issuer", "scripted", 1)
	if err != nil {
		t.Fatal(err)
	}
	input, err := agent.NewInput(agentIssuerMessage(t))
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.Start(t.Context(), definition, input)
	if err != nil {
		t.Fatal(err)
	}
	authority := newRunAuthority(t, filepath.Join(authorityTestRoot(t), "agent-issuer"))
	active, err := authority.Start(t.Context(), run.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err = run.Wait(t.Context()); err != nil {
		t.Fatalf("wait for completed run: %v", err)
	}
	snapshot, err := run.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status() != agent.LifecycleCompleted {
		t.Fatalf("agent snapshot status = %s", snapshot.Status())
	}
	envelope, err := active.IssueSnapshotEnvelope(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.GetLifecycle() != enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_COMPLETED {
		t.Fatalf("issued lifecycle = %s", envelope.GetLifecycle())
	}
	parsed, err := agent.ParseSnapshot(envelope.GetPayload())
	if err != nil {
		t.Fatal(err)
	}
	originalWire, err := snapshot.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	parsedWire, err := parsed.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(originalWire, parsedWire) || parsed.RunID() != run.ID() ||
		parsed.Status() != agent.LifecycleCompleted {
		t.Fatalf("parsed agent snapshot = run %q, status %s", parsed.RunID(), parsed.Status())
	}
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestActiveRunIssuedSuspendedSnapshotBecomesImportableAfterClose(t *testing.T) {
	authority := newRunAuthority(t, filepath.Join(authorityTestRoot(t), "agent-issuer-import"))
	active, err := authority.Start(t.Context(), "issued-suspended-import")
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := active.IssueSnapshotEnvelope(
		t.Context(),
		kernelSnapshot(t, "issued-suspended-import", agent.LifecycleSuspended),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	prepared, err := authority.PrepareImport(t.Context(), envelope)
	if err != nil {
		t.Fatalf("prepare issued suspended snapshot: %v", err)
	}
	if err = prepared.Abort(); err != nil {
		t.Fatal(err)
	}
}

func agentIssuerMessage(t *testing.T) message.Message {
	t.Helper()
	part, err := message.Text("hello")
	if err != nil {
		t.Fatal(err)
	}
	id, err := message.NewID("input")
	if err != nil {
		t.Fatal(err)
	}
	value, err := message.New(id, message.RoleUser, part)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type agentIssuerProvider struct {
	events []model.StreamEvent
}

func (provider agentIssuerProvider) Stream(context.Context, model.Request) (model.Stream, error) {
	return &agentIssuerStream{events: append([]model.StreamEvent(nil), provider.events...)}, nil
}

type agentIssuerStream struct {
	events []model.StreamEvent
	index  int
}

func (stream *agentIssuerStream) Recv(context.Context) (model.StreamEvent, error) {
	if stream.index == len(stream.events) {
		return model.StreamEvent{}, io.EOF
	}
	value := stream.events[stream.index]
	stream.index++
	return value, nil
}

func (*agentIssuerStream) Close() error { return nil }

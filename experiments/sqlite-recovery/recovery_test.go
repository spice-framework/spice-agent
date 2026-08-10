package sqliterecovery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

func TestStoreConfigurationStartAndClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.db")
	store := openTestStore(t, path)
	for query, want := range map[string]string{
		`PRAGMA journal_mode`: "wal", `PRAGMA synchronous`: "2", `PRAGMA foreign_keys`: "1", `PRAGMA trusted_schema`: "0",
	} {
		var got string
		if err := store.db.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Fatalf("%s = %q, %v; want %q", query, got, err, want)
		}
	}
	recorder, err := store.Start(t.Context(), testSeed("run-1"))
	if err != nil || recorder == nil {
		t.Fatalf("Start() = %v, %v", recorder, err)
	}
	if _, err = store.Start(t.Context(), testSeed("run-1")); err == nil {
		t.Fatal("duplicate Start succeeded")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
}

type fakeCheckpointRun struct {
	snapshot                          agent.Snapshot
	suspendErr, exportErr, prepareErr error
	prepared                          *fakePreparedResume
	order                             *[]string
}

func (run fakeCheckpointRun) Suspend(context.Context) error {
	*run.order = append(*run.order, "suspend")
	return run.suspendErr
}

func (run fakeCheckpointRun) ExportSnapshot() (agent.Snapshot, error) {
	*run.order = append(*run.order, "export")
	return run.snapshot, run.exportErr
}

func (run fakeCheckpointRun) Prepare() (resumeReservation, error) {
	*run.order = append(*run.order, "prepare")
	return run.prepared, run.prepareErr
}

type fakePreparedResume struct {
	runID     string
	next      uint64
	order     *[]string
	commitErr error
	aborted   bool
}

func (prepared *fakePreparedResume) RunID() string        { return prepared.runID }
func (prepared *fakePreparedResume) NextSequence() uint64 { return prepared.next }
func (prepared *fakePreparedResume) Commit() error {
	*prepared.order = append(*prepared.order, "commit")
	return prepared.commitErr
}

func (prepared *fakePreparedResume) Abort() error {
	prepared.aborted = true
	*prepared.order = append(*prepared.order, "abort")
	return nil
}

func TestCheckpointOrdersReservationDurabilityCommitAndAbortsFailure(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "checkpoint.db"))
	defer store.Close()
	snapshot, _, _ := fixtureCheckpoint(t, store, "run-checkpoint", 3)
	if _, err := store.db.Exec(`DELETE FROM checkpoints WHERE run_id = ?`, snapshot.RunID()); err != nil {
		t.Fatal(err)
	}
	order := []string{}
	prepared := &fakePreparedResume{runID: snapshot.RunID(), next: snapshot.LastSequence() + 1, order: &order}
	checkpoint, err := store.checkpoint(t.Context(), fakeCheckpointRun{snapshot: snapshot, prepared: prepared, order: &order})
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.RunID != snapshot.RunID() || strings.Join(order, ",") != "suspend,export,prepare,commit" || prepared.aborted {
		t.Fatalf("checkpoint=%+v order=%v aborted=%t", checkpoint, order, prepared.aborted)
	}

	missing := snapshotFor(t, "missing-run", 2)
	order = nil
	prepared = &fakePreparedResume{runID: missing.RunID(), next: missing.LastSequence() + 1, order: &order}
	if _, err = store.checkpoint(t.Context(), fakeCheckpointRun{snapshot: missing, prepared: prepared, order: &order}); err == nil || !prepared.aborted {
		t.Fatalf("storage failure = %v, aborted=%t", err, prepared.aborted)
	}
	if _, err = store.Checkpoint(t.Context(), nil); err == nil {
		t.Fatal("nil checkpoint run succeeded")
	}
	terminalDefinition, _ := agent.NewDefinition("terminal", "model", 1)
	part, _ := message.Text("done")
	terminalMessage, _ := message.New("terminal-message", message.RoleUser, part)
	_, terminalPlan := testToolPlan(t, tool.EffectReadOnly, tool.ReplaySafe)
	terminalSnapshot, _ := agent.NewSnapshot("terminal-run", terminalDefinition, 1, []message.Message{terminalMessage}, terminalPlan, 1, agent.LifecycleCompleted)
	if _, err = store.persistCheckpoint(t.Context(), terminalSnapshot); err == nil {
		t.Fatal("terminal snapshot persisted as resumable checkpoint")
	}
	_, differentPlan := testToolPlan(t, tool.EffectMutating, tool.ReplayUnsafe)
	if _, err = store.persistCheckpoint(t.Context(), snapshotForPlan(t, snapshot.RunID(), snapshot.LastSequence(), differentPlan)); err == nil || !strings.Contains(err.Error(), "authority") {
		t.Fatalf("mismatched snapshot authority = %v", err)
	}
	for _, fixture := range []fakeCheckpointRun{
		{suspendErr: errors.New("suspend"), order: &order},
		{snapshot: snapshot, exportErr: errors.New("export"), order: &order},
		{snapshot: snapshot, prepareErr: errors.New("prepare"), order: &order},
	} {
		order = nil
		fixture.order = &order
		if _, err = store.checkpoint(t.Context(), fixture); err == nil {
			t.Fatal("checkpoint stage failure succeeded")
		}
	}
	if _, err = store.db.Exec(`DELETE FROM checkpoints WHERE run_id = ?`, snapshot.RunID()); err != nil {
		t.Fatal(err)
	}
	order = nil
	prepared = &fakePreparedResume{runID: "wrong-run", next: snapshot.LastSequence() + 1, order: &order}
	if _, err = store.checkpoint(t.Context(), fakeCheckpointRun{snapshot: snapshot, prepared: prepared, order: &order}); err == nil || !prepared.aborted {
		t.Fatalf("reservation mismatch = %v, aborted=%t", err, prepared.aborted)
	}
	if _, err = store.db.Exec(`DELETE FROM checkpoints WHERE run_id = ?`, snapshot.RunID()); err != nil {
		t.Fatal(err)
	}
	order = nil
	prepared = &fakePreparedResume{runID: snapshot.RunID(), next: snapshot.LastSequence() + 1, order: &order, commitErr: errors.New("commit")}
	if _, err = store.checkpoint(t.Context(), fakeCheckpointRun{snapshot: snapshot, prepared: prepared, order: &order}); err == nil || !prepared.aborted {
		t.Fatalf("commit failure = %v, aborted=%t", err, prepared.aborted)
	}
}

type checkpointProvider struct {
	mu      sync.Mutex
	request int
	call    tool.Call
}

func (provider *checkpointProvider) Stream(context.Context, model.Request) (model.Stream, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.request++
	completed, _ := model.Completed(model.NewUsage(1, 1))
	if provider.request == 1 {
		toolEvent, _ := model.ToolCallEvent(provider.call)
		return &checkpointStream{events: []model.StreamEvent{toolEvent, completed}}, nil
	}
	delta, _ := model.TextDelta("resumed")
	return &checkpointStream{events: []model.StreamEvent{delta, completed}}, nil
}

type checkpointStream struct {
	events []model.StreamEvent
	index  int
}

func (stream *checkpointStream) Recv(context.Context) (model.StreamEvent, error) {
	if stream.index >= len(stream.events) {
		return model.StreamEvent{}, io.EOF
	}
	result := stream.events[stream.index]
	stream.index++
	return result, nil
}
func (*checkpointStream) Close() error { return nil }

type checkpointDispatcher struct {
	definition tool.Definition
	started    chan struct{}
	release    chan struct{}
}

func (dispatcher *checkpointDispatcher) Definitions() []tool.Definition {
	return []tool.Definition{dispatcher.definition}
}

func (dispatcher *checkpointDispatcher) Definition(name string) (tool.Definition, bool) {
	return dispatcher.definition, name == dispatcher.definition.Name()
}

func (dispatcher *checkpointDispatcher) Dispatch(ctx context.Context, _ stage.ToolDispatchScope, call tool.Call, _ tool.Reporter) (tool.Result, error) {
	close(dispatcher.started)
	select {
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	case <-dispatcher.release:
		return tool.NewResult(call.ID(), json.RawMessage(`"ok"`))
	}
}

func TestCheckpointWithRealRunUsesPublicLifecycle(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "real-checkpoint.db"))
	defer store.Close()
	definition, _ := testToolPlan(t, tool.EffectReadOnly, tool.ReplaySafe)
	dispatcher := &checkpointDispatcher{definition: definition, started: make(chan struct{}), release: make(chan struct{})}
	source, err := stage.NewStaticToolPlanSource(dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := source.LeaseCurrent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	planID := lease.ToolPlanID()
	if err = lease.Release(); err != nil {
		t.Fatal(err)
	}
	options := agent.DefaultEngineOptions()
	options.CompiledPlanIdentities = []string{"provider:fixture", "stage:fixture"}
	options.SnapshotCompatibilityIdentity = "snapshot:fixture-v1"
	options.WorkspaceFingerprint = "sha256:" + strings.Repeat("a", 64)
	plan, err := agent.NewPlanIdentity(options.CompiledPlanIdentities, options.SnapshotCompatibilityIdentity, options.WorkspaceFingerprint, planID, []tool.Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	seedDigest := sha256.Sum256([]byte("real-checkpoint"))
	recorder, err := store.Start(t.Context(), RunSeed{RunID: "run-1", SeedDigest: seedDigest, PlanFingerprint: plan.Fingerprint(), WorkspaceFingerprint: plan.WorkspaceFingerprint(), ToolPlanID: planID.String()})
	if err != nil {
		t.Fatal(err)
	}
	call, _ := tool.NewCall("call-1", "fixture", json.RawMessage(`{}`))
	engine, err := agent.NewEngineWithToolPlanSource(&checkpointProvider{call: call}, source, &agent.AtomicIDSource{}, time.Now, []event.Observer{recorder}, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(t.Context())
	part, _ := message.Text("checkpoint")
	initial, _ := message.New("input-1", message.RoleUser, part)
	input, _ := agent.NewInput(initial)
	agentDefinition, _ := agent.NewDefinition("checkpoint", "fixture-model", 2)
	run, err := engine.Start(t.Context(), agentDefinition, input)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-dispatcher.started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not start")
	}
	result := make(chan error, 1)
	go func() {
		_, checkpointErr := store.Checkpoint(context.Background(), run)
		result <- checkpointErr
	}()
	time.Sleep(20 * time.Millisecond)
	close(dispatcher.release)
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	if err = run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsInvalidAndFutureStores(t *testing.T) {
	for _, options := range []Options{{BusyTimeout: -1}, {BusyTimeout: maximumBusy + time.Millisecond}, {MaxBranches: maximumDepth + 1}} {
		if store, err := Open(t.Context(), filepath.Join(t.TempDir(), "bad.db"), options); err == nil || store != nil {
			t.Fatalf("Open with %+v succeeded", options)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if store, err := Open(canceled, filepath.Join(t.TempDir(), "cancel.db"), Options{}); !errors.Is(err, context.Canceled) || store != nil {
		t.Fatalf("canceled Open = %v, %v", store, err)
	}
	path := filepath.Join(t.TempDir(), "future.db")
	store := openTestStore(t, path)
	if _, err := store.db.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(t.Context(), path, Options{}); err == nil || reopened != nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("future Open = %v, %v", reopened, err)
	}
	applicationPath := filepath.Join(t.TempDir(), "application.db")
	applicationStore := openTestStore(t, applicationPath)
	if _, err := applicationStore.db.Exec(`PRAGMA application_id = 7`); err != nil {
		t.Fatal(err)
	}
	_ = applicationStore.db.Close()
	if reopened, err := Open(t.Context(), applicationPath, Options{}); err == nil || reopened != nil || !strings.Contains(err.Error(), "application ID") {
		t.Fatalf("foreign application Open = %v, %v", reopened, err)
	}
	contractPath := filepath.Join(t.TempDir(), "contract.db")
	contractStore := openTestStore(t, contractPath)
	if _, err := contractStore.db.Exec(`UPDATE store_meta SET snapshot_contract = 'future'`); err != nil {
		t.Fatal(err)
	}
	_ = contractStore.db.Close()
	if reopened, err := Open(t.Context(), contractPath, Options{}); err == nil || reopened != nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("contract mismatch Open = %v, %v", reopened, err)
	}
	blockedParent := filepath.Join(t.TempDir(), "parent-file")
	if err := os.WriteFile(blockedParent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(t.Context(), filepath.Join(blockedParent, "store.db"), Options{}); err == nil || opened != nil {
		t.Fatal("store opened below a regular file")
	}
}

func TestRecorderExactSequenceCorrelationPoisonAndAmbiguousCommit(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "observer.db"))
	defer store.Close()
	recorder, err := store.Start(t.Context(), testSeed("run-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err = recorder.Publish(t.Context(), testEnvelope(t, "run-1", 1, event.RunStarted, nil)); err != nil {
		t.Fatal(err)
	}
	store.afterCommit = func() error { return errors.New("ambiguous fixture") }
	if err = recorder.Publish(t.Context(), testEnvelope(t, "run-1", 2, event.ModelDelta, map[string]string{"text": "safe"})); err != nil {
		t.Fatalf("read-after proof rejected committed event: %v", err)
	}
	store.afterCommit = nil
	if err = recorder.Publish(t.Context(), testEnvelope(t, "run-1", 4, event.ModelDelta, nil)); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("gap error = %v", err)
	}
	if again := recorder.Publish(t.Context(), testEnvelope(t, "run-1", 3, event.ModelDelta, nil)); again != err {
		t.Fatalf("poison changed: first=%v again=%v", err, again)
	}

	recorder2, err := store.Start(t.Context(), testSeed("run-2"))
	if err != nil {
		t.Fatal(err)
	}
	definition, plan := testToolPlan(t, tool.EffectReadOnly, tool.ReplaySafe)
	started, err := agent.NewToolStartedOccurrence("call-1", "fixture", true, true, &definition, plan, 1)
	if err != nil {
		t.Fatal(err)
	}
	startedData, _ := started.Encode()
	if err = recorder2.Publish(t.Context(), testEnvelope(t, "run-2", 1, event.ToolStarted, json.RawMessage(startedData))); err != nil {
		t.Fatal(err)
	}
	terminal, _ := agent.NewToolTerminalOccurrence(event.ToolCompleted, "call-1", "fixture", "", "")
	terminalData, _ := terminal.Encode()
	if err = recorder2.Publish(t.Context(), testEnvelope(t, "run-2", 2, event.ToolCompleted, json.RawMessage(terminalData))); err != nil {
		t.Fatal(err)
	}
	if err = recorder2.Publish(t.Context(), testEnvelope(t, "run-2", 3, event.ToolCompleted, json.RawMessage(terminalData))); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("duplicate terminal = %v", err)
	}
}

func TestInputValidationAndRecorderFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validation.db")
	if store, err := Open(t.Context(), " ", Options{}); err == nil || store != nil {
		t.Fatal("blank path opened")
	}
	store := openTestStore(t, path)
	for _, seed := range []RunSeed{
		{},
		{RunID: " bad ", SeedDigest: sha256.Sum256([]byte("seed")), PlanFingerprint: "sha256:" + strings.Repeat("a", 64), WorkspaceFingerprint: "sha256:" + strings.Repeat("b", 64), ToolPlanID: "generation"},
		{RunID: "bad", PlanFingerprint: "sha256:" + strings.Repeat("a", 64), WorkspaceFingerprint: "sha256:" + strings.Repeat("b", 64), ToolPlanID: "generation"},
		{RunID: "bad", SeedDigest: sha256.Sum256([]byte("seed")), PlanFingerprint: "bad", WorkspaceFingerprint: "bad", ToolPlanID: "generation"},
		{RunID: "bad", SeedDigest: sha256.Sum256([]byte("seed")), PlanFingerprint: "sha256:" + strings.Repeat("a", 64), WorkspaceFingerprint: "sha256:" + strings.Repeat("b", 64), ToolPlanID: " "},
	} {
		if recorder, err := store.Start(t.Context(), seed); err == nil || recorder != nil {
			t.Fatalf("invalid seed %+v started", seed)
		}
	}
	if err := (*Recorder)(nil).Publish(t.Context(), event.Envelope{}); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("nil recorder error = %v", err)
	}
	recorder, _ := store.Start(t.Context(), testSeed("run-validation"))
	if err := recorder.Publish(t.Context(), testEnvelope(t, "other", 1, event.RunStarted, nil)); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("wrong-run error = %v", err)
	}
	malformed, _ := store.Start(t.Context(), testSeed("run-malformed"))
	if err := malformed.Publish(t.Context(), testEnvelope(t, "run-malformed", 1, event.ToolStarted, json.RawMessage(`{}`))); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("malformed tool start = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if recorder, err := store.Start(t.Context(), testSeed("after-close")); err == nil || recorder != nil {
		t.Fatal("closed store started run")
	}
}

func TestRestoreAcceptsSafeSnapshotAndRefusesDriftCorruptionAndOperations(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "restore.db"))
	defer store.Close()
	snapshot, checkpoint, recorder := fixtureCheckpoint(t, store, "run-safe", 4)
	expected := expectedFrom(snapshot)
	restored, err := store.Restore(t.Context(), checkpoint.ID, expected)
	if err != nil || restored.RunID() != snapshot.RunID() {
		t.Fatalf("Restore() = %q, %v", restored.RunID(), err)
	}
	bad := expected
	bad.ToolPlanID = "other-generation"
	if _, err = store.Restore(t.Context(), checkpoint.ID, bad); !errors.Is(err, ErrUnsafeRecovery) {
		t.Fatalf("plan drift = %v", err)
	}
	if _, err = store.db.Exec(`UPDATE checkpoints SET snapshot_digest = zeroblob(32) WHERE checkpoint_id = ?`, checkpoint.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Restore(t.Context(), checkpoint.ID, expected); !errors.Is(err, ErrUnsafeRecovery) {
		t.Fatalf("digest corruption = %v", err)
	}
	encoded, _ := snapshot.MarshalBinary()
	digest := sha256.Sum256(encoded)
	if _, err = store.db.Exec(`UPDATE checkpoints SET snapshot_digest = ? WHERE checkpoint_id = ?`, digest[:], checkpoint.ID); err != nil {
		t.Fatal(err)
	}

	definition, plan := testToolPlan(t, tool.EffectMutating, tool.ReplayUnsafe)
	// The occurrence must carry checkpoint authority, not the helper's default.
	plan = snapshot.PlanIdentity()
	started, _ := agent.NewToolStartedOccurrence("write-1", "fixture", true, true, &definition, plan, 2)
	data, _ := started.Encode()
	if err = recorder.Publish(t.Context(), testEnvelope(t, snapshot.RunID(), checkpoint.Sequence+1, event.ToolStarted, json.RawMessage(data))); err != nil {
		t.Fatal(err)
	}
	terminal, _ := agent.NewToolTerminalOccurrence(event.ToolCompleted, "write-1", "fixture", "", "")
	terminalData, _ := terminal.Encode()
	if err = recorder.Publish(t.Context(), testEnvelope(t, snapshot.RunID(), checkpoint.Sequence+2, event.ToolCompleted, json.RawMessage(terminalData))); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Restore(t.Context(), checkpoint.ID, expected); !errors.Is(err, ErrUnsafeRecovery) || !strings.Contains(err.Error(), "mutating") {
		t.Fatalf("mutating recovery = %v", err)
	}
}

func TestRestoreAcceptsCorrelatedReadOnlyPostCheckpointAndRejectsInteraction(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "post.db"))
	defer store.Close()
	snapshot, checkpoint, recorder := fixtureCheckpoint(t, store, "run-post", 3)
	definition, _ := testToolPlan(t, tool.EffectReadOnly, tool.ReplaySafe)
	plan := snapshot.PlanIdentity()
	started, _ := agent.NewToolStartedOccurrence("read-1", "fixture", true, true, &definition, plan, 2)
	data, _ := started.Encode()
	if err := recorder.Publish(t.Context(), testEnvelope(t, snapshot.RunID(), 4, event.ToolStarted, json.RawMessage(data))); err != nil {
		t.Fatal(err)
	}
	terminal, _ := agent.NewToolTerminalOccurrence(event.ToolCompleted, "read-1", "fixture", "", "")
	terminalData, _ := terminal.Encode()
	if err := recorder.Publish(t.Context(), testEnvelope(t, snapshot.RunID(), 5, event.ToolCompleted, json.RawMessage(terminalData))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Restore(t.Context(), checkpoint.ID, expectedFrom(snapshot)); err != nil {
		t.Fatalf("safe replay facts rejected: %v", err)
	}
	if err := recorder.Publish(t.Context(), testEnvelope(t, snapshot.RunID(), 6, event.InteractionStarted, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Restore(t.Context(), checkpoint.ID, expectedFrom(snapshot)); !errors.Is(err, ErrUnsafeRecovery) || !strings.Contains(err.Error(), "interaction") {
		t.Fatalf("interaction recovery = %v", err)
	}
}

func TestRestoreFailsClosedAcrossStoredFactCorruption(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *Store, agent.Snapshot, Checkpoint, *Recorder)
	}{
		{"event digest", func(t *testing.T, store *Store, _ agent.Snapshot, checkpoint Checkpoint, _ *Recorder) {
			_, err := store.db.Exec(`UPDATE events SET data_digest = zeroblob(32) WHERE run_id = ? AND sequence = 1`, checkpoint.RunID)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"event gap", func(t *testing.T, store *Store, _ agent.Snapshot, checkpoint Checkpoint, _ *Recorder) {
			_, err := store.db.Exec(`DELETE FROM events WHERE run_id = ? AND sequence = 2`, checkpoint.RunID)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"open precheckpoint tool", func(t *testing.T, store *Store, snapshot agent.Snapshot, checkpoint Checkpoint, _ *Recorder) {
			definition, _ := testToolPlan(t, tool.EffectReadOnly, tool.ReplaySafe)
			identity := snapshot.PlanIdentity()
			_, err := store.db.Exec(`INSERT INTO tool_operations(run_id, call_id, start_sequence, name, declared, executable, effect, replay_safety, definition_fingerprint, plan_fingerprint, workspace_fingerprint, tool_plan_id) VALUES (?, 'open', 2, 'fixture', 1, 1, ?, ?, ?, ?, ?, ?)`, checkpoint.RunID, string(tool.EffectReadOnly), string(tool.ReplaySafe), definition.Fingerprint(), identity.Fingerprint(), identity.WorkspaceFingerprint(), identity.ToolPlanID().String())
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"authority drift", func(t *testing.T, store *Store, snapshot agent.Snapshot, checkpoint Checkpoint, recorder *Recorder) {
			definition, _ := testToolPlan(t, tool.EffectReadOnly, tool.ReplaySafe)
			started, _ := agent.NewToolStartedOccurrence("drift", "fixture", true, true, &definition, snapshot.PlanIdentity(), 2)
			data, _ := started.Encode()
			if err := recorder.Publish(t.Context(), testEnvelope(t, checkpoint.RunID, checkpoint.Sequence+1, event.ToolStarted, data)); err != nil {
				t.Fatal(err)
			}
			_, err := store.db.Exec(`UPDATE tool_operations SET plan_fingerprint = ? WHERE run_id = ? AND call_id = 'drift'`, "sha256:"+strings.Repeat("c", 64), checkpoint.RunID)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"uncertain terminal", func(t *testing.T, store *Store, snapshot agent.Snapshot, checkpoint Checkpoint, recorder *Recorder) {
			definition, _ := testToolPlan(t, tool.EffectReadOnly, tool.ReplaySafe)
			started, _ := agent.NewToolStartedOccurrence("uncertain", "fixture", true, true, &definition, snapshot.PlanIdentity(), 2)
			data, _ := started.Encode()
			if err := recorder.Publish(t.Context(), testEnvelope(t, checkpoint.RunID, checkpoint.Sequence+1, event.ToolStarted, data)); err != nil {
				t.Fatal(err)
			}
			terminal, _ := agent.NewToolTerminalOccurrence(event.ToolFailed, "uncertain", "fixture", tool.ExecutionUncertain, tool.RetryNever)
			terminalData, _ := terminal.Encode()
			if err := recorder.Publish(t.Context(), testEnvelope(t, checkpoint.RunID, checkpoint.Sequence+2, event.ToolFailed, terminalData)); err != nil {
				t.Fatal(err)
			}
		}},
		{"unknown route", func(t *testing.T, _ *Store, snapshot agent.Snapshot, checkpoint Checkpoint, recorder *Recorder) {
			started, _ := agent.NewToolStartedOccurrence("unknown", "missing", false, false, nil, snapshot.PlanIdentity(), 2)
			data, _ := started.Encode()
			if err := recorder.Publish(t.Context(), testEnvelope(t, checkpoint.RunID, checkpoint.Sequence+1, event.ToolStarted, data)); err != nil {
				t.Fatal(err)
			}
		}},
		{"failed terminal without retry facts", func(t *testing.T, _ *Store, snapshot agent.Snapshot, checkpoint Checkpoint, recorder *Recorder) {
			definition, _ := testToolPlan(t, tool.EffectReadOnly, tool.ReplaySafe)
			started, _ := agent.NewToolStartedOccurrence("failed", "fixture", true, true, &definition, snapshot.PlanIdentity(), 2)
			data, _ := started.Encode()
			if err := recorder.Publish(t.Context(), testEnvelope(t, checkpoint.RunID, checkpoint.Sequence+1, event.ToolStarted, data)); err != nil {
				t.Fatal(err)
			}
			terminal, _ := agent.NewToolTerminalOccurrence(event.ToolFailed, "failed", "fixture", "", "")
			terminalData, _ := terminal.Encode()
			if err := recorder.Publish(t.Context(), testEnvelope(t, checkpoint.RunID, checkpoint.Sequence+2, event.ToolFailed, terminalData)); err != nil {
				t.Fatal(err)
			}
		}},
		{"terminal index corruption", func(t *testing.T, store *Store, snapshot agent.Snapshot, checkpoint Checkpoint, recorder *Recorder) {
			definition, _ := testToolPlan(t, tool.EffectReadOnly, tool.ReplaySafe)
			started, _ := agent.NewToolStartedOccurrence("corrupt-terminal", "fixture", true, true, &definition, snapshot.PlanIdentity(), 2)
			data, _ := started.Encode()
			if err := recorder.Publish(t.Context(), testEnvelope(t, checkpoint.RunID, checkpoint.Sequence+1, event.ToolStarted, data)); err != nil {
				t.Fatal(err)
			}
			terminal, _ := agent.NewToolTerminalOccurrence(event.ToolCompleted, "corrupt-terminal", "fixture", "", "")
			terminalData, _ := terminal.Encode()
			if err := recorder.Publish(t.Context(), testEnvelope(t, checkpoint.RunID, checkpoint.Sequence+2, event.ToolCompleted, terminalData)); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(`UPDATE tool_operations SET terminal_kind = 'tool.failed' WHERE run_id = ? AND call_id = 'corrupt-terminal'`, checkpoint.RunID); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t, filepath.Join(t.TempDir(), "facts.db"))
			defer store.Close()
			snapshot, checkpoint, recorder := fixtureCheckpoint(t, store, "run-facts", 3)
			test.mutate(t, store, snapshot, checkpoint, recorder)
			if _, err := store.Restore(t.Context(), checkpoint.ID, expectedFrom(snapshot)); !errors.Is(err, ErrUnsafeRecovery) {
				t.Fatalf("Restore = %v", err)
			}
		})
	}
	store := openTestStore(t, filepath.Join(t.TempDir(), "missing.db"))
	defer store.Close()
	if _, err := store.Restore(t.Context(), "missing", ExpectedPlan{}); err == nil {
		t.Fatal("invalid expected plan succeeded")
	}
	valid := expectedFrom(snapshotFor(t, "unused", 2))
	if _, err := store.Restore(t.Context(), "missing", valid); !errors.Is(err, ErrUnsafeRecovery) {
		t.Fatalf("missing checkpoint = %v", err)
	}
}

func TestBranchCreatesImmutableBoundedLineage(t *testing.T) {
	store := openTestStoreWithOptions(t, filepath.Join(t.TempDir(), "branch.db"), Options{MaxBranches: 1})
	defer store.Close()
	snapshot, checkpoint, _ := fixtureCheckpoint(t, store, "root-run", 2)
	seed := testSeed("branch-1")
	branch, err := store.Branch(t.Context(), checkpoint.ID, seed)
	if err != nil {
		t.Fatal(err)
	}
	if branch.NewRunID != seed.RunID || branch.ParentRunID != snapshot.RunID() || branch.Depth != 1 || branch.Snapshot.RunID() != snapshot.RunID() {
		t.Fatalf("unexpected branch: %+v", branch)
	}
	if _, err = store.Branch(t.Context(), checkpoint.ID, seed); err == nil {
		t.Fatal("duplicate branch identity succeeded")
	}
	branchRecorder := &Recorder{store: store, runID: branch.NewRunID}
	for sequence := uint64(1); sequence <= 2; sequence++ {
		if err = branchRecorder.Publish(t.Context(), testEnvelope(t, branch.NewRunID, sequence, event.ModelDelta, nil)); err != nil {
			t.Fatal(err)
		}
	}
	branchSnapshot := snapshotForPlan(t, branch.NewRunID, 2, snapshot.PlanIdentity())
	branchCheckpoint, err := store.persistCheckpoint(t.Context(), branchSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Branch(t.Context(), branchCheckpoint.ID, testSeed("branch-2")); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("depth overflow = %v", err)
	}
}

func TestRealWALCrashMarkerAcrossProcessKill(t *testing.T) {
	if os.Getenv("SPICE_SQLITE_CRASH_HELPER") == "1" {
		store, err := Open(context.Background(), os.Getenv("SPICE_SQLITE_CRASH_PATH"), Options{})
		if err != nil {
			os.Exit(3)
		}
		defer store.Close()
		fmt.Println("READY")
		_ = os.Stdout.Sync()
		select {}
	}
	path := filepath.Join(t.TempDir(), "crash.db")
	command := exec.Command(os.Args[0], "-test.run=^TestRealWALCrashMarkerAcrossProcessKill$")
	command.Env = append(os.Environ(), "SPICE_SQLITE_CRASH_HELPER=1", "SPICE_SQLITE_CRASH_PATH="+path)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() {
		buffer := make([]byte, 5)
		_, readErr := stdout.Read(buffer)
		if readErr == nil && string(buffer) != "READY" {
			readErr = fmt.Errorf("unexpected helper output %q", buffer)
		}
		ready <- readErr
	}()
	select {
	case err = <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("crash helper did not become ready")
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	store := openTestStore(t, path)
	defer store.Close()
	markers, err := store.Crashes(t.Context())
	if err != nil || len(markers) != 1 {
		t.Fatalf("Crashes() = %+v, %v", markers, err)
	}
}

func TestConcurrentPublishSerializesExactSequence(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "concurrent.db"))
	defer store.Close()
	recorder, _ := store.Start(t.Context(), testSeed("run-concurrent"))
	// Calls are concurrent, but the required contract deliberately rejects any
	// scheduler order that is not the exact next sequence and then poisons.
	var wg sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for sequence := uint64(1); sequence <= 2; sequence++ {
		wg.Add(1)
		go func(value uint64) {
			defer wg.Done()
			errorsSeen <- recorder.Publish(context.Background(), testEnvelope(t, "run-concurrent", value, event.ModelDelta, nil))
		}(sequence)
	}
	wg.Wait()
	close(errorsSeen)
	var failures int
	for err := range errorsSeen {
		if err != nil {
			failures++
		}
	}
	if failures == 0 {
		return
	}
	if !errors.Is(recorder.Publish(t.Context(), testEnvelope(t, "run-concurrent", 3, event.ModelDelta, nil)), ErrPoisoned) {
		t.Fatal("out-of-order concurrent failure did not poison recorder")
	}
}

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	return openTestStoreWithOptions(t, path, Options{})
}

func openTestStoreWithOptions(t *testing.T, path string, options Options) *Store {
	t.Helper()
	store, err := Open(t.Context(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testSeed(runID string) RunSeed {
	digest := sha256.Sum256([]byte("seed:" + runID))
	_, plan := testToolPlanNoTest(tool.EffectReadOnly, tool.ReplaySafe)
	return RunSeed{RunID: runID, SeedDigest: digest, PlanFingerprint: plan.Fingerprint(), WorkspaceFingerprint: plan.WorkspaceFingerprint(), ToolPlanID: plan.ToolPlanID().String()}
}

func testToolPlan(t *testing.T, effect tool.Effect, replay tool.ReplaySafety) (tool.Definition, agent.PlanIdentity) {
	t.Helper()
	definition, plan := testToolPlanNoTest(effect, replay)
	if err := definition.Validate(); err != nil {
		t.Fatal(err)
	}
	return definition, plan
}

func testToolPlanNoTest(effect tool.Effect, replay tool.ReplaySafety) (tool.Definition, agent.PlanIdentity) {
	definition, _ := tool.NewDefinition("fixture", "Fixture tool.", json.RawMessage(`{}`), effect, replay)
	planID, _ := stage.NewPlanID("generation-1")
	workspace := "sha256:" + strings.Repeat("a", 64)
	plan, _ := agent.NewPlanIdentity([]string{"provider:fixture", "stage:fixture"}, "snapshot:fixture-v1", workspace, planID, []tool.Definition{definition})
	return definition, plan
}

func testEnvelope(t *testing.T, runID string, sequence uint64, kind event.Kind, payload any) event.Envelope {
	t.Helper()
	var data json.RawMessage
	if payload != nil {
		if raw, ok := payload.(json.RawMessage); ok {
			data = raw
		} else {
			data, _ = json.Marshal(payload)
		}
	}
	envelope, err := event.Reconstruct(runID, sequence, time.Unix(1700000000+int64(sequence), 0).UTC(), kind, data)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func fixtureCheckpoint(t *testing.T, store *Store, runID string, sequence uint64) (agent.Snapshot, Checkpoint, *Recorder) {
	t.Helper()
	definition, plan := testToolPlan(t, tool.EffectReadOnly, tool.ReplaySafe)
	_ = definition
	seed := testSeed(runID)
	recorder, err := store.Start(t.Context(), seed)
	if err != nil {
		t.Fatal(err)
	}
	for current := uint64(1); current <= sequence; current++ {
		kind := event.ModelDelta
		if current == 1 {
			kind = event.RunStarted
		}
		if err = recorder.Publish(t.Context(), testEnvelope(t, runID, current, kind, nil)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := snapshotForPlan(t, runID, sequence, plan)
	checkpoint, err := store.persistCheckpoint(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, checkpoint, recorder
}

func snapshotFor(t *testing.T, runID string, sequence uint64) agent.Snapshot {
	t.Helper()
	_, plan := testToolPlan(t, tool.EffectReadOnly, tool.ReplaySafe)
	return snapshotForPlan(t, runID, sequence, plan)
}

func snapshotForPlan(t *testing.T, runID string, sequence uint64, plan agent.PlanIdentity) agent.Snapshot {
	t.Helper()
	part, _ := message.Text("recover me")
	history, _ := message.New("message-1", message.RoleUser, part)
	agentDefinition, _ := agent.NewDefinition("fixture-agent", "fixture-model", 3)
	snapshot, err := agent.NewSnapshot(runID, agentDefinition, 1, []message.Message{history}, plan, sequence, agent.LifecycleSuspended)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func expectedFrom(snapshot agent.Snapshot) ExpectedPlan {
	identity := snapshot.PlanIdentity()
	return ExpectedPlan{Fingerprint: identity.Fingerprint(), WorkspaceFingerprint: identity.WorkspaceFingerprint(), ToolPlanID: identity.ToolPlanID().String()}
}

func FuzzRestoreRejectsCorruptedSnapshot(f *testing.F) {
	f.Add([]byte(`{"version":"future"}`))
	f.Fuzz(func(t *testing.T, corrupted []byte) {
		if len(corrupted) > 4096 {
			t.Skip()
		}
		store := openTestStore(t, filepath.Join(t.TempDir(), "fuzz.db"))
		defer store.Close()
		snapshot, checkpoint, _ := fixtureCheckpoint(t, store, "run-fuzz", 2)
		digest := sha256.Sum256(corrupted)
		_, _ = store.db.Exec(`UPDATE checkpoints SET snapshot = ?, snapshot_digest = ? WHERE checkpoint_id = ?`, corrupted, digest[:], checkpoint.ID)
		if _, err := store.Restore(t.Context(), checkpoint.ID, expectedFrom(snapshot)); err == nil {
			t.Fatal("corrupted snapshot restored")
		}
	})
}

func TestSchemaTablesAreStrict(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "strict.db"))
	defer store.Close()
	for _, table := range []string{"store_meta", "writer_epochs", "runs", "events", "checkpoints", "tool_operations", "recovery_decisions"} {
		var sqlText string
		if err := store.db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type = 'table' AND name = ?`, table).Scan(&sqlText); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(sqlText, "STRICT") {
			t.Fatalf("table %s is not STRICT: %s", table, sqlText)
		}
	}
}

func TestClosedDatabaseFailurePaths(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "closed.db"))
	recorder, _ := store.Start(t.Context(), testSeed("run-closed"))
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Publish(t.Context(), testEnvelope(t, "run-closed", 1, event.RunStarted, nil)); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("closed publish = %v", err)
	}
	if _, err := store.Crashes(t.Context()); err == nil {
		t.Fatal("closed crash query succeeded")
	}
	if err := store.Close(); err == nil {
		t.Fatal("close after raw database close succeeded")
	}
	if err := store.verifyConfiguration(t.Context()); err == nil {
		t.Fatal("configuration verification on closed database succeeded")
	}
}

func TestRecoveryBoundaryValidation(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "boundary.db"))
	defer store.Close()
	valid := expectedFrom(snapshotFor(t, "boundary-run", 2))
	if _, err := store.Restore(t.Context(), " ", valid); err == nil {
		t.Fatal("blank checkpoint restored")
	}
	invalid := valid
	invalid.ToolPlanID = ""
	if _, err := store.Restore(t.Context(), "missing", invalid); err == nil {
		t.Fatal("blank generation restored")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.recordDecision(canceled, "checkpoint", "run", false, "test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled decision = %v", err)
	}
	if validSHA256("sha256:"+strings.Repeat("g", 64), true) {
		t.Fatal("non-hexadecimal digest accepted")
	}
}

var _ event.Observer = (*Recorder)(nil)

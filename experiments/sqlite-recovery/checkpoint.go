package sqliterecovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"github.com/spice-framework/spice-agent/agent"
)

// Checkpoint is immutable durable snapshot metadata.
type Checkpoint struct {
	ID       string
	RunID    string
	Sequence uint64
}

// Checkpoint suspends a real run, exports its safe boundary, reserves local
// resume, durably stores the snapshot, then commits resume. Storage failure
// always aborts the reservation and leaves the byte-identical boundary.
func (store *Store) Checkpoint(ctx context.Context, run *agent.Run) (Checkpoint, error) {
	if run == nil {
		return Checkpoint{}, errors.New("sqlite recovery checkpoint run is nil")
	}
	return store.checkpoint(ctx, agentCheckpointSource{run: run})
}

type resumeReservation interface {
	RunID() string
	NextSequence() uint64
	Commit() error
	Abort() error
}

type checkpointSource interface {
	Suspend(context.Context) error
	ExportSnapshot() (agent.Snapshot, error)
	Prepare() (resumeReservation, error)
}

type agentCheckpointSource struct{ run *agent.Run }

func (source agentCheckpointSource) Suspend(ctx context.Context) error {
	return source.run.Suspend(ctx)
}

func (source agentCheckpointSource) ExportSnapshot() (agent.Snapshot, error) {
	return source.run.ExportSnapshot()
}

func (source agentCheckpointSource) Prepare() (resumeReservation, error) {
	return source.run.PrepareLocalResume()
}

func (store *Store) checkpoint(ctx context.Context, run checkpointSource) (Checkpoint, error) {
	if err := run.Suspend(ctx); err != nil {
		return Checkpoint{}, fmt.Errorf("suspend sqlite recovery run: %w", err)
	}
	snapshot, err := run.ExportSnapshot()
	if err != nil {
		return Checkpoint{}, fmt.Errorf("export sqlite recovery snapshot: %w", err)
	}
	prepared, err := run.Prepare()
	if err != nil {
		return Checkpoint{}, fmt.Errorf("prepare sqlite recovery local resume: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = prepared.Abort()
		}
	}()
	if prepared.RunID() != snapshot.RunID() || prepared.NextSequence() != snapshot.LastSequence()+1 {
		return Checkpoint{}, errors.New("sqlite recovery resume reservation does not match snapshot boundary")
	}
	checkpoint, err := store.persistCheckpoint(ctx, snapshot)
	if err != nil {
		return Checkpoint{}, err
	}
	if err = prepared.Commit(); err != nil {
		return Checkpoint{}, fmt.Errorf("commit sqlite recovery local resume: %w", err)
	}
	committed = true
	return checkpoint, nil
}

func (store *Store) persistCheckpoint(ctx context.Context, snapshot agent.Snapshot) (Checkpoint, error) {
	if snapshot.Status() != agent.LifecycleSuspended {
		return Checkpoint{}, errors.New("sqlite recovery checkpoint requires a suspended snapshot")
	}
	encoded, err := snapshot.MarshalBinary()
	if err != nil {
		return Checkpoint{}, fmt.Errorf("encode sqlite recovery snapshot: %w", err)
	}
	digest := sha256.Sum256(encoded)
	id, err := randomID()
	if err != nil {
		return Checkpoint{}, err
	}
	identity := snapshot.PlanIdentity()
	store.mu.Lock()
	defer store.mu.Unlock()
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return Checkpoint{}, fmt.Errorf("begin sqlite recovery checkpoint transaction: %w", err)
	}
	defer tx.Rollback()
	var last uint64
	var plan, workspace, toolPlan string
	if err = tx.QueryRowContext(ctx, `SELECT last_sequence, plan_fingerprint, workspace_fingerprint, tool_plan_id FROM runs WHERE run_id = ?`, snapshot.RunID()).Scan(&last, &plan, &workspace, &toolPlan); err != nil {
		return Checkpoint{}, fmt.Errorf("read sqlite recovery run for checkpoint: %w", err)
	}
	if last != snapshot.LastSequence() || plan != identity.Fingerprint() || workspace != identity.WorkspaceFingerprint() || toolPlan != identity.ToolPlanID().String() {
		return Checkpoint{}, errors.New("sqlite recovery checkpoint authority does not match recorded run")
	}
	if _, err = tx.ExecContext(ctx, `
INSERT INTO checkpoints(checkpoint_id, run_id, sequence, snapshot, snapshot_digest, plan_fingerprint, workspace_fingerprint, tool_plan_id, created_unix_nano)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, snapshot.RunID(), snapshot.LastSequence(), encoded, digest[:], plan, workspace, toolPlan, store.now().UnixNano()); err != nil {
		return Checkpoint{}, fmt.Errorf("insert sqlite recovery checkpoint: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return Checkpoint{}, fmt.Errorf("commit sqlite recovery checkpoint: %w", err)
	}
	return Checkpoint{ID: id, RunID: snapshot.RunID(), Sequence: snapshot.LastSequence()}, nil
}

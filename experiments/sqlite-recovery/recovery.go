package sqliterecovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/tool"
)

const maximumRecoveryEvents = 100_000

// ExpectedPlan is current application authority. Restore never accepts plan,
// workspace, or runtime generation drift.
type ExpectedPlan struct {
	Fingerprint          string
	WorkspaceFingerprint string
	ToolPlanID           string
}

func (expected ExpectedPlan) validate() error {
	if !validSHA256(expected.Fingerprint, true) || !validSHA256(expected.WorkspaceFingerprint, true) {
		return errors.New("sqlite recovery expected plan fingerprints are invalid")
	}
	if expected.ToolPlanID == "" || expected.ToolPlanID != strings.TrimSpace(expected.ToolPlanID) {
		return errors.New("sqlite recovery expected tool plan ID is invalid")
	}
	return nil
}

// Restore validates and returns a suspended snapshot for explicit application
// import. It does not import, resume, restart, or execute anything.
func (store *Store) Restore(ctx context.Context, checkpointID string, expected ExpectedPlan) (agent.Snapshot, error) {
	if checkpointID == "" || checkpointID != strings.TrimSpace(checkpointID) {
		return agent.Snapshot{}, errors.New("sqlite recovery checkpoint ID is invalid")
	}
	if err := expected.validate(); err != nil {
		return agent.Snapshot{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	snapshot, runID, reason, err := store.validateRecovery(ctx, checkpointID, expected)
	if err != nil {
		if runID != "" {
			_ = store.recordDecision(ctx, checkpointID, runID, false, reason)
		}
		return agent.Snapshot{}, errors.Join(ErrUnsafeRecovery, err)
	}
	if err = store.recordDecision(ctx, checkpointID, runID, true, "accepted"); err != nil {
		return agent.Snapshot{}, fmt.Errorf("record sqlite recovery acceptance: %w", err)
	}
	return snapshot, nil
}

func (store *Store) validateRecovery(ctx context.Context, checkpointID string, expected ExpectedPlan) (agent.Snapshot, string, string, error) {
	var encoded, storedDigest []byte
	var runID, plan, workspace, toolPlan string
	var sequence uint64
	err := store.db.QueryRowContext(ctx, `SELECT run_id, sequence, snapshot, snapshot_digest, plan_fingerprint, workspace_fingerprint, tool_plan_id FROM checkpoints WHERE checkpoint_id = ?`, checkpointID).
		Scan(&runID, &sequence, &encoded, &storedDigest, &plan, &workspace, &toolPlan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return agent.Snapshot{}, "", "missing_checkpoint", errors.New("sqlite recovery checkpoint does not exist")
		}
		return agent.Snapshot{}, "", "storage_failure", fmt.Errorf("read sqlite recovery checkpoint: %w", err)
	}
	actualDigest := sha256.Sum256(encoded)
	if !bytes.Equal(storedDigest, actualDigest[:]) {
		return agent.Snapshot{}, runID, "snapshot_digest", errors.New("sqlite recovery snapshot digest does not match")
	}
	snapshot, err := agent.ParseSnapshot(encoded)
	if err != nil {
		return agent.Snapshot{}, runID, "snapshot_contract", fmt.Errorf("parse sqlite recovery snapshot: %w", err)
	}
	identity := snapshot.PlanIdentity()
	if snapshot.Version() != snapshotContract || snapshot.Status() != agent.LifecycleSuspended || snapshot.RunID() != runID || snapshot.LastSequence() != sequence {
		return agent.Snapshot{}, runID, "snapshot_boundary", errors.New("sqlite recovery snapshot boundary does not match checkpoint")
	}
	if len(snapshot.InteractionIDs()) != 0 {
		return agent.Snapshot{}, runID, "interaction_state", errors.New("sqlite recovery snapshot contains interaction identities")
	}
	if plan != expected.Fingerprint || workspace != expected.WorkspaceFingerprint || toolPlan != expected.ToolPlanID ||
		identity.Fingerprint() != expected.Fingerprint || identity.WorkspaceFingerprint() != expected.WorkspaceFingerprint || identity.ToolPlanID().String() != expected.ToolPlanID {
		return agent.Snapshot{}, runID, "plan_mismatch", errors.New("sqlite recovery plan, workspace, or generation does not match")
	}
	if err = store.validateEvents(ctx, runID, sequence); err != nil {
		return agent.Snapshot{}, runID, "event_integrity", err
	}
	if err = store.validateOperations(ctx, runID, sequence, expected); err != nil {
		return agent.Snapshot{}, runID, "operation_safety", err
	}
	return snapshot, runID, "accepted", nil
}

func (store *Store) validateEvents(ctx context.Context, runID string, checkpointSequence uint64) error {
	rows, err := store.db.QueryContext(ctx, `SELECT sequence, kind, data, data_digest FROM events WHERE run_id = ? ORDER BY sequence`, runID)
	if err != nil {
		return fmt.Errorf("query sqlite recovery events: %w", err)
	}
	defer rows.Close()
	var expected uint64 = 1
	for rows.Next() {
		if expected > maximumRecoveryEvents {
			return fmt.Errorf("sqlite recovery event count exceeds %d", maximumRecoveryEvents)
		}
		var sequence uint64
		var kind string
		var data, digest []byte
		if err = rows.Scan(&sequence, &kind, &data, &digest); err != nil {
			return fmt.Errorf("scan sqlite recovery event: %w", err)
		}
		if sequence != expected {
			return errors.New("sqlite recovery event sequence contains a gap or duplicate")
		}
		actual := sha256.Sum256(data)
		if !bytes.Equal(digest, actual[:]) {
			return errors.New("sqlite recovery event digest does not match")
		}
		if sequence > checkpointSequence && isInteractionKind(event.Kind(kind)) {
			return errors.New("sqlite recovery post-checkpoint interaction cannot be replayed")
		}
		expected++
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate sqlite recovery events: %w", err)
	}
	if checkpointSequence >= expected {
		return errors.New("sqlite recovery checkpoint sequence is not fully recorded")
	}
	return nil
}

func (store *Store) validateOperations(ctx context.Context, runID string, checkpointSequence uint64, expected ExpectedPlan) error {
	rows, err := store.db.QueryContext(ctx, `
SELECT operation.call_id, operation.name, operation.start_sequence, operation.terminal_sequence,
 operation.declared, operation.executable, operation.effect, operation.replay_safety,
 operation.definition_fingerprint, operation.plan_fingerprint, operation.workspace_fingerprint,
 operation.tool_plan_id, operation.terminal_kind, operation.execution_state, operation.retry_disposition,
 started.data, terminal.kind, terminal.data
FROM tool_operations AS operation
JOIN events AS started ON started.run_id = operation.run_id AND started.sequence = operation.start_sequence
LEFT JOIN events AS terminal ON terminal.run_id = operation.run_id AND terminal.sequence = operation.terminal_sequence
WHERE operation.run_id = ? ORDER BY operation.start_sequence`, runID)
	if err != nil {
		return fmt.Errorf("query sqlite recovery operations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var callID, name string
		var start uint64
		var terminal sql.NullInt64
		var declared, executable int
		var effect, replay, definitionFingerprint, plan, workspace, toolPlan string
		var terminalKind, executionState, retry, terminalEventKind sql.NullString
		var startedData, terminalData []byte
		if err = rows.Scan(
			&callID, &name, &start, &terminal, &declared, &executable, &effect, &replay,
			&definitionFingerprint, &plan, &workspace, &toolPlan, &terminalKind, &executionState,
			&retry, &startedData, &terminalEventKind, &terminalData,
		); err != nil {
			return fmt.Errorf("scan sqlite recovery operation: %w", err)
		}
		started, decodeErr := agent.DecodeToolStartedOccurrence(startedData)
		if decodeErr != nil || string(started.CallID()) != callID || started.Name() != name ||
			boolInt(started.Declared()) != declared || boolInt(started.Executable()) != executable ||
			string(started.Effect()) != effect || string(started.ReplaySafety()) != replay ||
			started.DefinitionFingerprint() != definitionFingerprint || started.PlanFingerprint() != plan ||
			started.WorkspaceFingerprint() != workspace || started.ToolPlanID().String() != toolPlan {
			return errors.New("sqlite recovery tool start index does not match its typed occurrence")
		}
		if terminal.Valid {
			terminalOccurrence, terminalErr := agent.DecodeToolTerminalOccurrence(event.Kind(terminalEventKind.String), terminalData)
			if terminalErr != nil || string(terminalOccurrence.CallID()) != callID || terminalOccurrence.Name() != name ||
				string(terminalOccurrence.Kind()) != terminalKind.String ||
				string(terminalOccurrence.ExecutionState()) != executionState.String ||
				string(terminalOccurrence.RetryDisposition()) != retry.String {
				return errors.New("sqlite recovery tool terminal index does not match its typed occurrence")
			}
		}
		if start <= checkpointSequence {
			if !terminal.Valid || uint64(terminal.Int64) > checkpointSequence {
				return errors.New("sqlite recovery checkpoint contains an open tool operation")
			}
			continue
		}
		if declared != 1 || executable != 1 {
			return errors.New("sqlite recovery post-checkpoint route is unknown or non-executable")
		}
		if plan != expected.Fingerprint || workspace != expected.WorkspaceFingerprint || toolPlan != expected.ToolPlanID {
			return errors.New("sqlite recovery post-checkpoint operation authority does not match")
		}
		if effect != string(tool.EffectReadOnly) || replay != string(tool.ReplaySafe) {
			return errors.New("sqlite recovery post-checkpoint mutating or replay-unsafe operation is forbidden")
		}
		if !terminal.Valid {
			return errors.New("sqlite recovery post-checkpoint operation is open")
		}
		if executionState.String == string(tool.ExecutionUncertain) ||
			(terminalKind.String == string(event.ToolFailed) && (executionState.String == "" || retry.String == "")) {
			return errors.New("sqlite recovery post-checkpoint retry outcome is uncertain")
		}
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate sqlite recovery operations: %w", err)
	}
	return nil
}

func isInteractionKind(kind event.Kind) bool {
	switch kind {
	case event.InteractionStarted, event.InteractionCompleted, event.InteractionFailed, event.InteractionCancelled:
		return true
	default:
		return false
	}
}

func (store *Store) recordDecision(ctx context.Context, checkpointID, runID string, accepted bool, reason string) error {
	id, err := randomID()
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO recovery_decisions(decision_id, checkpoint_id, run_id, accepted, reason_code, decided_unix_nano) VALUES (?, ?, ?, ?, ?, ?)`, id, checkpointID, runID, boolInt(accepted), reason, store.now().UnixNano())
	return err
}

func validSHA256(value string, prefixed bool) bool {
	if prefixed {
		if !strings.HasPrefix(value, "sha256:") {
			return false
		}
		value = strings.TrimPrefix(value, "sha256:")
	}
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// Branch is immutable experiment-owned lineage. Snapshot remains tied to its
// original run; NewRunID is authority for an application-created future run.
type Branch struct {
	NewRunID           string
	ParentRunID        string
	ParentCheckpointID string
	Depth              int
	Snapshot           agent.Snapshot
}

// Branch validates recovery and reserves an immutable new run identity without
// mutating the snapshot or starting execution.
func (store *Store) Branch(ctx context.Context, checkpointID string, seed RunSeed) (Branch, error) {
	if err := seed.validate(); err != nil {
		return Branch{}, err
	}
	snapshot, err := store.Restore(ctx, checkpointID, ExpectedPlan{
		Fingerprint: seed.PlanFingerprint, WorkspaceFingerprint: seed.WorkspaceFingerprint, ToolPlanID: seed.ToolPlanID,
	})
	if err != nil {
		return Branch{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	var parentDepth int
	if err = store.db.QueryRowContext(ctx, `SELECT branch_depth FROM runs WHERE run_id = ?`, snapshot.RunID()).Scan(&parentDepth); err != nil {
		return Branch{}, fmt.Errorf("read sqlite recovery branch lineage: %w", err)
	}
	depth := parentDepth + 1
	if depth > store.depth {
		return Branch{}, fmt.Errorf("sqlite recovery branch depth exceeds %d", store.depth)
	}
	_, err = store.db.ExecContext(ctx, `
INSERT INTO runs(run_id, seed_digest, plan_fingerprint, workspace_fingerprint, tool_plan_id, parent_run_id, parent_checkpoint_id, branch_depth, started_unix_nano)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, seed.RunID, seed.SeedDigest[:], seed.PlanFingerprint, seed.WorkspaceFingerprint, seed.ToolPlanID,
		snapshot.RunID(), checkpointID, depth, store.now().UnixNano())
	if err != nil {
		return Branch{}, fmt.Errorf("insert sqlite recovery branch: %w", err)
	}
	return Branch{NewRunID: seed.RunID, ParentRunID: snapshot.RunID(), ParentCheckpointID: checkpointID, Depth: depth, Snapshot: snapshot}, nil
}

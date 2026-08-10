package sqliterecovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
)

const (
	snapshotContract     = agent.SnapshotVersion
	toolStartedContract  = agent.ToolStartedOccurrenceVersion
	toolTerminalContract = agent.ToolTerminalOccurrenceVersion
)

// RunSeed binds persistence to application-selected run and execution-plan
// identities. SeedDigest is caller-owned non-secret deterministic seed data.
type RunSeed struct {
	RunID                string
	SeedDigest           [sha256.Size]byte
	PlanFingerprint      string
	WorkspaceFingerprint string
	ToolPlanID           string
}

func (seed RunSeed) validate() error {
	if seed.RunID == "" || seed.RunID != strings.TrimSpace(seed.RunID) {
		return errors.New("sqlite recovery run ID must be non-empty without surrounding whitespace")
	}
	if seed.SeedDigest == ([sha256.Size]byte{}) {
		return errors.New("sqlite recovery seed digest must not be zero")
	}
	if !validSHA256(seed.PlanFingerprint, true) || !validSHA256(seed.WorkspaceFingerprint, true) {
		return errors.New("sqlite recovery plan and workspace fingerprints must be lowercase prefixed SHA-256")
	}
	if seed.ToolPlanID == "" || seed.ToolPlanID != strings.TrimSpace(seed.ToolPlanID) {
		return errors.New("sqlite recovery tool plan ID must be non-empty without surrounding whitespace")
	}
	return nil
}

// Start registers immutable seed authority before an Engine starts the run and
// returns its required observer. The application must arrange for its IDSource
// to issue the same RunID.
func (store *Store) Start(ctx context.Context, seed RunSeed) (*Recorder, error) {
	if err := seed.validate(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return nil, errors.New("sqlite recovery store is closed")
	}
	_, err := store.db.ExecContext(ctx, `
INSERT INTO runs(run_id, seed_digest, plan_fingerprint, workspace_fingerprint, tool_plan_id, started_unix_nano)
VALUES (?, ?, ?, ?, ?, ?)`, seed.RunID, seed.SeedDigest[:], seed.PlanFingerprint,
		seed.WorkspaceFingerprint, seed.ToolPlanID, store.now().UnixNano())
	if err != nil {
		return nil, fmt.Errorf("start sqlite recovery run: %w", err)
	}
	return &Recorder{store: store, runID: seed.RunID}, nil
}

// Recorder is a required observer. It acknowledges only after event bytes and
// typed tool correlation commit together. Its first failure permanently
// poisons the instance so a caller cannot continue on uncertain durability.
type Recorder struct {
	store  *Store
	runID  string
	mu     sync.Mutex
	poison error
}

// Publish implements event.Observer.
func (recorder *Recorder) Publish(ctx context.Context, envelope event.Envelope) error {
	if recorder == nil || recorder.store == nil {
		return errors.Join(ErrPoisoned, errors.New("sqlite recovery recorder is nil"))
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.poison != nil {
		return recorder.poison
	}
	if envelope.RunID() != recorder.runID {
		return recorder.fail(errors.New("sqlite recovery event run ID does not match recorder"))
	}
	if _, err := event.Reconstruct(envelope.RunID(), envelope.Sequence(), envelope.At(), envelope.Kind(), envelope.Data()); err != nil {
		return recorder.fail(fmt.Errorf("validate sqlite recovery event: %w", err))
	}
	if err := recorder.persist(ctx, envelope); err != nil {
		return recorder.fail(err)
	}
	return nil
}

func (recorder *Recorder) fail(cause error) error {
	if recorder.poison == nil {
		recorder.poison = errors.Join(ErrPoisoned, cause)
	}
	return recorder.poison
}

func (recorder *Recorder) persist(ctx context.Context, envelope event.Envelope) error {
	store := recorder.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return errors.New("sqlite recovery store is closed")
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin sqlite recovery event transaction: %w", err)
	}
	defer tx.Rollback()
	var previous uint64
	if err = tx.QueryRowContext(ctx, `SELECT last_sequence FROM runs WHERE run_id = ?`, recorder.runID).Scan(&previous); err != nil {
		return fmt.Errorf("read sqlite recovery sequence: %w", err)
	}
	if envelope.Sequence() != previous+1 {
		return fmt.Errorf("sqlite recovery sequence %d is not exact successor of %d", envelope.Sequence(), previous)
	}
	data := envelope.Data()
	if data == nil {
		data = []byte{}
	}
	digest := sha256.Sum256(data)
	if _, err = tx.ExecContext(ctx, `INSERT INTO events(run_id, sequence, occurred_unix_nano, kind, data, data_digest) VALUES (?, ?, ?, ?, ?, ?)`,
		recorder.runID, envelope.Sequence(), envelope.At().UnixNano(), string(envelope.Kind()), data, digest[:]); err != nil {
		return fmt.Errorf("insert sqlite recovery event: %w", err)
	}
	if err = persistOccurrence(ctx, tx, envelope); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runs SET last_sequence = ? WHERE run_id = ? AND last_sequence = ?`, envelope.Sequence(), recorder.runID, previous); err != nil {
		return fmt.Errorf("advance sqlite recovery sequence: %w", err)
	}
	commitErr := tx.Commit()
	if commitErr == nil && store.afterCommit != nil {
		commitErr = store.afterCommit()
	}
	if commitErr == nil {
		return nil
	}
	// Commit errors are ambiguous. Acknowledgement is permitted only when a
	// fresh read proves the exact immutable row and digest is durable.
	var storedData, storedDigest []byte
	var kind string
	readErr := store.db.QueryRowContext(context.WithoutCancel(ctx), `SELECT kind, data, data_digest FROM events WHERE run_id = ? AND sequence = ?`, recorder.runID, envelope.Sequence()).Scan(&kind, &storedData, &storedDigest)
	if readErr == nil && kind == string(envelope.Kind()) && bytes.Equal(storedData, data) && bytes.Equal(storedDigest, digest[:]) {
		return nil
	}
	return fmt.Errorf("commit sqlite recovery event: %w", commitErr)
}

func persistOccurrence(ctx context.Context, tx *sql.Tx, envelope event.Envelope) error {
	switch envelope.Kind() {
	case event.ToolStarted:
		occurrence, err := agent.DecodeToolStartedOccurrence(envelope.Data())
		if err != nil {
			return fmt.Errorf("decode sqlite recovery tool start: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO tool_operations(run_id, call_id, start_sequence, name, declared, executable, effect, replay_safety,
 definition_fingerprint, plan_fingerprint, workspace_fingerprint, tool_plan_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, envelope.RunID(), string(occurrence.CallID()), envelope.Sequence(), occurrence.Name(),
			boolInt(occurrence.Declared()), boolInt(occurrence.Executable()), string(occurrence.Effect()), string(occurrence.ReplaySafety()),
			occurrence.DefinitionFingerprint(), occurrence.PlanFingerprint(), occurrence.WorkspaceFingerprint(), occurrence.ToolPlanID().String())
		if err != nil {
			return fmt.Errorf("insert sqlite recovery tool start: %w", err)
		}
	case event.ToolCompleted, event.ToolFailed:
		occurrence, err := agent.DecodeToolTerminalOccurrence(envelope.Kind(), envelope.Data())
		if err != nil {
			return fmt.Errorf("decode sqlite recovery tool terminal: %w", err)
		}
		result, err := tx.ExecContext(ctx, `
UPDATE tool_operations SET terminal_sequence = ?, terminal_kind = ?, execution_state = ?, retry_disposition = ?
WHERE run_id = ? AND call_id = ? AND name = ? AND terminal_sequence IS NULL`, envelope.Sequence(), string(envelope.Kind()),
			string(occurrence.ExecutionState()), string(occurrence.RetryDisposition()), envelope.RunID(), string(occurrence.CallID()), occurrence.Name())
		if err != nil {
			return fmt.Errorf("correlate sqlite recovery tool terminal: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return errors.New("sqlite recovery tool terminal has no exact open start")
		}
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

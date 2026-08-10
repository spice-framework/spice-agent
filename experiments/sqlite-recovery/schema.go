package sqliterecovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const schemaV1 = `
CREATE TABLE IF NOT EXISTS store_meta (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  snapshot_contract TEXT NOT NULL,
  tool_started_contract TEXT NOT NULL,
  tool_terminal_contract TEXT NOT NULL,
  created_unix_nano INTEGER NOT NULL
) STRICT;
CREATE TABLE IF NOT EXISTS writer_epochs (
  epoch_id TEXT PRIMARY KEY,
  opened_unix_nano INTEGER NOT NULL,
  closed_unix_nano INTEGER
) STRICT;
CREATE TABLE IF NOT EXISTS runs (
  run_id TEXT PRIMARY KEY,
  seed_digest BLOB NOT NULL CHECK (length(seed_digest) = 32),
  plan_fingerprint TEXT NOT NULL,
  workspace_fingerprint TEXT NOT NULL,
  tool_plan_id TEXT NOT NULL,
  parent_run_id TEXT,
  parent_checkpoint_id TEXT,
  branch_depth INTEGER NOT NULL DEFAULT 0 CHECK (branch_depth >= 0),
  last_sequence INTEGER NOT NULL DEFAULT 0 CHECK (last_sequence >= 0),
  started_unix_nano INTEGER NOT NULL,
  FOREIGN KEY(parent_run_id) REFERENCES runs(run_id)
) STRICT;
CREATE TABLE IF NOT EXISTS events (
  run_id TEXT NOT NULL,
  sequence INTEGER NOT NULL CHECK (sequence > 0),
  occurred_unix_nano INTEGER NOT NULL,
  kind TEXT NOT NULL,
  data BLOB NOT NULL,
  data_digest BLOB NOT NULL CHECK (length(data_digest) = 32),
  PRIMARY KEY(run_id, sequence),
  FOREIGN KEY(run_id) REFERENCES runs(run_id)
) STRICT;
CREATE TABLE IF NOT EXISTS checkpoints (
  checkpoint_id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  sequence INTEGER NOT NULL CHECK (sequence > 0),
  snapshot BLOB NOT NULL,
  snapshot_digest BLOB NOT NULL CHECK (length(snapshot_digest) = 32),
  plan_fingerprint TEXT NOT NULL,
  workspace_fingerprint TEXT NOT NULL,
  tool_plan_id TEXT NOT NULL,
  created_unix_nano INTEGER NOT NULL,
  UNIQUE(run_id, sequence),
  FOREIGN KEY(run_id) REFERENCES runs(run_id)
) STRICT;
CREATE TABLE IF NOT EXISTS tool_operations (
  run_id TEXT NOT NULL,
  call_id TEXT NOT NULL,
  start_sequence INTEGER NOT NULL,
  terminal_sequence INTEGER,
  name TEXT NOT NULL,
  declared INTEGER NOT NULL CHECK (declared IN (0, 1)),
  executable INTEGER NOT NULL CHECK (executable IN (0, 1)),
  effect TEXT NOT NULL,
  replay_safety TEXT NOT NULL,
  definition_fingerprint TEXT NOT NULL,
  plan_fingerprint TEXT NOT NULL,
  workspace_fingerprint TEXT NOT NULL,
  tool_plan_id TEXT NOT NULL,
  terminal_kind TEXT,
  execution_state TEXT,
  retry_disposition TEXT,
  PRIMARY KEY(run_id, call_id),
  UNIQUE(run_id, start_sequence),
  UNIQUE(run_id, terminal_sequence),
  FOREIGN KEY(run_id, start_sequence) REFERENCES events(run_id, sequence),
  FOREIGN KEY(run_id, terminal_sequence) REFERENCES events(run_id, sequence)
) STRICT;
CREATE TABLE IF NOT EXISTS recovery_decisions (
  decision_id TEXT PRIMARY KEY,
  checkpoint_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  accepted INTEGER NOT NULL CHECK (accepted IN (0, 1)),
  reason_code TEXT NOT NULL,
  decided_unix_nano INTEGER NOT NULL,
  FOREIGN KEY(checkpoint_id) REFERENCES checkpoints(checkpoint_id),
  FOREIGN KEY(run_id) REFERENCES runs(run_id)
) STRICT;
`

func (store *Store) initialize(ctx context.Context, busy time.Duration) error {
	pragmas := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = FULL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA trusted_schema = OFF`,
		fmt.Sprintf(`PRAGMA busy_timeout = %d`, busy.Milliseconds()),
	}
	for _, statement := range pragmas {
		if _, err := store.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite recovery store: %w", err)
		}
	}
	var currentApplication, currentVersion int
	if err := store.db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&currentApplication); err != nil {
		return fmt.Errorf("read sqlite recovery application ID: %w", err)
	}
	if err := store.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&currentVersion); err != nil {
		return fmt.Errorf("read sqlite recovery schema version: %w", err)
	}
	if currentApplication != 0 && currentApplication != applicationID {
		return errors.New("sqlite recovery application ID does not match")
	}
	if currentVersion > schemaVersion {
		return fmt.Errorf("sqlite recovery schema version %d is newer than supported version %d", currentVersion, schemaVersion)
	}
	if currentVersion != 0 && currentVersion < schemaVersion {
		return fmt.Errorf("sqlite recovery schema version %d requires an explicit migration", currentVersion)
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin sqlite recovery schema transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, schemaV1); err != nil {
		return fmt.Errorf("create sqlite recovery schema: %w", err)
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA application_id = %d`, applicationID)); err != nil {
		return fmt.Errorf("set sqlite recovery application ID: %w", err)
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return fmt.Errorf("set sqlite recovery schema version: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO store_meta(singleton, snapshot_contract, tool_started_contract, tool_terminal_contract, created_unix_nano) VALUES (1, ?, ?, ?, ?)`, snapshotContract, toolStartedContract, toolTerminalContract, store.now().UnixNano()); err != nil {
		return fmt.Errorf("write sqlite recovery metadata: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite recovery schema: %w", err)
	}
	return store.verifyConfiguration(ctx)
}

func (store *Store) verifyConfiguration(ctx context.Context) error {
	expectations := []struct {
		query string
		want  string
	}{
		{`PRAGMA journal_mode`, "wal"},
		{`PRAGMA synchronous`, "2"},
		{`PRAGMA foreign_keys`, "1"},
		{`PRAGMA trusted_schema`, "0"},
	}
	for _, expectation := range expectations {
		var got string
		if err := store.db.QueryRowContext(ctx, expectation.query).Scan(&got); err != nil {
			return fmt.Errorf("verify sqlite recovery configuration: %w", err)
		}
		if got != expectation.want {
			return fmt.Errorf("sqlite recovery configuration %q is %q, want %q", expectation.query, got, expectation.want)
		}
	}
	var snapshot, started, terminal string
	if err := store.db.QueryRowContext(ctx, `SELECT snapshot_contract, tool_started_contract, tool_terminal_contract FROM store_meta WHERE singleton = 1`).Scan(&snapshot, &started, &terminal); err != nil {
		return fmt.Errorf("read sqlite recovery contract metadata: %w", err)
	}
	if snapshot != snapshotContract || started != toolStartedContract || terminal != toolTerminalContract {
		return errors.New("sqlite recovery contract metadata is incompatible")
	}
	return nil
}

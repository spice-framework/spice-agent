# Phase 7 SQLite recovery experiment evidence

## Boundary

The `experiments/sqlite-recovery` nested module consumes only released public
contracts from `spice-agent v0.1.0-preview.5`, pins
`github.com/ncruces/go-sqlite3 v0.35.3`, contains no `replace`, and leaves root
product dependencies and releases unchanged. Its generated
`SQLiteRecoveryProof` application proves typed construction without a registry.

It is embedded recovery only. No daemon discovery, process restart, automatic
snapshot import, hidden retry, or execution occurs during recovery.

## Proven invariants

- schema v1 is STRICT with application/user version tags, WAL/FULL durability,
  foreign keys, trusted schema off, one connection, and bounded busy waits;
- required-observer acknowledgment follows exact event and decoded occurrence
  commit; first failure poisons and an ambiguous commit needs exact read proof;
- checkpoint ordering is Suspend, ExportSnapshot, PrepareLocalResume, durable
  commit, then Commit, with Abort on storage failure;
- restore locks snapshot v1alpha3 and tool occurrence v1alpha1 contracts and
  rejects digest, sequence, correlation, plan, workspace, generation,
  interaction, mutation, open-operation, and uncertain-retry hazards;
- branches have immutable parent/checkpoint lineage, new run identities, and a
  hard depth bound;
- a real cross-platform child process is killed against a WAL database and the
  next writer observes the unclosed epoch crash marker;
- fuzz, cancellation, concurrency, real Agent lifecycle, generated composition,
  offline vendor, race, coverage, and provisional benchmark paths are owned by
  the experiment.

## Verification

The nested `make fast`, `make check`, `make verify`, and `make benchmark` are
also wired into the root quality/bootstrap gate. Exact green command and commit
evidence is recorded after the commit tree passes. Initial Windows baseline and
provisional, non-gating budgets live in the experiment benchmark guide.

## Promotion and deletion

This proof does not stabilize a recorder, database schema, or recovery API.
Promotion requires independent adoption and contract/security review. Deletion
removes the nested directory plus these ledger links; no core package, protocol,
root dependency graph, or release artifact changes.

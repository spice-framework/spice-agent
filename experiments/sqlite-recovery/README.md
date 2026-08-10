# SQLite recovery experiment

This deletable Phase 7 nested module proves that released Spice Agent snapshot
and typed tool-occurrence contracts are sufficient for conservative embedded
recovery. It is not daemon persistence: it never starts, imports, resumes, or
retries a run. The application explicitly chooses what to do with a validated
suspended snapshot.

The module pins `spice-agent v0.1.0-preview.5` and
`github.com/ncruces/go-sqlite3 v0.35.3`, has no `replace`, commits vendor, and
supports offline verification. The driver is pure Go/wasm2go, BSD-3-Clause,
actively maintained, context-aware through `database/sql`, and avoids a C
toolchain. Its vendor cost is accepted only inside this experiment.

## Durable contract

- Schema v1 is `STRICT`, tagged with application ID `0x53504147` and
  `user_version=1`. Startup requires WAL, `synchronous=FULL`, foreign keys on,
  trusted schema off, one connection, and a bounded busy timeout.
- `store_meta`, `writer_epochs`, `runs`, `events`, `checkpoints`,
  `tool_operations`, and `recovery_decisions` have explicit ownership.
- `Start` binds an application-selected run ID and non-secret seed digest to an
  exact plan, workspace, and tool generation. Its `Recorder` is a required
  observer: it commits exact sequence bytes and decoded tool correlation before
  returning nil. First failure poisons the recorder. An ambiguous commit is
  acknowledged only after an independent exact row/digest read proves it.
- `Checkpoint` orders `Suspend` → `ExportSnapshot` → `PrepareLocalResume` →
  durable commit → `Commit`. Storage failure calls `Abort` and preserves the
  suspended boundary.
- `Restore` rejects missing or future contracts, digest/sequence/correlation
  errors, plan/workspace/generation drift, interactions, open operations,
  mutating operations, unsafe replay declarations, and uncertain retry facts.
  Only correlated, declared, executable, read-only, replay-safe facts after a
  checkpoint are accepted.
- `Branch` records immutable experiment-owned lineage with a new run ID and a
  bounded depth. It never rewrites the source snapshot's run identity.
- Unclosed writer epochs are crash markers. They report only random epoch IDs
  and times, never paths, prompts, arguments, tool output, or credentials.

The generated `SQLiteRecoveryProof` application injects `Options` into an
ordinary typed `StoreFactory` and then into `Proof`. It contains no registry,
reflection, package scan, or second composition graph.

## Verification and status

With Go 1.26.5:

```text
make fast
make check
make verify
make benchmark
```

Commands force `GOWORK=off`, vendor mode, and `GOPROXY=off`. Tests cover real
WAL files, process termination on Windows/Unix, strict schema and future-version
refusal, digest and occurrence corruption, cancellation, concurrency, branch
limits, fuzzed snapshots, generated construction, and a real Agent checkpoint.
Coverage starts above the repository's 85% handwritten-product floor.

This API is explicitly experimental. Passing conformance does not stabilize it.
See `docs/dependencies.md`, `docs/threat-model.md`, and `benchmarks/README.md`.

## Deletion

Delete this directory and remove only its evidence/status links from the root
Phase 7 ledger and snapshot RFC. The parent module imports none of it; deletion
changes no Agent package, protocol, release metadata, or root dependency graph.

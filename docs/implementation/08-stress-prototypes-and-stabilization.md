# Phases 7–8: Contract Stress and SDK Stabilization

## Objective and prerequisites

Attempt to break the preview contracts with isolated extensions before calling
them stable. This work begins after the architecture-proof preview is published
and preserves the rule that experimental concepts stay outside the kernel until
multiple implementations demonstrate a generic need.

## Phase 7 stress prototypes

### Permission guard

Build an optional `stage.ToolDispatchGuard` that observes and can deny every
compiled and runtime tool execution through RFC 0009. The generic immutable
scope, exact-plan binding, generated collection injection, and terminal guard
ordering are implemented in core; no external policy is installed by default.
The scope now also exposes the run-owned lifecycle requester required for a
policy prompt without exposing mutable broker scope. Its private pointer
capability, engine binding, terminal ordering, cancellation join, and snapshot
refusal are core invariants rather than behavior each policy reimplements.
The prototype must prove there is no bypass around canonical dispatch,
including retries, interactions, and plugin generations. Capability metadata
informs policy but does not become enforcement by itself.

Status: **implemented as an isolated experiment**. The nested
`experiments/permission` module pins the public preview5 core without a
`replace`, commits generated Spice construction and vendor contents, and owns
its compatibility manifest, conformance tests, provisional benchmarks, and
deletion path. It remains experimental and does not stabilize a core policy API.
See [the permission evidence](evidence/phase7-permission-experiment.md).

### SQLite recorder/restorer

Build an observer/snapshot extension that records events durably, marks crashes,
restores safe snapshots, branches history, and refuses to replay uncertain
mutating operations. It must exercise required-observer backpressure and
durability failure without changing kernel storage policy. Core now supplies
strict versioned secret-free start and terminal occurrences, so the recorder can
distinguish a correlated close from a crash-interrupted open operation without
parsing private maps or persisting free-form tool errors.

Status: **implemented as an isolated experiment**. The nested
`experiments/sqlite-recovery` module pins preview5 and ncruces SQLite without a
`replace`, commits a STRICT WAL schema, vendor, generated Spice construction,
compatibility locks, real process-crash/WAL conformance, conservative recovery,
branch lineage, fuzz/race coverage, and provisional benchmarks. It returns safe
snapshots for explicit embedded application handling and deliberately does not
implement transparent daemon restart. See
[the SQLite recovery evidence](evidence/phase7-sqlite-recovery-experiment.md).

### Alternate client and rich companion

Implement a second shell using the same UI-neutral client/session values and
portable semantic views. It must not import Bubble Tea presentation or require
plugin-provided executable UI.

Status: **implemented as an isolated TUI-repository experiment**. Commit
`0bacac3d5a2541abfde41fd9686b763f622f84c0` adds a standard-library semantic
shell pinned to the released TUI module. It consumes only UI-neutral Session
values, emits deterministic portable JSONL, imports no Bubble Tea, and remains
deletable without a production client or compatibility freeze.

### Two-worker distributed extension

Implement two cooperating workers entirely as an extension using ordinary runs,
messages, tools, and events. Success means the kernel needs no parent/child,
subagent, swarm, or scheduler concepts. If verification across multiple
annotation occurrences is truly required, propose a generic Spice aggregate-
analysis RFC with non-agent use cases.

Status: **implemented as an isolated core-repository experiment**. The nested
`experiments/two-worker` module pins preview5 without a replacement. Its
ordinary `worker.delegate` tool receives a public `client.Session`, delegates
to a second ordinary `daemon.RunHost` over authenticated current-user local
IPC, and proves retry identity, distributed cancellation, process cleanup,
generated Spice injection, race/coverage, and provisional benchmarks. It adds
no parent/child, subagent, scheduler, registry, or protocol concept. See
[the two-worker evidence](evidence/phase7-two-worker-experiment.md).

Each prototype has its own experimental module, compatibility manifest,
conformance tests, benchmarks, and deletion path. It becomes a public repository
only after its contract passes and a dependency/security review is approved.

## Phase 8 ecosystem breadth

Status: **in progress**. The first bounded compatibility slice commits an exact
machine-readable engine-protocol matrix and proves source-built previous (1.2)
and current (1.3) semantics through real authenticated local processes on Linux
and Windows. It deliberately makes no released-binary N/N-1 claim. Plugin
generation fixtures remain the next separate slice. See
[the engine compatibility evidence](evidence/phase8-engine-protocol-compatibility.md).

- Obtain three independently authored compiled extensions using only public
  annotation/SDK contracts.
- Run both runtime-plugin languages and a second client against current and
  previous compatible protocols.
- Add optional MCP, Git workflow, indexing/LSP, telemetry, compaction, planning,
  sandbox, and subagent extensions as separately versioned modules.
- Publish scaffolding, authoring guides, protocol schemas, GoDoc, compatibility
  matrices, migrations, examples, threat models, and conformance kits.
- Measure API usage and remove accidental surface before v1 rather than keeping
  it indefinitely.

## Stabilization criteria

`v0.2` SDK beta requires successful stress prototypes plus at least one external
author able to publish, configure, debug, and test an extension without private
APIs or core changes. `v1.0` additionally requires:

1. a written Go API and protocol compatibility policy;
2. frozen generated-source ownership/source-map contracts;
3. at least two supported protocol generations with upgrade tests;
4. supported security response and dependency update processes;
5. stable benchmark thresholds and recorded regression policy;
6. no unresolved kernel concept whose design depends on a single extension.

## Verification

Prototype conformance covers interception completeness, crash markers, snapshot
round trips, uncertain-operation refusal, alternate-client semantics, distributed
cancellation, protocol skew, and generated DI replacement. External-author
testing starts from public documentation and released artifacts only.

Compatibility matrices are machine-readable and tested. Deprecations include a
replacement, first/last supported release, migration example, and automated fix
where practical. Release candidates run the complete phase 6 workflow plus all
adopted extensions.

## Performance and evidence

Extensions publish marginal startup, allocation, event-latency, cancellation,
and binary-size costs. No optional extension may impose runtime or dependency
cost when it is not imported.

Status is **complete for Phase 7 experiments**. Permission, SQLite recovery,
alternate semantic client, and two-worker extension proofs are implemented.
Independent-author and broader optional-extension proofs remain Phase 8 work;
none of the experimental APIs are stabilized by this completion. The non-native
parallel static extension design remains rejected history and may not be revived
as a shortcut during experimentation.

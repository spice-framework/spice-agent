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

Status: **in progress**. The first bounded compatibility slices commit an exact
machine-readable engine-protocol matrix, prove source-built previous (1.2) and
current (1.3) semantics through real authenticated local processes on Linux and
Windows, and run independent Go and Python plugin/v1 process services through
an immutable run-leased tool plan behind both exact engine modes. Plugin
versioning remains independent, and the evidence deliberately makes neither a
released-binary N/N-1 claim nor a native Python Host-launch claim. See
[the engine compatibility evidence](evidence/phase8-engine-protocol-compatibility.md).

The MCP experiment remains blocked before implementation. The immutable
preview5 module predates the public verified-child launcher required to start a
digest-pinned MCP server without a pathname race. The experiment will not use a
pseudo-version, `replace`, workspace source, duplicated launcher, or an
interpreter fallback; it resumes only after an immutable Agent release contains
that generic seam.

The first optional-extension proof is also complete as the removable
`experiments/compaction` nested module. It pins released preview5 without a
replacement and implements deterministic local complete-round extraction as an
explicit application-owned `model.Provider` wrapper. The delegate alone owns
the one model operation; authoritative engine history, events, and snapshots
remain unmodified, and exact semantic options feed the application's portable
snapshot identity. Generated Spice construction, fuzz/race/coverage, security,
benchmark, compatibility, and deletion evidence are owned by the experiment.
See [the compaction evidence](evidence/phase8-compaction-experiment.md).

The guarded Git workflow proof is complete as the removable
`experiments/git-workflow` nested module. It pins preview5 without a replacement
and contributes only fixed `git.inspect` and interaction-guarded
`git.commit_staged` tools through generated Spice injection. Authority binds the
exact staged index and run plan; hooks, signing, credentials, arbitrary
arguments, repository mutation helpers, and network Git operations are absent.
Executable/repository/config identities, bounded output, cancellation,
uncertainty, and real Windows/Unix process-tree containment are proven. Because
preview5 lacks the later generic atomic verified-child seam, strict held-
identity plus pre/post SHA-256 checks are explicitly experimental and promotion
is blocked until a released Agent version supplies `VerifiedLauncher`. See
[the Git workflow evidence](evidence/phase8-git-workflow-experiment.md).

The best-effort telemetry projection proof is complete as the removable
`experiments/telemetry` nested module. It pins preview5 without a replacement,
uses one Agent-owned bounded mailbox and one consumer, emits only immutable
closed-schema values, and never exports generic event payloads. Process-local
HMAC pseudonyms and the public typed tool occurrence decoders provide safe run
and tool correlation; slow exporters produce exact accounted drops without
backpressure or retry. Generated construction proves real engine shutdown
precedes mailbox close, accepted-event drain, and exporter shutdown. This is an
exporter-neutral diagnostic projection, not durable history, OpenTelemetry, or
distributed trace continuity. See
[the telemetry evidence](evidence/phase8-telemetry-experiment.md).

The deterministic planning proof is complete as the removable
`experiments/planning` nested module. It injects an application-owned named
typed planner stage, separates inspectable preparation from explicit worker
start, and appends one bounded canonical JSON plan to ordinary initial user
history. SHA-256 identities bind the definition, original message, producer,
goal, and ordered backward-only steps. Suspended and terminal snapshots preserve
the exact bytes; same-identity resume does not replan, while a different planner
semantic identity fails before execution. A real terminal guard still denies a
plan-recommended mutation, proving the plan has no authority. See
[the planning evidence](evidence/phase8-planning-experiment.md).

- Obtain three independently authored compiled extensions using only public
  annotation/SDK contracts.
- Run both runtime-plugin languages and a second client against current and
  previous compatible protocols. The plugin-language half is complete. The
  alternate semantic shell proves the second client contract independently,
  while its version-skew matrix remains a separate slice.
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

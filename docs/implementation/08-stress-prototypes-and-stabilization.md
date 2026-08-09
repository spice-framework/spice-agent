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

### SQLite recorder/restorer

Build an observer/snapshot extension that records events durably, marks crashes,
restores safe snapshots, branches history, and refuses to replay uncertain
mutating operations. It must exercise required-observer backpressure and
durability failure without changing kernel storage policy.

### Alternate client and rich companion

Implement a second shell using the same UI-neutral client/session values and
portable semantic views. It must not import Bubble Tea presentation or require
plugin-provided executable UI.

### Two-worker distributed extension

Implement two cooperating workers entirely as an extension using ordinary runs,
messages, tools, and events. Success means the kernel needs no parent/child,
subagent, swarm, or scheduler concepts. If verification across multiple
annotation occurrences is truly required, propose a generic Spice aggregate-
analysis RFC with non-agent use cases.

Each prototype has its own experimental module, compatibility manifest,
conformance tests, benchmarks, and deletion path. It becomes a public repository
only after its contract passes and a dependency/security review is approved.

## Phase 8 ecosystem breadth

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

Status is **planned**. The non-native parallel static extension design remains
rejected history and may not be revived as a shortcut during experimentation.

# Phase 2: Kernel and Events

**Objective:** provide immutable value contracts and a deterministic single-agent
state machine. Every started run, turn, model operation, tool operation, and
interaction has exactly one terminal event. Sequences increase strictly;
cancellation preserves emitted history; panics become typed failures.

Required observers may backpressure explicitly. Best-effort observers are
bounded, report drops, and never block execution. **Acceptance:** text and tool
loops, malformed streams, maximum turns, cancellation, panic, race, snapshot
round-trip, and deterministic replay tests. **Status:** in progress.

## Hardened preview contracts

- Model requests carry a host-issued operation ID and immutable messages and
  tool definitions. Provider metadata is optional namespaced bounded JSON; it
  must never contain credentials, authorization data, prompts, or secrets.
- A stream is a strict tagged union. The published per-item and aggregate text
  and tool-call limits are shared by providers and the engine. A failed stream
  item counts as observed, so it is never classified as a before-stream retry.
- Tool definitions and capabilities are constructor-time snapshots exposed by
  every dispatcher and decorator. Calls, results, and progress are immutable;
  the dispatcher enforces active call identity even when a tool ignores a
  reporter error. Tools are trusted concurrent singleton beans and may not
  retain a reporter after `Execute` returns. Snapshot plan matching fingerprints
  each complete tool contract, not only its bean name.
- Tool definitions require explicit effect and replay-safety metadata.
  Capabilities are canonical unordered sets, and contradictory read-only
  mutation capabilities fail closed. Model-visible problem results remain
  distinct from bounded, call-correlated infrastructure failures. Typed
  failures preserve cancellation, distinguish definitive from uncertain
  mutation outcomes, and cannot carry unsafe retry advice. Execution errors
  must be direct typed values rather than wrappers or joins. Tool terminal
  events use a strict versioned occurrence retaining bounded call, name, kind,
  outcome, and retry correlation while structurally excluding result output,
  problem/error text, paths, and secrets. A valid successful
  result wins a concurrent cancellation after tool commit; a cancellation
  sentinel returned while the run context is active remains a run failure.
  Simultaneous execution and reporter failures retain the authoritative typed
  outcome plus explicitly inspectable durability without reporter-controlled
  cancellation classification.
- Every `ToolStarted` event is a strict agent-owned 4 KiB occurrence rather
  than an open map. It contains declared/executable status, the exact definition
  security metadata and fingerprint, exact leased plan/workspace authority, and
  turn, but cannot contain arguments, paths, descriptions, schemas, or secrets.
  Unknown model tool names commit a false/false occurrence and fail before
  guard or tool execution. Daemon `engine/v1` continues to expose only the
  legacy `call_id` and `name` projection.
- Every `ToolCompleted` and `ToolFailed` event is a strict agent-owned 1 KiB
  terminal occurrence. Its kind must match the event, each open call closes
  exactly once, and optional execution-state/retry facts are validated as one
  pair. The daemon retains the legacy terminal JSON using an empty completion
  error or fixed safe failure problem rather than exposing local error text.
- Each run owns a count-and-encoded-byte-bounded authoritative event log.
  `Subscribe(ctx, afterSequence)` creates an independent gap-free replay/tail
  cursor. Typed out-of-range and resource-exhaustion errors provide recovery
  cursors and last-delivered sequence; health stats expose retention, eviction,
  exhaustion, and slow-subscriber disconnection. Delivery keeps its in-flight
  entry accounted until the consumer accepts it; concurrent cancellation or
  exhaustion cannot panic, duplicate it, or report a stale recovery cursor.
- The local log commits a sequence before required-observer acknowledgement.
  Post-commit acknowledgement failures never reuse that sequence and every
  committed lifecycle start is paired with exactly one terminal. Best-effort
  publication happens only after required observers acknowledge.
- Run terminal and failure persistence use a bounded context independent of
  caller cancellation and surface typed durability errors. `Close` rejects new
  runs and drains; `Shutdown` additionally requests cooperative cancellation.

Interaction completion and snapshot import/export are implemented as preview
contracts. UI-neutral broker lifecycles are exactly
terminal under response validation, cancellation, panic, and observer failure.
Versioned snapshots round-trip deterministically, reject active or uncertain
mutations, and resume only after leasing and recomputing the exact combined
compiled/tool `PlanIdentity`, with monotonic sequence continuation. Every run
leases before mutation and releases exactly once; release failure is part of the
authoritative failed terminal and a blocking callback is bounded by finalization.
Portable import additionally requires an explicit compiler-generated
compatibility identity covering every executable static bean; defaults remain
inspection/local-resume only. Used interaction IDs survive snapshot import so a client
cannot ambiguously reuse a completed lifecycle identity. Durable SQLite recovery and uncertain-operation policy
remain the isolated Phase 7 stress proof rather than hidden kernel behavior.

**Kernel-local status:** complete for the preview contract. Cross-repository
provider/tool conformance and the architecture-proof distribution remain in
progress; this document does not claim those later phase exits.

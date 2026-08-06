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
  retain a reporter after `Execute` returns.
- Each run owns a count-and-encoded-byte-bounded authoritative event log.
  `Subscribe(ctx, afterSequence)` creates an independent gap-free replay/tail
  cursor. Typed out-of-range and resource-exhaustion errors provide recovery
  cursors and last-delivered sequence; health stats expose retention, eviction,
  exhaustion, and slow-subscriber disconnection.
- The local log commits a sequence before required-observer acknowledgement.
  Post-commit acknowledgement failures never reuse that sequence and every
  committed lifecycle start is paired with exactly one terminal. Best-effort
  publication happens only after required observers acknowledge.
- Run terminal and failure persistence use a bounded context independent of
  caller cancellation and surface typed durability errors. `Close` rejects new
  runs and drains; `Shutdown` additionally requests cooperative cancellation.

Interaction completion and snapshot import/export remain intentionally
incomplete. Phase 2 cannot close until those contracts and their round-trip
tests are implemented.

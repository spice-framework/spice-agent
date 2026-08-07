# Phase 4 RunHost lifecycle core evidence

## Outcome

The public `daemon.RunHost` is the transport-independent owner of hosted run
lifecycle. It composes the generated definition catalog, deterministic kernel,
OS-backed run authority, stable-client session store, bounded idempotency
ledger, and pending-interaction hub. It does not implement a second container or
runtime extension registry.

The typed surface is:

- `Start` resolves a server-owned definition and creates a new run.
- `Import` verifies and consumes an authority-issued snapshot before resuming it.
- `Suspend` reaches a kernel safe boundary and returns a signed snapshot.
- `Resume` invalidates the retained snapshot before execution continues.
- `Cancel` requests terminal cancellation without borrowing the request context
  as the run lifetime.
- `Respond` completes a pending interaction owned by the same stable client.
- `Export` returns only a cached authority-issued safe-boundary envelope.
- `ReplayEvents` returns one owned run's bounded atomic page and optional
  gap-free tail across active and retained-terminal runs.
- `SnapshotInteractions` returns a watcher-free complete client view, while
  `SubscribeInteractions` atomically couples that view to a live delta tail.
  Both read paths return opaque observations that own their reconnect fence
  until the transport finishes sending and calls idempotent `Close`.
- `Health` reports the configured server limits, active reservations, and fixed
  secret-safe degradation reasons.
- `Shutdown` fences admission, drains owned work, and closes authority last.

## Ordering and ownership invariants

Start and import reserve active capacity and bind the run to the stable client's
pending-interaction partition before the run can execute. Kernel preparation is
registered through `CommitPaused`, which owns the run identity and exact plan
lease but cannot publish events or call providers, stages, tools, observers, or
interaction code. The host then takes the session's mutation-commit gate and
performs the durable authority transition. Kernel `Activate` is called only
after authority is durably `ACTIVE`.

Import performs keyed envelope verification before kernel preparation. Its
durable order is authority `IMPORTING`, inert kernel registration, authority
`ACTIVE`, and then kernel activation. Once authority consumption may have been
written, an ambiguous failure is uncertain and cannot be retried.

Suspend first asks the live kernel run to stop at a safe boundary. Only then,
under the mutation gate, does the authority issue the signed envelope. A proven
pre-write failure restores local execution. An uncertain authority result is
not resumed. Local resume reserves the exact suspended boundary, durably
invalidates the authority envelope, and only then commits kernel execution.

All mutating methods validate the stable-client epoch and use bounded operation
IDs. A failure proved to occur before any durable or visible boundary abandons
its ledger entry so the same operation ID can retry. Success, business failure,
and uncertainty remain committed deterministic results. Another client's run
and an unknown run produce the same public unavailable result.

Host construction proves the PendingHub's maximum client snapshot count and a
conservative complete-frame byte bound fit the configured protocol limits.
The configured concurrent-stream limit is also enforced by SessionStore lease
admission rather than left to a transport convention.

Per-run lifecycle serialization is context-aware. A request waiting behind an
active Suspend, Resume, Cancel, or terminal transition can stop on its own
deadline without taking the transition token or changing the run. Setup work
observes the request, stable-client session, and host lifetimes together, so a
reconnect or daemon shutdown cannot strand provider-plan acquisition before a
commit boundary. Proven pre-write authority unavailability is retriable for
Start, import preparation/consumption, Suspend envelope issuance, and Resume;
an attempted authority write remains uncertain and permanently memoized.

## Retention, degradation, and shutdown

Active reservations have a configured hard limit. Terminal authority envelopes
have independent count and encoded-byte limits and are evicted in deterministic
completion order. Accepted interaction bindings drain after terminalization,
so a response already in flight is not lost merely because the run completed.

Authority uncertainty, authority unavailability, terminal-snapshot failure, and
lifecycle-cleanup failure add fixed safe Health reasons. They do not expose keys,
tokens, provider data, snapshot payloads, or arbitrary dependency error text.

Shutdown marks the host closed synchronously, cancels the host-owned run root,
closes sessions and pending admission, aborts inert candidates, joins accepted
operations, shuts down the kernel, joins terminal monitors and binding drains,
shuts down session resources, and closes run authority last. A caller deadline
bounds only that caller's wait; cleanup continues in the background.

The RunHost terminal-envelope cache is bounded, but `agent.Engine` retains
seen-run identity tombstones for its entire service lifetime without a bound.
Terminal eviction therefore does not bound total process-lifetime identity
retention. This remains an explicit pre-freeze daemon lifecycle limitation.

## Exclusions

This slice contains transport-neutral event and interaction read seams, but no
gRPC stream sender, Protobuf/RPC translation for those seams, listener, Unix
socket, Windows named pipe, endpoint discovery, managed daemon startup, or TUI
client adapter. RunHost acquires the `SessionStore` lease into an opaque
observation; the transport must retain that observation until its final sender
has joined and then call `Close`, which cancels and joins internal delivery
before releasing the reconnect fence.

## Verification status

Focused lifecycle tests cover configuration rejection, authority-before-kernel
activation, request-context independence, operation deduplication and
abandonment, active capacity and reclamation, uncertain authority behavior,
terminal-cache eviction, stable ownership, interaction response, suspend/local
resume/import ordering, context-cancelable transition waits, session/host
cancellation during pre-commit setup, per-authority-path retry classification,
and synchronous shutdown admission fencing. The focused suite passed 20
shuffled repetitions and three race-enabled repetitions; authority Start's
attempted-write regression passed ten shuffled repetitions. Repository
`make check` passed on the same tree before the full gate.

Stream-seam tests additionally cover active and terminal replay, paging, live
tailing, cursor gaps, byte exhaustion, non-disclosing ownership, session epoch
cancellation, complete pending snapshots, client isolation, open/close deltas,
observer admission, and snapshot-only operation without allocating a watcher
or partition.

The exact combined RunHost tree passed repository `make verify` before commit.
This document does not claim an RPC stream adapter, IPC, managed startup,
Windows/Linux process, or release acceptance.

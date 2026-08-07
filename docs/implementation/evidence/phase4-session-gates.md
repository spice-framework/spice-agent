# Phase 4 Session Commit and Stream Gate Evidence

This evidence covers the transport-independent ownership boundary used by a
future engine RPC adapter. It does not claim that the gRPC host, local IPC, or
managed-daemon discovery is implemented.

## Public contract

Each stable client has an independent mutation-commit gate. Commit leases are
exclusive, contended claimants are admitted through a hard-bounded FIFO, and a
registered reconnect intent has priority over queued old-epoch mutations. The
lease begins only at the mutation's commit boundary: cancellation after
acquisition does not implicitly release it or claim that an external operation
did not commit.

Stream acquisition racing an active reconnect waits rather than crossing its
fence. Those waiters are explicitly registered, share the hard per-client gate
bound, and count against orderly shutdown until each caller returns. They do not
receive FIFO ordering because they are read-only stream attachments; mutation
commit waiters retain the ordering guarantee.

`ReconnectContext` drains the active commit before it starts fencing streams.
It cancels each old stream context and then waits for every opaque stream lease
to close. A stream handler closes its lease only after all of its senders have
joined, so a successful reconnect cannot be followed by an old-epoch frame.
Only after both drains complete does reconnect atomically advance the epoch and
cancel the prior `Session.Context`.

A reconnect canceled before that compare-and-swap removes its priority intent,
preserves the old epoch context, and resumes the old mutation FIFO. Streams
already asked to stop remain canceled; cancellation cannot be undone, and their
leases still require an explicit join acknowledgement. Known stale clients
receive immutable expected/observed epoch facts. An unknown client retains the
sentinel-only not-found behavior because no positive authoritative epoch can be
reported.

The exported `SessionStore` zero value is closed: all work fails with
`ErrSessionStoreClosed`, while `Close` and `Shutdown` are safe and idempotent.
`SessionStore.Close` is nonblocking: it rejects new work, cancels session and
stream contexts, and wakes all queued claimants. `Shutdown(ctx)` additionally
waits for active commit/stream leases and every registered mutation, reconnect,
or stream-acquisition claimant to leave, bounded by its caller-owned context.
No store mutex is held while invoking cancellation, reading randomness,
stopping context callbacks, or waiting for a lease, stream, claimant, or caller
context.

## Executable acceptance

| Contract | Focused evidence |
| --- | --- |
| Reconnect priority | A paused active commit, queued mutation, and later reconnect prove that reconnect advances first and the old queued mutation receives typed stale facts. |
| FIFO restoration | Cancellation both before fencing and while an old stream is joining proves the epoch remains unchanged and queued mutations resume in original order. |
| One-winner CAS | Thirty-two simultaneous context-aware reconnect claimants produce exactly one next-epoch owner and thirty-one stale results. |
| Stream join fence | A deliberately paused sender observes cancellation, emits its final old frame, joins, and closes its lease before reconnect returns; no frame can arrive afterward. |
| Bounds and isolation | Hard combined mutation/reconnect/stream-acquisition waiter and active-stream capacities return a typed resource and maximum; one client's paused commit does not block another client's commit or reconnect. |
| Closure and root ownership | Nonblocking close cancels contexts while active leases remain, a short shutdown times out, and final lease closure drains. Queued mutation, reconnect, and stream-acquisition claimants wake on explicit close and root cancellation without advancing the epoch; a tracked-waiter regression proves shutdown cannot report a false drain. Zero-value store methods fail closed without panic. |
| Race safety | High-iteration cancellation/release races, shuffled repetitions, the Go race detector, vet, golangci-lint, NilAway, and package coverage exercise the split session core, reconnect, gate, and error implementation. |

The accepting parent slice records exact commands and timings after integrating
this work with the concurrent client and run-authority changes. Repository-wide
`make check` and exact-tree `make verify` remain required before commit.

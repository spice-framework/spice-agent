# Phase 4 Host Foundation Evidence

This evidence covers only transport-independent daemon semantics. It does not
claim a gRPC server, Protobuf adapter, authentication interceptor, OS listener,
endpoint discovery, managed process, run host, client, or TUI.

| Contract | Executable evidence |
| --- | --- |
| Server-owned definitions | Canonical sorting and SHA-256 revision tests prove exact `agent.Definition` model/turn policy, duplicate rejection, defensive copies, bounded count, and exact resolution. |
| Stable client ownership | High-contention reconnect tests prove one CAS winner, stable cryptographic identity, epoch fencing, root/explicit shutdown cancellation, overflow refusal, and hard capacity. |
| Idempotent mutations | Owner/waiter races prove stable-client operation identity, canonical digest conflicts, committed-result precedence, independent waiter cancellation, per-client fairness, total bounds, and exact duplicate outcomes. Error, panic, and invalid-result tests prove bounded secret-safe uncertain commitment. |
| Pending interactions | Tests prove exclusive stable-client run bindings, client-isolated composite keys, binding-release admission fencing without abandoning accepted prompts, mandatory sorted complete snapshots, atomic snapshot/tail registration, independent contiguous client revisions, wrong-client response rejection, accepted-response precedence, close-revision reservation, and defensive copies. |
| Bounded observation | Explicit global/per-client run, pending-entry/byte, observer, and reserved/actual queue-entry/byte tests prove every admitted queue is funded. Slow subscribers receive a typed exact `LastDelivered` recovery revision and cannot backpressure interaction execution. |
| Reconnect and closure | Fencing synchronously joins the old client's delivery loops, drains queued payload references, and preserves pending state for a new complete-first subscription. Pre-canceled work publishes nothing; nil hub/subscription values fail closed; deterministic close advances every pending terminal revision, releases bindings, clears accounting, and joins unread observers. |
| Architecture | `daemon` is a public Modulith named interface and imports only public kernel contracts. It contains no gRPC, Protobuf, listener, endpoint, registry, reflection, or OS IPC implementation. |

The focused package test and race suite, repository `make fast`, `make check`,
and exact-tree `make verify` are required before the slice is committed. The
commit identifier and final timings are reported after push because a commit
cannot contain its own object ID.

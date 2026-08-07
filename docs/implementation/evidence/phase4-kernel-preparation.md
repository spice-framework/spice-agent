# Phase 4 Transactional Kernel Preparation Evidence

This slice provides the kernel transaction needed by a future daemon host. It
does not implement protocol translation, run authority, idempotent RPC handling,
authentication, a listener, or OS IPC.

| Contract | Executable evidence |
| --- | --- |
| Prepared new run | `PrepareStart` validates input, leases the current immutable plan, allocates and exposes `RunID`, and constructs the bounded log without engine registration, events, provider work, or a goroutine. |
| Prepared snapshot resume | `PrepareResumeSnapshot` validates the suspended snapshot, compiled compatibility, exact recorded plan, tail cursor, history, and interaction/message identities without publishing authority. |
| Context ownership | Tests cancel setup after preparation and prove both start and resume remain viable; only the separately supplied commit root cancels the running provider and selects the terminal event. |
| Registered-run finalization | Immediate cancellation still commits `RunStarted` through a bounded cancellation-independent lifecycle context and produces exactly one `RunCancelled`. If lifecycle-start persistence cannot commit, the registered run still attempts and records its terminal outcome when capacity permits. |
| Atomic terminal choice | High-contention start and resume tests race `Commit` against `Abort`; exactly one transfers ownership, the other receives a typed state error, and every lease releases exactly once. |
| Duplicate authority | Two preparations for the same snapshot ID are allowed, exactly one engine commit succeeds, the loser releases, and later imports fail before plan acquisition. |
| Failure closure | Nil and pre-canceled setup/root contexts, closed engines, invalid snapshots, plan mismatches, duplicate IDs, release failures, repeated commit/abort/close, and delayed canceled acquisition fail without visible partial runs. |
| Compatibility | Existing `Start` and `ResumeSnapshot` are prepare-plus-commit wrappers and retain ordinary caller-owned lifetime behavior. |

Focused tests, repeated race tests, `make fast`, `make check`, and exact-tree
`make verify` are required before commit. The pushed commit is recorded outside
itself in the canonical ledger.

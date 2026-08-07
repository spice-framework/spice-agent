# Phase 4 Prepared Local Resume Evidence

This slice gives the daemon a local kernel-resume transaction without adding
transport, persistence, authentication, or run-authority policy to the kernel.

| Contract | Executable evidence |
| --- | --- |
| Inert reservation | `Run.PrepareLocalResume` captures the run identity and exact next sequence without a context, I/O, event, goroutine, provider call, tool call, or interaction. |
| Explicit decision | `PreparedLocalResume.Commit`, `Abort`, and `Close` form one synchronized decision. Commit and abort contention has one winner; duplicate and conflicting decisions are deterministic. |
| Cancellation handshake | Run cancellation and engine shutdown latch behind a pending decision. Commit after cancellation and abort after cancellation both release exactly one terminal finalization without post-boundary work. |
| Safe rollback | Aborting a live reservation restores a byte-identical suspended snapshot and preserves the original event boundary. |
| Boundary isolation | Snapshot export, another suspend/resume reservation, and interactions fail while the reservation is pending. No event sequence is consumed until the decision releases execution. |
| Compatibility | `Run.Resume` is a prepare-plus-commit wrapper. Cancellation of an in-flight `Suspend` still auto-resumes a boundary established after that request was cancelled. |

The agent tests cover live commit and abort, cancellation before each decision,
shutdown timeout and drain, exact event sequences, nil and invalid states,
duplicate decisions, and high-contention commit/abort races. Focused shuffled,
race, vet, lint, NilAway, and coverage gates are required before integration.

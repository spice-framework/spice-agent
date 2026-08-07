# Phase 4 Kernel Activation-Gate Evidence

This prerequisite closes the execution gap between durable daemon authority
transitions and kernel publication. It does not implement `daemon.RunHost`,
snapshot-envelope issuance, gRPC, authentication, listeners, or OS IPC.

| Contract | Executable evidence |
| --- | --- |
| Inert registration | `PreparedStart.CommitPaused` and `PreparedResume.CommitPaused` atomically register the run ID, replay log, and exact tool-plan lease while exposing only the stable run ID. Provider, tool, broker, observer, stage, and event work remains behind a private gate. |
| Authority-safe activation | `PreparedRun.Activate` is a context-free, exactly-once decision and is the only operation that returns the registered `*agent.Run`. A daemon can therefore consume a snapshot, commit the inert kernel run, durably activate authority, and only then release execution. |
| Cancellation latch | Run-root cancellation and engine shutdown cancel the registered run context but cannot decide the gate. After durable authority activation, `Activate` still succeeds and ordinary execution observes the cancellation. Before authority activation, explicit `Abort` remains available. |
| Side-effect-free rollback | `PreparedRun.Abort` performs kernel-owned finalization without calling `executeState`: no lifecycle event, observer, provider, stage, tool, or interaction broker runs. It releases the plan lease with a bounded independent context, closes the empty log, removes the engine-active entry, and joins before returning unless the caller's wait context expires. Cleanup continues independently after such a timeout. |
| Shutdown ordering | Engine shutdown deliberately waits for the explicit gate decision rather than guessing whether external authority committed. Tests prove a bounded shutdown can time out at the gate and drains after the owner aborts. |
| Compatibility | Existing `PreparedStart.Commit`, `PreparedResume.Commit`, `Engine.Start`, and `Engine.ResumeSnapshot` atomically pre-activate the gate before launching its waiter, preserving their prior run/event behavior including immediate root cancellation. |

Focused shuffled tests repeated ten times, race tests, `go vet`, the allowlisted
golangci-lint policy, NilAway, and diff hygiene passed for the `agent` package on
the working tree containing this prerequisite.

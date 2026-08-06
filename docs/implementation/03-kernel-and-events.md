# Phase 3: Kernel and Events

**Objective:** provide immutable value contracts and a deterministic single-agent
state machine. Every started run, turn, model operation, tool operation, and
interaction has exactly one terminal event. Sequences increase strictly;
cancellation preserves emitted history; panics become typed failures.

Required observers may backpressure explicitly. Best-effort observers are
bounded, report drops, and never block execution. **Acceptance:** text and tool
loops, malformed streams, maximum turns, cancellation, panic, race, snapshot
round-trip, and deterministic replay tests. **Status:** in progress.


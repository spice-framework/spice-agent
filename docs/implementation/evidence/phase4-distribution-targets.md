# Phase 4 Generated Distribution Target Evidence

The `spice-agent-coding` distribution now contains two independently generated
Spice applications. `spice-agentd` owns typed daemon configuration, provider and
compiled-tool beans, engine/run host, authenticated gRPC, current-user local IPC,
publication ordering, and graceful reversal. `spice-agent` owns Bubble Tea
presentation, explicit attach, managed attach-or-start, protocol initialization,
the UI-neutral session bridge, and cleanup of only its own daemon candidate.

Exact selections exercised by the Windows proof:

| Component | Commit/version |
| --- | --- |
| Spice Agent core | `4a1c812` |
| Spice Agent TUI | `a0d4824` |
| Coding tools | `eeacf58` |
| Coding distribution | `8f92368` |

The distribution's exact `make verify` passed in 115.7 seconds at 87.0% whole
handwritten-product coverage. It included deterministic `--check`/`--diff` for
the architecture proof, daemon, and terminal targets; `-trimpath` builds for
both executables; lint/NilAway; vulnerability and security scans; shuffled and
race tests; and offline-vendor execution. `go run ./cmd/spice-agent --check`
constructed and reversed the complete terminal graph without discovery,
connection, or process launch.

This is Windows composition/build evidence, not installed-terminal acceptance.
Real Windows and Linux terminal interaction, resize/reconnect, and release
packaging remain required before Phase 4 freezes.

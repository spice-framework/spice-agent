# Spice Agent Implementation Ledger

This directory is the canonical, append-only source of phase status and evidence.
Repository roadmaps link here and must not duplicate status.

| Phase | State | Exit evidence |
| --- | --- | --- |
| 0 — product and repositories | In progress | repository governance, exact toolchain, offline build, quality gate |
| 1 — Spice-native composition | In progress | annotation/DI fixture, generated graph diagnostics |
| 2 — deterministic kernel | In progress | scripted vertical tests, race/cancellation/terminal-event proof |
| 3 — provider and coding tools | Planned | external repositories and acceptance |
| 4 — daemon and TUI | Planned | local protocol and reconnect proof |
| 5 — runtime plugins | Planned | Go/Python conformance and generation leases |
| 6 — architecture proof | Planned | signed `v0.1.0-preview.1` distribution |
| 7 — stress prototypes | Planned | permission, SQLite, alternate UI, two-worker experiments |
| 8 — stabilization | Planned | external authors and frozen compatibility policy |

Exact commits and command output are recorded only after the corresponding gate
has run. A phase is not complete because code exists; every exit criterion in its
document must be green on Windows and Linux where required.

## Current API-hardening blockers

Phase 2 remains incomplete. Before daemon or provider integration is accepted,
the next slice must make model/tool wire values deeply immutable, snapshot tool
definitions in the dispatcher, enforce call/progress correlation and cancellation,
preserve typed provider failures, and replace the current single-consumer run
event delivery with a count-and-byte-bounded authoritative log plus independent
`Subscribe(afterSequence)` replay. It must also make terminal persistence and
engine shutdown explicit. Interaction completion and snapshots are contracts,
not completed runtime implementations.

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

## Current Phase 2 boundary

The immutable model/tool contracts, typed provider failures, dispatcher
capability snapshot, call/progress correlation, bounded independent event
replay, terminal durability, and engine shutdown lifecycle are implemented.
They remain pre-1.0 contracts and will be exercised by the independent OpenAI
provider and coding-tool repositories before stabilization.

Phase 2 is still incomplete: interaction completion and snapshot import/export
remain documented seams rather than runtime implementations. Daemon protocol
work must not imply those contracts are complete.

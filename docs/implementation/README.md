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
are now implemented as bounded preview contracts with deterministic round-trip,
resume, cancellation, panic, observer-failure, and race tests. Phase 2 remains
listed in progress until this exact tree is committed and its cross-repository
provider/tool conformance evidence is recorded; durable SQLite recovery remains
the Phase 7 stress proof.

## Completed evidence

- Organization governance/profile: `.github` `11e9470`.
- Development catalog/workspace: `36a3bf5`; `make verify` 98s, 85.5% coverage.
- Core foundation: `spice-agent` `218ffbb`.
- Professional quality baseline: `829dd0a`; `make verify` 27.6s, 87.7% coverage.
- Hardened deterministic kernel: `eaf1918`; `make verify` 23.9s, 88.4% coverage.
- OpenAI provider foundation `d57447f`, implementation `62b9481`, and
  timeout/concurrency hardening `729017d`; final `make verify` 89.3% coverage
  with lint, security, race, offline, and vendor gates green.
- Coding-tools foundation `707b60a` and green main implementation `9afe9e3`;
  final `make verify` 87.6% coverage. The manifest/Windows rename follow-up
  remains in progress and is not recorded as final head.
- TUI foundation through `spice-agent-tui` `28a89dc`.
- Reference distribution through `spice-agent-coding` `2809a1a`.

Active follow-up slices are not recorded as completed evidence until their
repositories publish green commits.

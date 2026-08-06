# Spice Agent Implementation Ledger

This directory is the canonical, append-only source of phase status and evidence.
Repository roadmaps link here and must not duplicate status.

| Phase | State | Exit evidence |
| --- | --- | --- |
| 0 — product and repositories | In progress | repository governance, exact toolchain, offline build, quality gate |
| 1 — Spice-native composition | In progress | annotation/DI fixture, generated graph diagnostics |
| 2 — deterministic kernel | Complete for preview | `841edd3`; deterministic lifecycle, interaction, snapshot, race/fuzz proof |
| 3 — provider and coding tools | In progress | generated cross-repository continuation and opt-in live acceptance |
| 4 — daemon and TUI | Planned | local protocol and reconnect proof |
| 5 — runtime plugins | Planned | Go/Python conformance and generation leases |
| 6 — architecture proof | Planned | signed `v0.1.0-preview.1` distribution |
| 7 — stress prototypes | Planned | permission, SQLite, alternate UI, two-worker experiments |
| 8 — stabilization | Planned | external authors and frozen compatibility policy |

Exact commits and command output are recorded only after the corresponding gate
has run. A phase is not complete because code exists; every exit criterion in its
document must be green on Windows and Linux where required.

## Phase 2 preview boundary

The immutable model/tool contracts, typed provider failures, dispatcher
capability snapshot, call/progress correlation, bounded independent event
replay, terminal durability, and engine shutdown lifecycle are implemented.
They remain pre-1.0 contracts and will be exercised by the independent OpenAI
provider and coding-tool repositories before stabilization.

Interaction completion and snapshot import/export are bounded preview contracts
with deterministic round-trip, resume, cancellation, panic, observer-failure,
race, replay-gap, identifier-reuse, and tool-plan-fingerprint tests. Commit
`841edd3` passed `make verify` in 29.9s at 86.2% repository coverage. Durable
SQLite recovery remains the isolated Phase 7 stress proof.

## Current Phase 1 boundary

Canonical `@Stage`, `@Tool`, and `@ModelProvider` descriptors and their
authorized v1alpha2 process are implemented using only generic Spice provider
and bean-metadata contributions. The in-module `CompositionProof` application
now commits and executes the real generated graph, selection matrix, ordered
collections, canonical tool map, cleanup/rollback, typed override, ownership,
diagnostic, module, and source-mapping proof. Exact reproducible commands and
asserted output are recorded in
[`evidence/phase1-composition.md`](evidence/phase1-composition.md). Phase 1
remains in progress until auto-configuration contracts and cross-repository
compiled continuation evidence are complete.

## Current Phase 3 boundary

The provider and coding-tool repositories are implemented and independently
repinned, but Phase 3 is not complete. Remaining exit evidence is a generated
cross-repository compiled tool-call continuation through the kernel and the
opt-in live OpenAI acceptance, which has not been run because no credentials
were supplied. Offline scripted acceptance remains the mandatory default.

## Current infrastructure blocker

A Windows clean-clone audit of all five repositories passed Go 1.26.5,
`make fast`, offline vendor tests with `GOWORK=off GOPROXY=off
GOFLAGS=-mod=vendor`, and `govulncheck`. This is not Linux or macOS evidence and
does not close Phase 0. A separate WSL2 Linux 6.18.33.1 audit with Go 1.26.5
linux/amd64 used fresh public clones: all five repositories passed `make fast`
and explicit `GOWORK=off GOPROXY=off GOFLAGS=-mod=vendor go test ./...`.
Fresh-clone full verification still needs a source-preserving dependency
bootstrap. After cache preparation, `spice-agent` passed in 32.2s and
`spice-agent-coding` passed in 6.7s; the other repositories exposed bootstrap
or `go.sum` preservation gaps being corrected separately. This is not macOS
evidence and does not close Phase 0. GitHub Actions jobs remain queued without
starting; diagnosing organization billing/policy requires unavailable
`admin:org` authority. Core/tools Dependabot gRPC alerts remain open and must
not be dismissed, although their tools modules already pin v1.82.1 and local
`govulncheck` is clean.

## Completed evidence

- Organization governance/profile: `.github` `11e9470`.
- Development catalog/workspace: `36a3bf5`; `make verify` 98s, 85.5% coverage.
- Core foundation: `spice-agent` `218ffbb`.
- Professional quality baseline: `829dd0a`; `make verify` 27.6s, 87.7% coverage.
- Hardened deterministic kernel: `eaf1918`; `make verify` 23.9s, 88.4% coverage.
- Completed preview kernel: `841edd3`; `make verify` 29.9s, 86.2% coverage.
- OpenAI provider final repin `88c3044`; `make verify` 29.25s, 89.3% coverage.
- Coding-tools final repin `653b405`; `make verify` 33.19s, 87.7% coverage.
- TUI foundation through `spice-agent-tui` `28a89dc`.
- Reference distribution through `spice-agent-coding` `2809a1a`.

Active follow-up slices are not recorded as completed evidence until their
repositories publish green commits.

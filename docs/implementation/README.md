# Spice Agent Implementation Ledger

This directory is the canonical, append-only source of phase status and evidence.
Repository roadmaps link here and must not duplicate status.

| Phase | State | Exit evidence |
| --- | --- | --- |
| 0 — product and repositories | In progress | repository governance, exact toolchain, offline build, quality gate |
| 1 — Spice-native composition | Complete for preview | generated static DI, auto-configuration, cross-repository continuation |
| 2 — deterministic kernel | Complete for preview | `841edd3`; deterministic lifecycle, interaction, snapshot, race/fuzz proof |
| 3 — provider and coding tools | In progress | generated cross-repository continuation and opt-in live acceptance |
| 4 — daemon and TUI | In progress | provisional local protocol baseline; host/reconnect proof pending |
| 5 — runtime plugins | In progress | execution-outcome and kernel plan-lease prerequisites complete; plugin host/conformance pending |
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

## Phase 1 preview completion

Canonical `@Stage`, `@Tool`, and `@ModelProvider` descriptors and their
authorized v1alpha2 process are implemented using only generic Spice provider
and bean-metadata contributions. The in-module `CompositionProof` application
now commits and executes the real generated graph, selection matrix, ordered
collections, canonical tool map, cleanup/rollback, typed override, ownership,
diagnostic, module, and source-mapping proof. Provider, coding-tool, and TUI
repositories now contribute through explicit auto-configuration, and
distribution commit `4cfd19a` proves the generated cross-repository compiled
continuation without a registry or `RuntimeGraph`. Exact reproducible commands
and asserted core output are recorded in
[`evidence/phase1-composition.md`](evidence/phase1-composition.md). This closes
Phase 1 for the preview; pre-1.0 contract changes remain governed by the later
stress and external-author phases.

## Current Phase 3 boundary

The provider and coding-tool repositories, including the opt-in live test path,
are implemented and independently repinned. The generated cross-repository architecture proof is green at
`spice-agent-coding` commit `16244f5`: Windows `make fast` was 15.2 seconds,
`make check` was 18.2 seconds, and `make verify` was 65 seconds at 86.0%
coverage. A clean WSL2 Linux/amd64 clone at that commit ran Go 1.26.5,
`make tools-bootstrap` in 1.35 seconds, and full `make verify` in approximately
75 seconds with zero lint findings, no reachable vulnerabilities, race tests,
and vendor-offline acceptance; the worktree remained clean. Phase 3 is not
complete because opt-in live OpenAI acceptance has not been run with supplied
credentials. Distribution commit `4cfd19a` (including follow-up `1dbef3d` and
exact provider `4beed383` / coding-tools `17cbef3b`) proves real provider HTTP-request
cancellation, exact model/turn/run terminal events, and secret scans across
events, generated source, and manifests; `make verify` passed in 73.8 seconds
at 86.4% coverage with zero lint findings, no reachable vulnerabilities, race,
and vendor-offline checks. Offline scripted acceptance remains the mandatory
default.

## Current Phase 4 boundary

`common/v1` and `engine/v1` now define the provisional repository-owned Protobuf boundary
and handwritten fail-closed validation for negotiation, health, run creation,
ordered bounded replay, cancellation, stale interactions, and safe snapshot
transfer. The repaired boundary advertises server-owned definitions, encodes
and validates stable-owner reconnect CAS, keeps auth in transport metadata, separates pending
interaction snapshots/deltas from run events, pages replay atomically, and
preserves snapshot run identity through suspend/resume/import. Buf lint,
FILE-level breaking comparison against
`schema-baseline`, exact local Go-tool generation, and byte-identical freshness
are normal offline gates. Unknown fields, old/new peers, capabilities, overload,
replay gaps, stale clients, snapshot version skew, generated service shape, and
protocol fuzzing are covered. No daemon, listener, transport authentication,
managed startup, or TUI implementation is claimed by this slice. Reproducible
commands are recorded in
[`evidence/phase4-protocol.md`](evidence/phase4-protocol.md).
The first host-foundation slice now implements the transport-independent
`daemon` named interface: exact immutable definition catalogs, root-owned
stable-client reconnect CAS, bounded per-client idempotency with panic-safe
uncertain outcomes, and an atomic complete-first pending-interaction broker.
Its deterministic concurrency, shutdown, overflow, aliasing, capacity, and
secret-containment evidence is recorded in
[`evidence/phase4-host-foundation.md`](evidence/phase4-host-foundation.md).
No RPC server or local transport is claimed by this foundation.
The schema is not the final Phase 4 freeze: the host slice must prove these
repaired contracts over real RPCs, enforce same-daemon run tombstones and
cross-process import authority, and ensure RPC contexts never own run lifetime
before the baseline can be declared stable.

## Phase 5.0A execution prerequisite

The shared tool contract now requires effect and replay-safety metadata,
canonicalizes capabilities as an unordered set, and fails closed on read-only
definitions that request mutation-capable operations. `Tool.Execute` separates
model-visible problem results from bounded correlated infrastructure errors.
The dispatcher rejects ambiguous, untyped, mismatched, and replay-unsafe error
combinations while preserving typed errors and normal cancellation checks.
This is a prerequisite shared by compiled and runtime tools; it does not add a
plugin protocol, dynamic plan source, generation lease, or retry engine. Exact
acceptance commands are listed in
[`evidence/phase5-tool-execution.md`](evidence/phase5-tool-execution.md).

## Phase 5.0B immutable tool-plan prerequisite

The kernel now leases one immutable dispatcher generation per run through the
generic `stage.ToolPlanSource` boundary. Acquisition precedes mutation, snapshot
resume leases the exact recorded `ToolPlanID`, `PlanIdentity` combines compiled
bean identities with tool definitions, and release occurs exactly once before
terminal selection. Static embedded applications retain their existing
constructors through `StaticToolPlanSource`. Ordered decorators fail closed and
cannot change or bypass the snapshotted definition set. This is a kernel seam,
not a plugin protocol or host implementation. Exact acceptance commands are in
[`evidence/phase5-tool-plan-leases.md`](evidence/phase5-tool-plan-leases.md).

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

Development catalog commit `a8990e3f` adds topological concurrent
five-repository fast verification, an exact compatibility snapshot, and
vendor-only macOS amd64/arm64 compile proof; its `make verify` passed at 85.6%
coverage. Organization-profile commit `3ee0039d` records reusable workflow and
Actions-queue governance. macOS remains compile-only: real race, UI, process,
and runtime acceptance still requires a macOS runner.

## Completed evidence

- Organization governance/profile: `.github` `11e9470`.
- Development catalog/workspace: `36a3bf5`; `make verify` 98s, 85.5% coverage.
- Phase 0 catalog/governance follow-up: development `a8990e3f` (85.6% coverage,
  concurrent five-repository fast and macOS vendor compile), organization
  profile `3ee0039d`.
- Core foundation: `spice-agent` `218ffbb`.
- Professional quality baseline: `829dd0a`; `make verify` 27.6s, 87.7% coverage.
- Hardened deterministic kernel: `eaf1918`; `make verify` 23.9s, 88.4% coverage.
- Completed preview kernel: `841edd3`; `make verify` 29.9s, 86.2% coverage.
- Spice-native composition proof: `1f07284`; `make verify` 137.3s, 86.2% coverage.
- OpenAI provider final repin `88c3044`; `make verify` 29.25s, 89.3% coverage.
- Coding-tools final repin `653b405`; `make verify` 33.19s, 87.7% coverage.
- TUI foundation through `spice-agent-tui` `28a89dc`.
- Reference distribution through `spice-agent-coding` `2809a1a`.
- Generated Phase 3 architecture proof: `spice-agent-coding` `16244f5`;
  Windows and clean-clone WSL2 Linux full verification green at 86.0% coverage.
- Current Phase 3 distribution proof: `spice-agent-coding` `4cfd19a` (provider
  `4beed383`, coding tools `17cbef3b`);
  `make verify` 73.8s, 86.4% coverage.

Active follow-up slices are not recorded as completed evidence until their
repositories publish green commits.

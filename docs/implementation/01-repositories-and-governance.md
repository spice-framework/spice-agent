# Phase 0: Repositories and Governance

## Objective and prerequisites

Establish five independently versioned, public Apache-2.0 repositories with a
single generated development workspace and one compatibility catalog. This
phase depends only on organization administration and the immutable Spice
core/toolchain preview tag.

## Repository contracts

| Repository | Sole product ownership |
| --- | --- |
| `spice-agent` | contracts, kernel, annotations, protocols, plugin host, conformance |
| `spice-agent-provider-openai` | OpenAI Responses translation and configuration |
| `spice-agent-tools-coding` | read, atomic replacement/write, and process tools |
| `spice-agent-tui` | Bubble Tea shell and terminal presentation |
| `spice-agent-coding` | generated daemon/TUI targets and release distribution |

Each module uses `main`, direct-main single-writer delivery, exact Go 1.26.5,
committed vendor data, and bounded green commits. A committed `replace` is
forbidden. Pseudo-versions are allowed before preview tags only when they name an
immutable pushed commit and are recorded in the compatibility catalog.

The organization `.github` repository owns reusable verification and release
workflows, contribution templates, governance, security reporting, code of
conduct, release provenance verification, and the public organization profile.
Repository-local `AGENTS.md` and quality gates remain authoritative for local
delivery.

## Security and release governance

- Default branches and release tags have organization-owner protection. Tags
  are immutable after creation.
- Security advisories and private vulnerability reporting are enabled.
- Release environments restrict `v*` refs and require an organization owner.
- Releases include checksums, CycloneDX/SPDX-compatible SBOMs, keyless signing,
  provenance, and an independent verification path.
- Dependency review records maintenance, license, security, cancellation,
  observability, and replacement cost before a production dependency is added.
- Hosted workflows mirror local evidence. They do not authorize bypassing a
  failed local gate.

## Implementation slices

1. Create and configure all repositories and the organization profile.
2. Install licenses, governance, security policy, templates, exact tool pins,
   quality gates, vendor policy, and reusable workflows.
3. Register repositories and dependency order in the development catalog.
4. Generate `go.work` and editor workspace files without committing local
   replacement state into product modules.
5. Prove clean-clone module identity, isolated `GOWORK=off` tool-module checks,
   offline vendor testing, and concurrent dependency-ordered verification.
6. Record exact repository heads and gate evidence in the ledger.

## Exclusions

No product behavior, placeholder daemon executable, fake provider, or no-op TUI
is added to make this phase appear more complete. Repositories with no executable
product report that honestly and retain `null` compatibility pins until a real
contract is adopted.

## Verification

- Repository-name and module-path tests reject copied scaffolds.
- `go mod tidy -diff` and tools-module tidy run with workspace isolation.
- Vendor regeneration is byte-for-byte reproducible and offline tests use it.
- Vet, allowlisted lint, NilAway, gosec, govulncheck, shuffled/race tests, fuzz
  smoke where applicable, and coverage policy run through `make verify`.
- The catalog rejects cycles, missing repositories, incompatible Go/Spice pins,
  dirty modules, and dependency-order violations.
- GitHub repository settings, rulesets, release environments, and open security
  alerts are audited through the API and recorded as evidence.

## Performance and completion evidence

Per-repository `make fast` targets 30 seconds. Catalog fast verification runs
independent repositories concurrently after their dependencies are green.

Status is **complete for the preview**. All five public repositories have exact
canonical module identities, immutable sibling/Spice selections where
applicable, Go 1.26.5, committed reproducible vendor data, and no workspace
replacement directives. Exact-head hosted Windows/Linux/macOS verification,
vendor-offline execution, private vulnerability reporting, enabled Dependabot
security updates, and zero open Dependabot alerts close the repository
foundation. The schema-6 Development catalog and organization-owned reusable
workflow semantics are independently hosted green. Exact heads, runs, and
claim limits are recorded in
[`evidence/phase0-repository-foundation.md`](evidence/phase0-repository-foundation.md).
Later phase releases and product acceptance remain independent boundaries.

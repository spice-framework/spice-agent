# Phase 0 Repository Foundation Evidence

## Scope

This record closes the preview repository-foundation boundary. It does not
claim that later product phases, protected release workflows, or pre-1.0 API
stabilization are complete.

The five independently versioned public product repositories are:

| Repository | Audited contract head | Hosted evidence |
| --- | --- | --- |
| `spice-agent` | `3a9a7ce9749eb8b6b87c5717640f7fcaab5b22ee` | CI `31391946876`; Documentation `31391946135` |
| `spice-agent-provider-openai` | `6f8027b7946f6b35237c34d2b936920887431d72` | CI `31351725317`; Documentation `31351725325` |
| `spice-agent-tools-coding` | `4e386b644b75759d51367e72c55fc27137de473c` | CI `31351855282`; Documentation `31351855268` |
| `spice-agent-tui` | `37ded601f90f047659cd680bffce00071031ff60` | CI `31361794330`; Documentation `31361794358`; Dependency Graph `31361797395` |
| `spice-agent-coding` | `05c16d2e932416085ad489cd401a372661bb95b3` | CI `31349137633`; Documentation `31349137623` |

Every listed CI run is terminal green at the exact head. Together they execute
Go 1.26.5 on Windows, Linux, and macOS, reproduce committed vendor contents,
run offline vendor-only tests, and enforce the repository-specific formatting,
tidy, vet, lint, NilAway, gosec, govulncheck, shuffled/race, fuzz, generated
freshness, and coverage contracts. The distribution's protected preview 4
release run is waiting in Phase 6 and is not Phase 0 evidence.

## Module and repository identity

Each repository is public, uses `main`, reports Apache-2.0 through the GitHub
repository API, and declares its exact canonical module path. All five product
modules declare `go 1.26.0` plus `toolchain go1.26.5`; none contains a
`replace`, `exclude`, or `retract` directive.

The current Development schema-6 catalog at
`c69219eb33ec50b6c5ab4a99515cb28d38975990` lists all five modules as active,
preserves their dependency order, and records exact immutable Spice, Agent,
provider, tools, TUI, and Toolchain selections for each applicable release
profile. Its CI `31380968025` and Documentation `31380968001` are green.

The current Toolchain head
`bab8bcaf7d0c6311237b34812c681c3ee6a6593b` passed native Windows, Linux, and
macOS verification, all three arm64 cross-builds, independent library
cross-producer acceptance, and documentation in runs `31388852544`,
`31388852541`, and `31388852958`. The current organization-profile head
`f29b7ce16f8d220e87bfae54469057d001944b7b` passed exact reusable-workflow
semantic verification and documentation in runs `31382031771` and
`31382031890`.

## Security boundary

The GitHub API reports private vulnerability reporting enabled for all five
repositories, Dependabot security updates enabled, and zero open Dependabot
alerts at the audited heads. Repository-owned hosted gates independently report
no reachable vulnerability through `govulncheck` and retain the security and
dependency-review documents. Secret-scanning product features are not used as
a substitute for the repositories' deterministic source, generated-output,
event, manifest, and release secret-canary checks.

## Completion boundary

Phase 0 is complete for the preview: repository identity, governance,
development catalog and workspace model, exact toolchain, isolated module
graphs, committed vendor policy, cross-platform hosted verification, offline
execution, and vulnerable-manifest checks are all established.

This completion does not authorize a release, claim live OpenAI acceptance,
freeze the engine/plugin protocols, or close the native-terminal and external-
author work owned by later phases.

# Phase 6: Architecture-Proof Preview Release

## Objective and prerequisites

Publish coordinated `v0.1.0-preview.1` artifacts only after phases 0 through 5
prove the complete Spice-native architecture. Existing Spice core/toolchain
preview tags remain immutable inputs; every agent repository records exact
compatible sibling tags and protocol versions.

## Decisive workflow

From a clean installation, `spice-agent` starts or attaches to the user-local
daemon and opens the generated Bubble Tea client. One run streams an OpenAI
response, executes a compiled read/replace/shell tool through the canonical
dispatcher, executes a digest-pinned runtime-plugin tool, continues the model
turn, and renders the final response.

The same workflow demonstrates:

- deterministic strictly increasing events and exactly-once terminals;
- static DI fallback, ambiguity diagnostics, and application-owned typed
  resolution visible in generated Go;
- cancellation crossing TUI, daemon, provider, compiled tool/plugin, and
  process boundaries;
- disconnect/reconnect after an acknowledged sequence with bounded replay or
  explicit snapshot recovery;
- last-known-good development restart after an invalid edit;
- capability and bare-process privilege warnings before tool use;
- no secret material in logs, events, source maps, generated files, manifests,
  SBOMs, or archives.

## Release artifacts

The distribution publishes reproducible `-trimpath` archives for supported
Windows, Linux, and macOS/arm64-amd64 targets, checksums, SBOMs, keyless
signatures, provenance, licenses/notices, protocol descriptors, configuration
reference, and an independent verification script. The SDK repositories publish
module tags, GoDoc, conformance kits, compatibility manifests, and migration
notes.

## Implementation slices

1. Freeze the coordinated compatibility matrix and release candidate commits.
2. Execute clean-clone, isolated tools-module, offline vendor, generation,
   cross-repository, protocol, terminal, and live opt-in acceptance.
3. Baseline startup, connection, event, cancellation, generation, and build
   benchmarks; investigate any budget miss before release.
4. Build archives twice in independent paths and compare normalized hashes.
5. Generate/verify SBOM, signatures, provenance, and checksum bundles.
6. Install each archive into a clean profile and run the decisive workflow.
7. Create immutable preview tags only after evidence is linked in the ledger.

## Exclusions and support statement

This preview is an SDK architecture proof and reference application. It does
not promise compatibility stability, remote daemon support, a default sandbox,
automatic approval policy, persistence recovery, MCP, Git automation, indexing,
telemetry, planning, or production subagent orchestration. Release notes state
these limitations prominently.

## Preview migration note

The kernel snapshot wire format makes a deliberate pre-release hard cut from
`spice.agent.snapshot/v1alpha1` to `v1alpha2`. Old snapshots are rejected rather
than guessed or silently upgraded. `v1alpha2` replaces separate static/dynamic
fields with `PlanIdentity`, `ToolPlanID`, compiled executable-bean identities,
and an explicit generated snapshot-compatibility identity. Applications that
need cross-engine import must regenerate construction with that identity;
convenience/default engines remain local-resume and inspection only.

## Verification and evidence

All repository `make verify` gates run on exact release commits. The catalog
runs dependency-ordered full verification and rejects dirty worktrees or version
drift. Windows and Linux execute real end-to-end behavior; macOS performs native
archive smoke and protocol verification. The opt-in live OpenAI test records no
response content and publishes only redacted pass/fail/timing evidence.

Release evidence contains commit/tag/module hashes, Go/tool versions, generated
source ownership hashes, benchmark tables, supported OS/architecture matrix,
protocol compatibility output, terminal transcript hashes, security scans, SBOM
and signature verification, and known limitations.

Status is **planned**. No preview tag may be created from partial phase evidence.

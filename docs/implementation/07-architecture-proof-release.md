# Phase 6: Architecture-Proof Preview Release

## Objective and prerequisites

Publish the coordinated architecture proof only after phases 0 through 5 prove
the complete Spice-native architecture. The Agent core recovery is
`v0.1.0-preview.4`; TUI, provider, coding tools, and the distribution retain
their independently versioned `v0.1.0-preview.1` release identities. The
release graph must first replace every foundation and sibling pseudo-version
with an immutable preview tag. Every repository records the exact compatible
tags, commits, module graph, and protocol versions used to build it.

Component module tags are dependency inputs, not proof that this phase is
complete. A component may be tagged after its exact candidate passes local and
hosted gates plus the protected keyless module-release path. Phase 6 remains
incomplete until the final distribution is independently rebuilt, attested,
installed, and exercised through the decisive workflow.

The immutable `v0.1.0-preview.1`, `v0.1.0-preview.2`, and
`v0.1.0-preview.3` Spice Agent tags are failed release history, not published
components. Preview.1 failed before rendering; preview.2 failed during
independent-verifier cleanup; preview.3 passed candidate verification and
rendering but exposed disagreement between the separately reviewed release
policies before any verified handoff or release authority. The exact tags,
commits, failures, policy checks, and artifact boundaries are recorded in
[`evidence/phase6-release-history.md`](evidence/phase6-release-history.md).
Recovery advances the release identity; it never moves, deletes, or retries the
failed tag as a different candidate.

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

1. Publish a keyless Spice foundation preview that replaces the two historical
   post-preview pseudo-versions used by the Agent repositories. Repin every
   Agent module to that exact foundation tag.
2. Tag `spice-agent` and `spice-agent-tui` only after their exact candidate
   commits pass local, hosted, and protected keyless release verification.
3. Repin the provider and coding-tools modules to the released Agent core,
   verify them independently, and publish their component tags.
4. Repin the distribution to all four released sibling modules, regenerate
   vendor and Spice-owned output, and freeze the compatibility matrix.
5. Execute clean-clone, isolated tools-module, offline vendor, generation,
   cross-repository, protocol, terminal, and live opt-in acceptance.
6. Baseline startup, connection, event, cancellation, generation, and build
   benchmarks; investigate any budget miss before release.
7. Build archives twice in independent paths and compare normalized hashes.
8. Generate and independently verify SBOM, keyless provenance, and checksum
   bundles.
9. Install each archive into a clean profile and run the decisive workflow.
10. Create the immutable distribution tag only after all evidence is linked in
    the ledger. Publishing that final tag does not retroactively excuse a
    failed component or foundation release.

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
Phase 7 makes a second deliberate hard cut to `v1alpha3`, adding the workspace
SHA-256 to `PlanIdentity`. Portable import now refuses cross-workspace authority
before leasing a tool generation; v1alpha2 remains unsupported.

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

Status is **in progress**. Keyless component release callers exist, but the
foundation and distribution release paths, exact tagged dependency graph,
installed-platform proof, and decisive live workflow remain pending. Component
tags may be created only in the dependency order above; no distribution tag may
be created from partial phase evidence.

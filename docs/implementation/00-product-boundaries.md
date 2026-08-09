# Program Charter: Product Boundaries

## Objective

Deliver an SDK-first, Go-native agent platform whose compiled application graph
is ordinary generated Spice code. The architecture-proof milestone proves one
deterministic agent, one production provider, bounded coding tools, a local
daemon, a terminal client, and language-neutral runtime tools. It is not a
claim that the reference distribution is already a complete daily-use coding
product.

## Fixed decisions

- The five public modules live under `github.com/spice-framework` and use the
  Apache-2.0 license.
- Go source declares static composition. Spice validates it and emits committed,
  inspectable Go; no runtime container or `RuntimeGraph` is permitted.
- Go 1.26.5 is the only development toolchain. Product modules declare
  `go 1.26.0` plus `toolchain go1.26.5` and commit vendor contents.
- The first model integration is OpenAI Responses. The first terminal is Bubble
  Tea v2. Process boundaries use Protobuf and gRPC only where a real process
  boundary exists.
- The first daemon release is authenticated and user-local. Remote listeners,
  network daemon discovery, and silent version reuse are excluded.
- Coding tools intentionally have the current user's privileges. Capability
  declarations and prominent warnings describe that trust boundary; they do
  not pretend to be a sandbox.
- Public contracts remain pre-1.0 until the stress prototypes and external
  extension proofs in phases 7 and 8 complete.

## Ownership boundaries

The core repository owns provider-neutral values, the deterministic kernel,
events, snapshots, public annotations, protocols, runtime-plugin hosting, and
conformance kits. It does not own an OpenAI adapter, coding tool implementation,
terminal rendering, persistence backend, permission policy, or distribution
defaults.

The provider, coding-tools, and TUI repositories implement those independent
ports. The distribution repository owns two generated Spice applications and
their release/install experience. A repository may depend only toward the core
contracts and never back into the distribution.

## Global invariants

1. Static dependencies are constructor-injected by generated Spice code.
2. Dynamic runtime tools are leased process generations; they never mutate the
   static bean graph.
3. Every started lifecycle has exactly one terminal event and committed event
   sequence numbers are never reused.
4. Values crossing package or process boundaries are validated, immutable,
   bounded by count and encoded bytes, and safe to log only when documented.
5. Credentials, authorization headers, prompt bodies, tool payloads, and
   environment secrets never enter generated code, compatibility manifests,
   provenance, or ordinary event metadata.
6. Cancellation propagates across provider, dispatcher, process, daemon, and
   client boundaries. A component that ignores context is treated as an
   explicitly trusted defect; the kernel cannot forcibly stop it.
7. Normal verification and editor analysis do not download modules or install
   tools. Standalone module verification must not inherit a masking `go.work`.

## Explicit exclusions for the architecture proof

Persistence, approval policy, sandboxing, MCP, Git automation, indexing, LSP,
telemetry exporters, compaction, planning, and subagent scheduling are extension
work. They must not leak provisional concepts into the kernel. Remote daemon
access is excluded until there is a separate TLS, authorization, approval
routing, and threat-model RFC.

## Program acceptance

- A clean Windows or Linux workstation can clone the catalog, create the
  generated workspace, and verify every module offline after dependency
  preparation.
- The generated daemon and terminal applications execute a streamed model turn,
  a compiled tool, and a runtime-plugin tool with deterministic events and
  complete cooperative cancellation.
- Generated Go identifies every selected implementation and contains no manual
  edits, reflection lookup, runtime registry, or hidden package scan.
- Release archives are reproducible, signed, checksummed, accompanied by SBOMs,
  and independently verified.

## Performance budgets

Warm affected-package verification targets 30 seconds and repository checks two
minutes. Provisional product budgets are embedded startup below 100 ms, daemon
startup below 250 ms, warm local client connection below 75 ms, local event
delivery p95 below 10 ms, and cooperative cancellation propagation p95 below
50 ms. Model latency is measured separately.

Core now owns a bounded single-CPU `make benchmark` command for engine
construction, text execution, one compiled tool round, and cooperative
cancellation. Its first Windows baseline and material-regression policy are
recorded in
[`phase7-kernel-runtime-baseline.md`](evidence/phase7-kernel-runtime-baseline.md).
These kernel measurements do not replace installed daemon, client, plugin, or
TUI latency evidence.

## Evidence and status

Status is **in progress**. Repository creation and the hardened kernel are
implemented; the ledger `README.md` records exact commits and commands. This
charter changes only through a reviewed RFC/ADR update because every later phase
depends on these boundaries.

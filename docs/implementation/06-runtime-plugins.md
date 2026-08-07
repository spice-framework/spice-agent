# Phase 5: Runtime Plugins and Developer Loop

## Objective and prerequisites

Support genuinely dynamic runtime tools without mutating compiled Spice DI.
This phase depends on stable run plan snapshots, daemon negotiation, canonical
tool dispatch, and cancellation semantics.

The Phase 5.0A prerequisite is complete in core: all tool definitions carry
effect/replay metadata and all execution failures use the correlated typed
outcome contract. The plugin host must translate protocol failures into that
same contract; it may not introduce a parallel error or retry model.

The Phase 5.0B kernel seam is also complete: `ToolPlanSource` leases current or
exact immutable plans, `Run.PlanIdentity` records the combined compiled/tool
identity, resume fails closed on substitution, and release is exactly-once and
terminal-authoritative. A source contractually performs only a non-blocking
reference decrement during release; generation drain remains asynchronous and
source-owned. This slice deliberately contains no plugin protocol,
process host, daemon integration, or activation manager.

The initial Phase 5.1 `plugin/v1` runtime-tool wire contract is frozen. It
defines authenticated protocol/build/limit/manifest initialization, immutable
tool definitions converted to the kernel's existing `tool.Definition`, one
contiguous normally completed Execute server stream with exactly one terminal
result or correlated typed failure, and session-bound Drain/Shutdown lifecycle.
The launch secret is never serialized; HMAC-SHA256 authenticates the
deterministic complete handshake transcript. Handwritten validators count
unknown fields toward bounds, preserve them on wire round trips, and fail closed
on duplicate tools, unknown enums/capabilities, correlation or sequence
mismatch, oversized JSON,
missing terminals, and post-terminal frames. A reusable black-box conformance
harness and independent Go fixture process prove bounded private-stdin
bootstrap, exact stdout readiness with no contamination, current-user local
gRPC, authenticated initialization, immutable manifest, success and
typed-failure streams, unknown fields, malformed/oversized input, transport
cancellation, Drain, and Shutdown. This is test infrastructure, not a process
host, digest verifier, or generation activation manager. The independent
Python 3.12+ fixture uses locked `grpcio`
and Protobuf dependencies and passes the same public harness over an absolute
AF_UNIX socket on Windows, Linux, and macOS. This fixture-specific Windows
choice does not change the production named-pipe transport.

The first production-host security foundation is also implemented. Public
bootstrap/readiness framing is shared by fixtures and production; immutable
executable configuration has no interpreter arguments or ambient environment;
and a held, platform-identified file is digest-verified before launch and must
be identity/digest-rechecked immediately afterward. Bounded stdout readiness
and stderr drains never reflect child-controlled content. The pathname-based
`process.Launcher` cannot universally eliminate the verify-to-exec race, so the
post-launch check detects it and activation fails closed. Process activation,
local IPC, authenticated initialization, remote tool proxies, generations, and
cleanup remain pending. Evidence is in
[`evidence/phase5-host-security-foundation.md`](evidence/phase5-host-security-foundation.md).

## Runtime-plugin contracts

- One host process and one local gRPC connection exist per plugin generation.
- Configuration names an absolute executable path and pinned SHA-256 digest.
  Relative paths, PATH lookup, digest drift, and ambient plugin discovery fail.
- Every launch receives a cryptographically random handshake secret. Candidate
  manifest, protocol, digest, identity, capabilities, and bounds are verified
  before it may serve a future run.
- Activation is atomic for future runs. Existing runs retain a lease on their
  original immutable generation until terminal finalization.
- Failed candidates never alter the active generation. Old generations drain
  within a bound and are forcibly terminated only with explicit uncertainty
  reporting.
- Startup, message, stderr, call, cancellation, restart, drain, and shutdown
  are count/time/byte bounded. Crash replay is never automatic for a mutating
  tool whose outcome is uncertain.
- Initial `plugin/v1` exposes tools only. Providers and portable views require
  additive protocol revisions; compiled stages and executable UI code are
  permanently excluded.

## Cross-language conformance

Independent Go and Python 3.12+ fixtures implement the same public protocol. The
Python fixture uses locked `grpcio` and Protobuf dependencies, starts from a
clean environment, and passes the host-owned conformance suite without importing
private Go implementation details.

## Developer-loop contracts

`spice dev` watches daemon and terminal targets, debounces deterministic
generation/builds, restarts them independently, and preserves the last-known-good
process when a new generation fails. Diagnostics identify source locations and
the active/failed generation; no partial generated tree is activated.

## Implementation slices

1. Freeze `plugin/v1` with Buf lint/breaking checks. **Complete.**
2. Implement manifest/digest verification and the fallback direct launcher.
   **Digest/configuration foundation complete; launch and manifest validation pending.**
3. Add generation manager, candidate handshake, atomic activation, leases,
   bounded stderr, restart policy, drain, and cleanup.
4. Provide optional Spice auto-configuration that decorates/injects the dynamic
   tool source through normal static DI.
5. Ship Go and Python echo/filesystem-neutral fixture tools and conformance CLI.
   **Complete for the initial runtime-tool profile.**
6. Integrate last-known-good `spice dev` for generated daemon/TUI targets.

## Exclusions

Plugins are trusted native processes, not sandboxes. Capability declarations
are input to later policy; they do not enforce security. Plugins cannot alter
static DI, register compiled stages, contribute native widgets, listen remotely,
or trigger hidden downloads/installations.

## Verification

- Tests cover wrong digest, path swap, handshake secret, old/new protocol,
  malformed/oversized messages, stdout contamination, bounded stderr, timeout,
  cancellation, and process-tree cleanup.
- Crash scenarios include handshake, call, activation, drain, and shutdown.
  Candidate failures preserve the active generation; leased runs keep the old
  generation; uncertain mutating calls are never replayed.
- Conformance executes identical cases against Go and Python on Windows and
  Linux, including cancellation and unknown fields.
- Developer-loop tests change valid/invalid sources rapidly, prove debounce,
  byte-identical generation, independent restart, diagnostic stability, and
  last-known-good continuity.

## Performance and completion evidence

Plugin handshake and activation budgets are baselined separately from tool work.
Local call overhead targets the daemon event-latency budget and must not add an
unbounded queue. Evidence includes digests, generation/lease timelines, process
logs, conformance versions, and failure-injection results.

Status is **in progress**. The execution-outcome and kernel plan-lease
prerequisites, initial runtime-tool protocol, reusable conformance harness, and
independent Go/Python fixtures are implemented. Process/digest ownership,
generation management, and the developer loop remain pending.

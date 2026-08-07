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
post-launch check detects it and activation fails closed.

The host now also derives caller-owned current-user local endpoints without
listening or discovery, launches one authenticated candidate with exact private
bootstrap and stdout readiness, validates the negotiated manifest and approved
capabilities, and translates one initialized session into bounded remote tool
implementations. Every acquired resource remains caller-owned after a failed
launch. Cleanup releases the endpoint and verification lease only after process
containment is proved. Remote execution never retries and maps any possibly
started mutating operation without a valid terminal result to an uncertain,
non-retryable kernel failure. A complete immutable desired `Set` supplies the
atomic-activation input. `plugin/host.Host` now stages and validates a complete
set invisibly, rejects all tool-name collisions, applies ordered generated
decorators to the complete compiled/runtime merge, and publishes one immutable
generation. Exact run leases survive later activation. The final retired lease
closes reacquisition before asynchronous reverse cleanup; a current candidate
crash fails new leasing closed instead of substituting an old or compiled-only
graph. Accepted candidates close local admission, join active calls, perform
validated bounded Drain and Shutdown, allow bounded graceful process exit, and
retain endpoint/executable ownership until containment is proved. Host shutdown
joins staging and leases and supports explicit ownership-cleanup retry. Bounded
whole-set recovery and passive public health are now implemented. A zero
`RestartPolicy` disables recovery; the default policy is an explicit
application/distribution choice, not a side effect of blank-importing
auto-configuration. Attempt one starts immediately, later attempts use bounded
exponential backoff, and every attempt has its own deadline. A successful
explicit activation retains a defensive clone of the complete desired `Set`.
Only a recovery whose current generation pointer/identity, desired revision,
and explicit-activation revision still match may atomically publish a distinct
generation. A newer explicit activation cancels or stales recovery, while a
failed explicit replacement re-arms the last successful desired set. Recovery
never redirects a lease or replays a call. `Health` performs no plugin callback
or transition and emits only fixed secret-safe state/issue values and ownership
counts. Optional leaf auto-configuration now
contributes the compiled dispatcher, current-user endpoint factory, concrete
Host with generated cleanup, and exact `stage.ToolPlanSource` adapter as
ordinary named fallback beans. Evidence is in
[`evidence/phase5-host-security-foundation.md`](evidence/phase5-host-security-foundation.md)
and
[`evidence/phase5-candidate-and-remote-tools.md`](evidence/phase5-candidate-and-remote-tools.md),
with generation/lifecycle evidence in
[`evidence/phase5-generations-and-lifecycle.md`](evidence/phase5-generations-and-lifecycle.md)
and recovery/health evidence in
[`evidence/phase5-recovery-and-health.md`](evidence/phase5-recovery-and-health.md)
and auto-configuration evidence in
[`evidence/phase5-runtime-autoconfiguration.md`](evidence/phase5-runtime-autoconfiguration.md).

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
   **Complete through authenticated candidate launch and manifest validation;
   public fallback source composition remains in slice 4.**
3. Add generation manager, candidate handshake, atomic activation, leases,
   bounded stderr, restart policy, drain, and cleanup. **Complete in core,
   including bounded whole-set recovery and public health.**
4. Provide optional Spice auto-configuration that decorates/injects the dynamic
   tool source through normal static DI. **Complete through the core adapter,
   generated reference-distribution Host cutover, explicit typed Set
   construction, pre-publication activation, fixed-code health, and generated
   bounded cleanup.**
5. Ship Go and Python echo/filesystem-neutral fixture tools and conformance CLI.
   **Complete for the initial runtime-tool profile.**
6. Integrate last-known-good `spice dev` for generated daemon/TUI targets.
   **Package-main targets and independent supervisors are complete. A real
   vendored-CLI fixture proves invalid-edit diagnostics, unchanged
   last-known-good identity, deterministic debounce, graceful replacement,
   process containment, and byte-identical source/generated restoration. The
   simultaneous installed daemon/TUI independent-restart and Bubble Tea
   reconnect workflow remains pending.**

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
prerequisites, initial runtime-tool protocol, reusable conformance harness,
independent Go/Python fixtures, digest/process ownership, local candidate
handshake, and remote execution translation are implemented. Atomic generation
management, exact run leases, graceful lifecycle, bounded recovery, and public
health are implemented. The generated distribution Host cutover and
explicit plugin configuration/activation, real process
activation/cancellation acceptance, package-main development supervisors, and
one real last-known-good developer loop are implemented. Only the simultaneous
installed daemon/TUI fault, independent restart, and Bubble Tea reconnect proof
remains for the Phase 5 developer-loop boundary.

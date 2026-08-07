# Phase 4: Local Daemon and Bubble Tea TUI

## Objective and prerequisites

Expose the deterministic engine through authenticated user-local IPC and a
separate generated terminal application. This phase starts only after snapshots,
interaction lifecycles, replay cursors, and provider/tool vertical behavior are
stable enough to encode in a versioned protocol.

## Engine protocol contracts

`common/v1` and `engine/v1` are Protobuf APIs governed by Buf lint and breaking
checks. They provide initialization and version/capability negotiation,
server-owned definitions, stable-owner reconnect CAS, health, run creation,
atomic paged event replay/tailing, complete pending-interaction snapshots with
revisioned deltas, cancellation, interaction responses, suspend/resume, safe
snapshot import, and bounded replay diagnostics. Authentication is transport
metadata, never an application payload.

Protocol minor 1 makes snapshot transfer authenticated and atomic with the
`snapshot-authority-v1` capability. Signed construction and keyed import
verification are mandatory. Minor-0 connections receive neither snapshot
capability; unkeyed SHA-256 validation is payload integrity, not authority.
Protocol minor 2 replaces the provisional interaction replay limit with one
complete current snapshot and captured control followed, when requested, by a
bounded live tail. Minor-0 and minor-1 peers cannot invoke that revised stream.
Minor-2 initialization also requires server-sized message and collection
bounds so every state admitted by the host remains reconnectable in one frame.
Minor 3 adds one caller-owned 128-bit initialization attempt identity and exact
committed-success replay. Legacy 1.0 through 1.2 requests remain non-retryable,
and no request range may cross the 1.2/1.3 semantic boundary.

Every request has count, byte, and deadline limits. Unknown fields follow the
documented additive-compatibility rule. Protocol errors distinguish invalid
argument, unauthenticated, incompatible version, out-of-range cursor, resource
exhaustion, unavailable, cancellation, and uncertain operation state.

## Local transport and managed startup

- Linux/macOS use a current-user Unix socket; Windows uses a current-user named
  pipe. Remote TCP listening is absent, not merely disabled by default.
- Endpoint metadata and random authentication tokens use user-only permissions
  and are validated against the current user before connection.
- The library boundary can attach to a compatible protected endpoint or own one
  candidate launched after a bounded startup lock and health handshake. The
  distribution commands that exercise it remain pending.
- Distribution launchers implement the public `process.Launcher` contract.
  Root outcomes and containment joins remain separate, and core does not claim
  that every supported operating system supplies universal descendant
  containment.
- The distribution will expose `spice-agentd serve`; `spice-agent attach` will
  connect to an explicitly selected protected local endpoint.
- An incompatible daemon is rejected with its observed/required versions and a
  safe remediation command. It is never silently killed or reused.
- Last acknowledged event sequence drives reconnect. Cursor gaps require an
  explicit snapshot recovery path; no event is silently omitted.

## TUI contracts

The `spice-agent-tui` repository owns only Bubble Tea presentation, UI-neutral
render values, editor/input translation, commands, key bindings, accessibility,
status, and theme. It consumes a high-level client/session port and may not
import kernel internals, generated gRPC packages, daemon hosting, or OS IPC.

Shell, renderers, command set, prompt editor, key bindings, workspace view,
status bar, and theme are generated Spice beans. Runtime plugins may emit
portable semantic views and namespaced data; they never load executable UI code.

## Implementation slices

1. Freeze common/engine schemas and generate deterministic Go code.
2. Implement the transport-independent host primitives and typed run lifecycle,
   then authenticated local listeners, client/session translation,
   negotiation, replay, cancellation, interaction streams, snapshot, and
   health translation.
3. Add user-scoped endpoint discovery, explicit-attach policy, and ownership-safe
   managed candidate coordination; then supply the distribution process starter.
4. Implement the Bubble Tea shell with injected presentation components and
   terminal-size-independent semantic models.
5. Generate separate daemon and terminal `@Application` targets in the
   distribution; preserve args, environment, working directory, and cleanup.
6. Add reconnect, resize, interruption, and clean-shutdown acceptance on Windows
   and Linux.

## Exclusions

Remote access, TLS, multi-user hosting, browser UI, persistence policy, plugin
executable views, and automatic daemon upgrades are excluded. macOS receives
compile/protocol coverage until a stable terminal-interaction runner is available.

## Verification

- Buf lint/breaking checks and deterministic generation run offline.
- Protocol tests cover old/new peers, unknown fields, authentication, overload,
  cursor replay/gap, stale clients, half-close, cancellation, and malformed data.
- OS tests prove socket/pipe ownership, token permissions, stale endpoint cleanup,
  concurrent managed startup, version rejection, and process cleanup.
- TUI golden tests cover multiple dimensions, wrapping, Unicode width, light/dark
  themes, accessibility text, resize, reconnect, and bounded history.
- Real terminal tests exercise one-command start, explicit serve/attach,
  interactions, cancellation, reconnect, and Ctrl-C shutdown.

## Performance and completion evidence

Daemon startup targets 250 ms, warm connection 75 ms, and local event delivery
p95 10 ms. Evidence records OS/build, endpoint type, handshake timing, replay
cursor, terminal transcript, and generated target source map.

Status is **in progress**. Slice 1 is implemented: provisional schemas,
generated Go, handwritten boundary validation, compatibility/freshness gate,
protocol tests, and fuzz smoke are green. This is intentionally not a daemon.
The transport-independent foundation of slice 2 is also implemented: immutable
server definitions, root-owned reconnect CAS sessions, bounded per-client
idempotency, and a stable-client-partitioned pending-interaction hub with
explicit run-binding leases, independent complete-first revisions, reconnect
fencing, global/per-client retention budgets, and joined observer shutdown. It
adds no listener or RPC adapter. Session ownership now has a bounded commit and
stream gate: reconnect takes priority over queued old-epoch mutations, drains
the active commit, cancels and joins every old stream, then alone advances the
epoch. Mutation, reconnect, and stream-acquisition queues are bounded and
participate in shutdown drain accounting. The OS-backed run authority now
persists a distinct current-user scope and HMAC key, holds a stable per-run OS
lock, and
drives signed `ACTIVE`, `SUSPENDED`, `IMPORTING`, and terminal records through
an explicit prepare/consume/activate transaction. Authority-key generation and
local run-transition generation are deliberately separate. A consumed import
cannot be retried after an uncertain failure. Its retained OS directory
identity makes lock and state operations immune to pathname substitution, and
full-ancestry trust validation prevents cross-lifetime rollback by another
unprivileged principal. Suspended export retains ownership; authority resume
durably invalidates the old snapshot before kernel execution resumes. Its
explicit close/drain lifecycle is ready for generated singleton cleanup.
The kernel now also exposes
transactional prepared start/resume handles: preparation yields a stable run ID
and exact leased plan without registration or execution, then commit accepts
the separately owned run root. This is the authority-acquisition seam, not an
authority implementation.
Prepared starts and imported resumes additionally support an inert registered
state. `CommitPaused` reserves the engine identity and leased plan while
withholding all events and extension work; the host may durably activate
authority and only then release execution through an exactly-once gate. Root
cancellation cannot guess that external decision, and abort performs bounded
zero-event cleanup.
Locally suspended runs additionally expose an inert prepared-resume boundary.
The host can reserve the exact next event sequence, durably invalidate the old
snapshot through `RunAuthority`, and only then release kernel execution.
Cancellation and shutdown latch behind that decision; abort restores the exact
suspended snapshot.
The transport-independent `daemon.RunHost` now closes the lifecycle-composition
part of slice 2. It owns typed Start, Import, Suspend, Resume, Cancel, Respond,
Export, Health, and Shutdown behavior using the generated `DefinitionSet`,
kernel, run authority, stable-client sessions, idempotency ledger, and pending
interaction hub. Start and import bind ownership and register an inert kernel
run before taking the session mutation-commit gate; durable `ACTIVE` authority
always precedes kernel activation. Suspend reaches a kernel safe boundary
before issuing an authority envelope, and resume durably invalidates that
envelope before work continues. Accepted pending interactions remain
respondable through terminal drain.

The host abandons an idempotency entry only for a failure proved to precede any
visible or durable boundary. Committed errors and uncertain transitions remain
stable outcomes. Typed stale-session and host/session-gate capacity facts are
encoded in those bounded outcomes and survive replay or abandonment; malformed
or legacy details fail closed to their stable public error class. Active reservations are separately bounded from a
count-and-byte-bounded terminal-envelope cache, whose eviction follows terminal
completion order. Ownership lookup does not distinguish unknown runs from runs
owned by another client. Health reports immutable configured limits and fixed
secret-safe degraded reasons. Shutdown synchronously fences admission, aborts
inert candidates, cancels kernel work, drains accepted operations and pending
bindings, joins finalizers, and closes authority last; cleanup continues even
if an individual caller stops waiting.

`RunHost.Describe` provides a validated sessionless initialization snapshot of
the immutable generated definitions and readiness facts observed at one host
synchronization boundary. It honors cancellation without creating session
state. The session-bound Health path checks ownership first and then uses the
same readiness snapshot implementation.

This lifecycle core does not include gRPC, Protobuf/RPC translation, event or
interaction stream delivery, endpoint authentication, Unix sockets, Windows
named pipes, endpoint discovery, or managed daemon startup. Although RunHost
bounds retained terminal envelopes, `agent.Engine` still retains unbounded
service-lifetime seen-run identity tombstones. That limitation is explicit and
must be resolved or accepted by a bounded-lifetime daemon policy before the
Phase 4 contract freezes.

The endpoint-authentication prerequisite now lives in the separate
`daemon/grpcserver` package. It generates opaque 256-bit credentials, accepts
only canonical unpadded base64url values carried as one exact `Bearer` metadata
entry, compares fixed-size values in constant time, and marks authenticated
handler contexts privately. Unary and streaming middleware reject before
application handling and never echo credential material through statuses,
headers, trailers, formatting, structured logs, or JSON. The middleware factory
is intentionally private. `grpcserver.NewServer` installs both paths with global
receive/send bounds and registers the generated engine service atomically. A
bounded private registry stores exact daemon sessions and cloned validated
negotiation contracts. Authenticated Initialize and Health now run over real
gRPC: preflight precedes ownership allocation, reconnect is an exact epoch CAS,
and Health rechecks both registry and SessionStore ownership before reaching the
host. Authenticated Start, Cancel, Respond, Suspend, Resume, Export, and Import
now cross the same ownership boundary. Requests are checked against the hard
server bound before lookup and against the exact negotiated connection limits
before RunHost. Application failures are fixed typed statuses; cancellation and
deadlines alone remain gRPC errors. Start deliberately supports one user text
part until the standard-library client contract grows richer input types.
Snapshot transfer requires minor 1 plus `snapshots` and
`snapshot-authority-v1`; the adapter validates and serializes import structure
but never verifies its HMAC. Authenticated event and interaction streams now
consume RunHost-owned observations, prevalidate complete pages before
disclosure, preserve typed replay/overload recovery, and retain reconnect
fences until their senders exit. Bounded server shutdown cancels adapter-owned
observations and force-stops gRPC at the caller deadline. This slice has no OS listener, endpoint
metadata file, discovery, or public client adapter; those remain pending.

The following local-client slice supplies those previously excluded pieces.
`daemon/endpoint` owns strict current-user metadata, stable publication and
startup locks, liveness probing, and stale cleanup. `daemon/localipc` supports
only private Unix sockets and current-user Windows named pipes. The public gRPC
adapter translates and validates the complete session protocol, and the managed
adapter launches only after exact proven absence. Their local bridge owns each
channel and disables generic transport retry and every nonlocal resolution
path. The protocol adapter alone performs the one bounded, byte-identical 1.3
initialization retry. See
[`evidence/phase4-local-client.md`](evidence/phase4-local-client.md).

The current-user lifecycle follow-up now binds the default runtime directory,
transport, and address as one validated `endpoint.UserScope`. Explicit attach
resolves an exact requested address only through protected active metadata and
can never return the absence sentinel that authorizes launch. Managed startup
now retains the exact `Candidate` returned by its starter, detects early exit,
shuts down and joins failed or canceled launches, serializes concurrent
initialization, and stops only its own candidate. An incomplete bounded join
retains ownership for a later shutdown attempt. The generic candidate contract
is implemented; an `os/exec` distribution starter and command wiring are not.
Evidence is in
[`evidence/phase4-managed-local-lifecycle.md`](evidence/phase4-managed-local-lifecycle.md).

Protocol-1.3 initialization now adds caller-owned attempt identities, bounded
server coalescing, atomic committed-response retention, and one exact client
retry while preserving legacy uncertainty. See
[`evidence/phase4-initialization-replay.md`](evidence/phase4-initialization-replay.md).

The lifecycle-adapter prerequisite now separates snapshot-import structure
from authority. `ValidateImportSnapshotRequestStructure` checks the complete
client mutation, negotiated encoded size (including compatible unknown fields),
snapshot envelope and digest, authority shape, and suspended lifecycle. It does
not verify the HMAC. It rejects unsigned root-envelope extensions and enforces
the complete opaque-envelope bound before translation. The adapter must call only this untrusted-input check before
translation; `RunHost.Import` remains the sole keyed authority and mutation
boundary.
The TUI composition half of slice 4 is implemented independently at
`spice-agent-tui` commit `82adb45`: public APIs contain no Bubble Tea or daemon
types, Spice generates the renderer/theme/binding/I/O/shell graph, cancellation
has an independent control lane, and external acceptance executes the actual
injected terminal shell through explicit application start and stop. Its full
gate passed in 158.4 seconds at 90.1% product coverage. The distribution now
pins the later TUI commit `a0d4824` and generates separate daemon and terminal
applications at `spice-agent-coding` commit `8f92368`. Managed attach-or-start,
explicit attach, the protocol-1.3 session bridge, injected native process
containment, deterministic source mapping, and I/O-lazy `--check` composition
are implemented. Real installed-terminal Windows/Linux reconnect interaction
and release packaging remain pending. See
[`evidence/phase4-protocol.md`](evidence/phase4-protocol.md).
Foundation-specific evidence is in
[`evidence/phase4-host-foundation.md`](evidence/phase4-host-foundation.md).
Run-authority evidence is in
[`evidence/phase4-run-authority.md`](evidence/phase4-run-authority.md).
Kernel preparation evidence is in
[`evidence/phase4-kernel-preparation.md`](evidence/phase4-kernel-preparation.md).
Local-resume evidence is in
[`evidence/phase4-kernel-local-resume.md`](evidence/phase4-kernel-local-resume.md).
The standard-library-only public client contract is recorded in
[`evidence/phase4-client-contract.md`](evidence/phase4-client-contract.md).
Session gate evidence is in
[`evidence/phase4-session-gates.md`](evidence/phase4-session-gates.md).
Protocol prerequisite evidence is in
[`evidence/phase4-protocol-prerequisites.md`](evidence/phase4-protocol-prerequisites.md).
Kernel activation-gate evidence is in
[`evidence/phase4-kernel-activation-gate.md`](evidence/phase4-kernel-activation-gate.md).
RunHost lifecycle-core evidence is in
[`evidence/phase4-run-host.md`](evidence/phase4-run-host.md).
RunHost description evidence is in
[`evidence/phase4-run-host-description.md`](evidence/phase4-run-host-description.md).
Endpoint-authentication prerequisite evidence is in
[`evidence/phase4-grpc-authentication.md`](evidence/phase4-grpc-authentication.md).
Initialize/Health adapter evidence is in
[`evidence/phase4-initialize-health.md`](evidence/phase4-initialize-health.md).
Lifecycle unary adapter evidence is in
[`evidence/phase4-lifecycle-unary.md`](evidence/phase4-lifecycle-unary.md).
Authenticated streaming adapter evidence is in
[`evidence/phase4-streaming-rpc.md`](evidence/phase4-streaming-rpc.md).
Current-user scope, explicit-attach, and managed-candidate evidence is in
[`evidence/phase4-managed-local-lifecycle.md`](evidence/phase4-managed-local-lifecycle.md).

The baseline remains intentionally provisional. The host and authenticated
local-client libraries plus both generated distribution targets prove protocol,
authority, IPC, explicit attach, managed startup, and owned process containment.
Before the final Phase 4 freeze, installed Windows/Linux terminals must still
prove one-command startup, explicit serve/attach, resize, reconnect, and clean
shutdown, and the engine's service-lifetime run-identity tombstones must be
resolved or formally bounded. Exact distribution evidence is in
[`evidence/phase4-distribution-targets.md`](evidence/phase4-distribution-targets.md).

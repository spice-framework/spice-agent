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

Every request has count, byte, and deadline limits. Unknown fields follow the
documented additive-compatibility rule. Protocol errors distinguish invalid
argument, unauthenticated, incompatible version, out-of-range cursor, resource
exhaustion, unavailable, cancellation, and uncertain operation state.

## Local transport and managed startup

- Linux/macOS use a current-user Unix socket; Windows uses a current-user named
  pipe. Remote TCP listening is absent, not merely disabled by default.
- Endpoint metadata and random authentication tokens use user-only permissions
  and are validated against the current user before connection.
- `spice-agent` attaches to a compatible user daemon or starts one, with a
  bounded startup lock and health handshake.
- `spice-agentd serve` explicitly hosts the daemon. `spice-agent attach`
  connects to an explicitly selected local endpoint.
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
2. Implement the transport-independent host primitives, then authenticated
   local listeners, client/session translation, negotiation, replay,
   cancellation, interaction, snapshot, and health.
3. Add user-scoped endpoint discovery and managed start coordination.
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
idempotency, and an atomic complete-first pending-interaction broker. It adds no
listener or RPC adapter. The kernel now also exposes transactional prepared
start/resume handles: preparation yields a stable run ID and exact leased plan
without registration or execution, then commit accepts the separately owned run
root. This is the authority-acquisition seam, not an authority implementation.
The remainder of slices 2 through 6 stays pending,
including OS transport, authentication, run hosting/translation, managed
startup, Bubble Tea behavior, and real Windows/Linux reconnect acceptance. See
[`evidence/phase4-protocol.md`](evidence/phase4-protocol.md).
Foundation-specific evidence is in
[`evidence/phase4-host-foundation.md`](evidence/phase4-host-foundation.md).
Kernel preparation evidence is in
[`evidence/phase4-kernel-preparation.md`](evidence/phase4-kernel-preparation.md).

The baseline remains intentionally provisional. The pre-host repair closes the
schema and kernel seams for interaction discovery/run identity, reconnect CAS,
suspend/resume/import identity, authenticated snapshot envelopes, and atomic
replay bounds. Before the final Phase 4 freeze, the daemon host must prove them
over real RPCs, enforce run tombstones and OS-backed authority-key lifecycle,
and separate RPC contexts from run lifetime. Buf protects
changes against the committed baseline; it does not imply those host semantics
are already implemented.

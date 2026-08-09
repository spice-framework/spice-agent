# Phase 4 Protocol Foundation Evidence

This evidence covers only the provisional repository-owned process schema and boundary
validation slice. It does not claim a daemon server, local IPC implementation,
client lifecycle, managed startup, or TUI.

The product module pins gRPC `v1.83.0` and Protobuf `v1.36.11`. The isolated
tools module pins Buf `v1.72.0`, Protobuf generation `v1.36.11`, and gRPC Go
generation `v1.6.2`. All generated Go is committed. Normal commands use the
vendor tree and `GOPROXY=off`.

| Contract | Executable evidence |
| --- | --- |
| Schema style and compatibility | `buf lint .` and `buf breaking . --against schema-baseline` through `make check` |
| Deterministic generation | `make proto`; `make check` rerenders to a temporary tree with the exact selected Go binary and compares every `*.pb.go` byte |
| Negotiation | `common/v1` tests cover the 1.0-1.2 range, greatest-compatible selection, old/new peers, malformed ranges, capabilities, bounds, health, and typed status details |
| Engine behavior | `engine/v1` tests cover server definitions, reconnect CAS, run validation, ordered replay controls, minor-2 complete-snapshot/control-first interaction streams, complete response-envelope bounds, stateful contiguous live-delta membership validation, reconnectable aggregate-state limits, cancellation, suspend/resume, authenticated stable snapshot identity, exact reservations, and service shape |
| Snapshot authority | Golden-vector and tamper tests cover canonical domain-separated HMAC input, exact scope/generation/tag structure, signed-only construction, keyed-only import, secret-safe panic/failure containment, cancellation, defensive copies, capability gating, descriptors, and unknown fields |
| Kernel replay | `event` tests cover empty initial/imported bounds, bounded pages, final-head tail registration, eviction, stale/future recovery, and slow-tail recovery without gaps or duplicates |
| Interaction scope | `interaction` and `agent` tests prove every broker request receives its validated owning run ID |
| Additive compatibility | Protobuf unknown fields survive unmarshal/remarshal in common and engine envelopes |
| Robust decoding | `FuzzCommonEnvelope` and `FuzzEngineEnvelope` run in the full fuzz smoke gate |
| Architecture | the gate rejects gRPC, Protobuf, `common/v1`, or `engine/v1` imports from kernel packages |
| Offline operation | `make verify` runs ordinary tests, race, security, fuzz, coverage, vendor tests, and build with no proxy access |

The provisional protocol defines nine unary methods and two server-streaming methods. Typed
status details distinguish version/capability mismatch, replay bounds,
overload, stale clients, snapshot version skew, and uncertain mutation. Unknown
lifecycle event kinds fail closed. Protocol minor 1 snapshots carry a format,
lifecycle boundary, sequence, payload containing the immutable plan identity,
verified SHA-256 digest, and HMAC-SHA256 authority claim. Minor 0 advertises no
snapshot transfer; clients requiring snapshots or `snapshot-authority-v1` fail
capability negotiation.
The daemon advertises immutable generated definitions and selects the dynamic
generation at run start, returning an immutable `plan_id`; the client supplies
only definition ID/revision and cannot choose model policy or runtime-plugin
state. Authentication is reserved out of the payload for transport metadata.
Event replay captures bounds/page/tail registration atomically. Every
interaction stream begins with one complete pending snapshot, followed by
revision-contiguous opened/closed deltas. Snapshot import preserves its embedded
v1alpha3 run ID.

This is not the final Phase 4 schema freeze. The contract-repair slice defines
interaction discovery/run identity, reconnect ownership CAS, remote
suspend/resume, stable snapshot import identity, and atomic replay bounds. The
daemon host must still prove those contracts, same-daemon tombstone conflicts,
OS-backed authority-key lifecycle, metadata authentication, idempotent operation
commit, and RPC-context/run-lifetime separation.

Exact Phase 4 commit and final gate timings are appended to the canonical ledger
after the green tree is pushed; a commit cannot truthfully contain its own Git
object ID.

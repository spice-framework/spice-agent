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
| Negotiation | `common/v1` tests cover greatest-compatible selection, old/new peers, malformed ranges, capabilities, bounds, health, and typed status details |
| Engine behavior | `engine/v1` tests cover run validation, ordered replay, gaps, overload, cancellation, stale interaction responses, snapshots, and the exact generated service shape |
| Additive compatibility | Protobuf unknown fields survive unmarshal/remarshal in common and engine envelopes |
| Robust decoding | `FuzzCommonEnvelope` and `FuzzEngineEnvelope` run in the full fuzz smoke gate |
| Architecture | the gate rejects gRPC, Protobuf, `common/v1`, or `engine/v1` imports from kernel packages |
| Offline operation | `make verify` runs ordinary tests, race, security, fuzz, coverage, vendor tests, and build with no proxy access |

The provisional protocol defines seven unary methods and one server-streaming method. Typed
status details distinguish version/capability mismatch, replay bounds,
overload, stale clients, snapshot version skew, and uncertain mutation. Unknown
lifecycle event kinds fail closed. Snapshots carry a format, lifecycle boundary,
sequence, payload containing the immutable plan identity, and verified SHA-256 digest.
The daemon, not the client, selects the dynamic generation at run start and
returns an immutable `plan_id`; the client cannot pin arbitrary runtime-plugin
state through `AgentDefinitionRef`.

This is not the final Phase 4 schema freeze. Interaction delivery/run identity,
reconnect ownership, snapshot suspend/import identity, atomic replay bounds,
and RPC-context versus run-lifetime ownership remain explicit host-slice audit
items. `ImportSnapshot` already asserts the server-selected `expected_plan_id`
rather than exposing a client-selected dynamic generation.

Exact Phase 4 commit and final gate timings are appended to the canonical ledger
after the green tree is pushed; a commit cannot truthfully contain its own Git
object ID.

# Phase 4 Lifecycle Unary Adapter Evidence

This evidence covers the authenticated gRPC translation for Start, Cancel,
Respond, Suspend, Resume, Export, and Import. It does not claim event or
interaction streams, an OS listener, discovery, managed daemon startup, or a
public gRPC client adapter.

| Contract | Executable evidence |
| --- | --- |
| Complete generated service path | A `bufconn` acceptance test initializes a negotiated client through the generated gRPC client and server, then executes all seven lifecycle unary RPCs. Each call reaches the typed RunHost boundary and each success response passes its public `engine/v1` validator. |
| Valid-Go client translation | Wire values become immutable standard-library `client` requests: definition and operation identities, a single user-text input, cancellation reason, structured interaction JSON, run mutations, run references, and deterministic opaque snapshot bytes. No provider object, registry, reflection, or raw service lookup crosses the boundary. |
| Ownership and bounds | Every request is validated against the server hard limit before registry lookup, then against the exact negotiated limits after registry lookup and `SessionStore.Check`. Known stale epochs retain expected/observed facts; unknown ownership remains non-disclosing. |
| Snapshot authority | Export and import require protocol minor 1 plus `snapshots` and `snapshot-authority-v1`. Import rejects unsigned root-envelope extensions and oversized opaque envelopes, then deterministically serializes the structurally valid envelope. The adapter has no authority key or verifier; RunHost remains the sole keyed admission path. |
| Failure vocabulary | Typed stale, host/session capacity, uncertain operation, missing run, conflict, closed, and unavailable errors map to validated status-only responses with fixed safe messages. Missing or malformed recovery facts and arbitrary dependency errors fail closed. Application error strings never cross the protocol. |
| Transport separation | Authentication, request/response framing, request cancellation, and deadline expiry use gRPC errors. Application failures use response `Status`. A real gRPC cancellation test proves no application response is returned. |
| Architecture-proof input boundary | Although `engine/v1.Message` is provider-neutral, public `client.Input` currently contains exactly one user text part. Two-part or non-text Start input is rejected before RunHost rather than flattened or partially interpreted. |

Focused package tests run shuffled under the race detector and include positive,
invalid, unsupported, stale, overload, capability, unsigned-envelope,
cancellation, secret-redaction, and fail-closed cases. The integrating writer
records the exact repository gates on the final commit tree.

# Phase 4 Protocol Prerequisite Evidence

This evidence covers the transport-independent unary validation, initialization
preflight, and opaque snapshot-transfer sizing prerequisites for the Phase 4
gRPC adapter. It does not claim a gRPC server, authentication interceptor, OS
listener, endpoint discovery, managed daemon, or TUI transport.

| Contract | Executable evidence |
| --- | --- |
| Side-effect-free preflight | `PreflightInitialize` validates the complete request under a fixed 1 MiB bootstrap bound, server configuration, supported protocol selection, capabilities, limits, request size, global health, definitions, and reconnect shape before returning an immutable `InitializeNegotiation`. `CompleteInitialize` is the only step that binds a host-allocated ownership result. Tests prove rejected negotiation never reaches session/ownership allocation, reconnect claims are defensive, stale completion fails, and post-preflight caller mutation cannot change the response. |
| Bounded completion | Preflight validates the selected request bound and proves a structurally valid worst-case success response—with a maximum-length client ID and maximum ownership epoch—fits the selected message limit before a host allocates ownership. Completion revalidates the actual result defensively, and error-only initialization responses remain inside the fixed bootstrap bound. |
| Unary request validation | Pure validators cover health, start, cancel, interaction response, suspend, resume, snapshot export, and authenticated snapshot import requests. They require initialized ownership, operation/run/interaction identities as applicable, bounded valid JSON, valid negotiated limits, negotiated collection bounds, and the negotiated encoded-message bound. |
| Snapshot import authority split | `ValidateImportSnapshotRequestStructure` validates the complete untrusted import shape, counts compatible unknown fields in the encoded-message limit, enforces the opaque-envelope bound, checks the payload digest and suspended lifecycle, and deliberately does not authenticate the HMAC. Unsigned root-envelope extensions fail closed. Tests prove a mutated HMAC passes structure and fails keyed validation, invalid structure never reaches the verifier, a structurally accepted envelope crosses client serialization into `RunHost.Import`, and the host remains the only keyed admission path. |
| Unary response validation | Pure validators cover health, run start/cancel, interaction response, suspend/resume, and snapshot export/import responses. Successful results require their complete semantic fields. Every application error is status-only; inactive result fields fail closed. Start always begins at sequence one, cancel outcomes are exclusive, and resumable sequences cannot overflow. |
| Snapshot transfer size | The public client bound is payload plus the exact current `engine/v1` maximum-width signed-envelope overhead. A deterministic Protobuf fixture proves a maximum 16 MiB kernel payload produces exactly that opaque envelope size, the client accepts it, and one additional byte is rejected. A separate assertion proves the enclosing RPC response is larger and is governed by negotiated message limits, not the client envelope-only bound. |
| Robustness | Positive, negative, nil, inactive-field, exact-boundary, overflow, deterministic encoding, defensive-copy, UTF-8/control-token, repeated-field, and fuzz tests cover the new seams. |
| Authentication wording | RFC 0004 now states the implementable boundary: gRPC applies a hard receive bound during decoding, then metadata authentication completes before application handling or daemon-state access. It no longer claims unary interceptors authenticate before Protobuf decoding. |
| Limit semantics | Initialization request/response limits are per-connection, while `Health.limits` advertises server-global capacity. Response correlation proves selected limits do not exceed either the request or global capacity; a client with a lower run limit may still observe a larger global active-run count. |
| Typed recovery facts | RunHost capacity, session-gate capacity, and known stale-session errors retain bounded resource/limit/observation or ownership-epoch facts through durable failure replay and abandoned pre-boundary outcomes. Tests prove exact round trips and verify malformed, control-bearing, overflowed, or legacy detail fields fail closed to public sentinels instead of leaking or inventing facts. |

The existing `NegotiateInitialize` function remains as a compatibility wrapper
over preflight and completion. New daemon adapters must call the two phases
separately so invalid negotiation cannot allocate or reconnect session state.

The focused package tests, race run, vet, linter, and repository gates are
reported by the integrating writer on the exact combined commit tree.

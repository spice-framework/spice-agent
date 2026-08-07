# Phase 5 Runtime-Tool Protocol Evidence

This evidence covers the frozen initial `plugin/v1` Protobuf wire contract, its
public handwritten validation/conversion layer, the reusable black-box
conformance harness, and independent Go and Python fixture processes. It does
not claim the production process host, executable or digest verification, candidate
activation, generation leases, stderr ownership, or capability enforcement.

| Contract | Executable evidence |
| --- | --- |
| Schema compatibility | `buf lint .` and FILE-level `buf breaking . --against schema-baseline` run through `make check` |
| Deterministic generation | `make proto`; the quality gate regenerates all plugin messages and gRPC stubs with local pinned tools and compares every byte |
| Authenticated initialization | Tests prove language-neutral runtime/build provenance, exact launch/challenge/session sizes, protocol/capability/limit/manifest selection, HMAC-SHA256 transcript proof, wrong-secret rejection, and tamper rejection including unknown fields |
| Canonical transcript | Shared Go/Python goldens cover every frozen known field, exact large integers, Unicode, proof exclusion, nested and wrong-wire unknowns, occurrence-order preservation, and normalized non-minimal varints. Adding a known Initialize field requires a negotiated transcript revision |
| Immutable tools | Manifest conversion produces defensive kernel `tool.Definition` values and rejects empty, duplicate, unsorted, unknown, inconsistent, or oversized definitions |
| Execution | Stateful stream tests prove exact call correlation, contiguous sequence, bounded progress, model-visible result, typed definitive/uncertain infrastructure failure, exactly one terminal, missing-terminal rejection, and post-terminal rejection |
| Negotiated bounds | Both fixtures reject a manifest that does not fit selected limits without committing the session, defensively retain successful limits, enforce call size and concurrent-call admission, and atomically fence admission during Drain |
| Lifecycle | Drain and Shutdown requests are exact-session-bound; successful Drain cannot report active calls and all lifecycle messages remain negotiated-size-bounded |
| Forward-safe unknowns | Generated messages preserve unknown fields on unmarshal/remarshal; ordered unknown occurrences participate in handshake proof and encoded-size accounting without claiming compatibility after a field is promoted into the frozen known inventory |
| Secret safety | Handshake secrets never enter Protobuf. Validation errors do not reflect rejected token, schema, argument, result, capability, or proof content; failure text is explicitly named `safe_message` and remains the plugin author's responsibility |
| Robust decoding | `FuzzPluginEnvelope` exercises initialization and Execute decoding, while `FuzzBootstrap` exercises the shared public bounded stdin bootstrap parser in the full fuzz smoke gate |
| Architecture | The gate excludes `plugin/v1`, gRPC, and Protobuf imports from kernel packages; conversion points only toward the existing public `tool` values |
| Independent Go process | Acceptance builds the fixture with `-trimpath`, sends its explicit local address and secret only through bounded stdin, verifies its single exact readiness record and empty remaining stdout, dials a current-user Unix socket or Windows named pipe, and observes clean process exit after Shutdown |
| Black-box conformance | The reusable public suite uses only `PluginServiceClient` and proves authenticated duplicate initialization, immutable echo/fail/wait definitions, contiguous progress/result, correlated typed failure, strict UTF-8 JSON and token rejection, malformed and oversized calls, unknown fields, RPC cancellation, Drain admission fencing, and Shutdown |
| Independent Python process | Python 3.12+ uses exact locked `grpcio==1.75.1` and `protobuf==6.32.1`, committed generated bindings, a schema-hash regeneration gate, exact stdout readiness, secret clearing, and the same public Go conformance suite. Windows uses private AF_UNIX because Python gRPC cannot serve named pipes; production local IPC is unchanged |
| Cancellation semantics | A normally completed stream requires exactly one terminal. Client transport cancellation may preempt it, returns gRPC `CANCELLED`, releases the active call, and never becomes a successful or replay-authorizing terminal |

Cross-language acceptance is `make verify-python`. It performs a frozen offline
environment sync, deterministic schema regeneration, warning-strict Python unit
and real-process tests, bytecode compilation, and the Go-owned conformance run.

Exact commit and full-gate timing are appended only after the green tree is
committed by the repository writer. Production host behavior remains pending
Phase 5 work.

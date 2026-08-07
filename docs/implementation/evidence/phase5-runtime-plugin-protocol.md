# Phase 5 Runtime-Tool Protocol Evidence

This evidence covers only the frozen initial `plugin/v1` Protobuf wire contract
and its public handwritten validation/conversion layer. It does not claim a
process host, executable or digest verification, candidate activation,
generation leases, stderr ownership, capability enforcement, or Go/Python
fixtures.

| Contract | Executable evidence |
| --- | --- |
| Schema compatibility | `buf lint .` and FILE-level `buf breaking . --against schema-baseline` run through `make check` |
| Deterministic generation | `make proto`; the quality gate regenerates all plugin messages and gRPC stubs with local pinned tools and compares every byte |
| Authenticated initialization | Tests prove language-neutral runtime/build provenance, exact launch/challenge/session sizes, protocol/capability/limit/manifest selection, HMAC-SHA256 transcript proof, wrong-secret rejection, and tamper rejection including unknown fields |
| Immutable tools | Manifest conversion produces defensive kernel `tool.Definition` values and rejects empty, duplicate, unsorted, unknown, inconsistent, or oversized definitions |
| Execution | Stateful stream tests prove exact call correlation, contiguous sequence, bounded progress, model-visible result, typed definitive/uncertain infrastructure failure, exactly one terminal, missing-terminal rejection, and post-terminal rejection |
| Lifecycle | Drain and Shutdown requests are exact-session-bound; successful Drain cannot report active calls and all lifecycle messages remain negotiated-size-bounded |
| Additive compatibility | Generated messages preserve unknown fields on unmarshal/remarshal; unknown bytes participate in handshake proof and encoded-size accounting |
| Secret safety | Handshake secrets never enter Protobuf. Validation errors do not reflect rejected token, schema, argument, result, capability, or proof content; failure text is explicitly named `safe_message` and remains the plugin author's responsibility |
| Robust decoding | `FuzzPluginEnvelope` exercises initialization and Execute decoding in the full fuzz smoke gate |
| Architecture | The gate excludes `plugin/v1`, gRPC, and Protobuf imports from kernel packages; conversion points only toward the existing public `tool` values |

Exact commit and full-gate timing are appended only after the green tree is
committed by the repository writer. Host behavior remains pending Phase 5 work.

# RFC 0005: Runtime Tool Plugin Protocol

- **Status:** draft
- **Initial package:** `plugin/v1`
- **Transport:** one local gRPC connection per process generation

## Purpose

Runtime plugins support tools whose executable implementation is not part of the
compiled application. They are the only dynamic extension graph. The protocol
does not recreate static Spice composition and never registers compiled stages,
providers, or native UI code.

## Identity and launch

Configuration includes a canonical absolute executable path, expected SHA-256,
plugin name, startup/call/drain bounds, and approved capabilities. The host opens
the verified file, detects path/digest drift as far as the operating system
permits, launches with a random single-use handshake secret, and bounds stderr.
PATH lookup and ambient directory scanning are forbidden.

`Initialize` exchanges protocol/build identity, manifest, tool definitions,
capabilities, limits, and the launch-secret proof. A candidate is not visible to
runs until all facts validate.

## Generation lifecycle

Candidate activation is atomic and affects future runs only. Starting a run
leases the current generation and snapshots its identity/tool definitions. Old
processes remain alive until all leases release, then receive bounded drain and
shutdown. A failed candidate or drain never mutates the active generation.

Host restart is bounded and applies only to future calls. A call interrupted by
crash is failed. A mutating call with uncertain outcome is never automatically
replayed. Cancellation propagates through RPC then process-tree cleanup; failure
to confirm termination is observable.

## Initial tool API

`ListTools` returns immutable bounded definitions. `Execute` accepts one validated
call and streams bounded progress followed by exactly one result or typed
failure. Call/progress IDs must match. Unknown capabilities, duplicate names,
oversized schemas/payloads, post-terminal messages, and stdout protocol
contamination fail the candidate or call according to phase.

## Compatibility and language neutrality

Buf lint/breaking checks govern `plugin/v1`. Additive optional fields follow the
engine-protocol rules. Go and Python 3.12+ fixtures must pass the same black-box
conformance suite. Generated client code is permitted; importing host-private Go
packages is not.

## Trust model

Digest pinning provides provenance/change detection, not sandboxing. The process
runs with the daemon user's privileges unless a later launcher/policy extension
provides real isolation. Capability declarations are mandatory metadata but not
security theater: help/status must state whether an enforcing decorator exists.

## Acceptance before freeze

Conformance covers wrong digest, path replacement, handshake replay, old/new
versions, unknown fields, duplicate tools, malformed/oversized traffic, bounded
stderr, crash at every lifecycle phase, cancellation, uncertain mutation,
activation races, generation leases, drain timeout, and clean Windows/Unix
process-tree shutdown.

# RFC 0005: Runtime Tool Plugin Protocol

- **Status:** initial `plugin/v1` runtime-tool wire contract frozen; host and
  generation management remain provisional
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

`Initialize` exchanges protocol/build identity, a sorted immutable manifest,
tool definitions, feature capabilities, negotiated limits, a 128-bit launch
identity, a 256-bit host challenge, and a 128-bit connection session identity.
The 256-bit launch secret is never transmitted. The plugin returns an
HMAC-SHA256 proof over a domain-separated deterministic encoding of the entire
request and response with the proof field cleared. Unknown fields therefore
participate in authentication and remain additive. A candidate is not visible
to runs until all facts validate.

Build identity names a language-neutral runtime string (for example,
`go1.26.5` or `python3.12`) rather than a Go-only field, so independent Python
fixtures implement the same schema without inventing provenance.

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

The initialization manifest is the one immutable bounded definition catalog;
there is no mutable `ListTools` surface. Each definition uses the kernel's
canonical capability, effect, replay-safety, and input-schema contract.
`Execute` accepts one validated call and streams strictly contiguous frames
starting at sequence one: zero or more bounded progress frames followed by
exactly one model-visible result or correlated typed infrastructure failure.
An uncertain failure always forbids retry. Call IDs must match. Unknown
capabilities/enums, duplicate or unsorted names, oversized schemas/payloads,
missing terminals, and post-terminal messages fail closed. `Drain` stops call
admission and joins admitted calls; `Shutdown` releases a drained session.
RPC cancellation/deadline remain the transport mechanism for bounded waits.

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

## Wire-freeze acceptance

Repository validation covers transcript tampering and wrong secrets, old/new
versions, unknown-field round trips, duplicate/unsorted tools, unknown
capabilities and enums, malformed/oversized traffic, correlation and sequence
mismatch, uncertain mutation, missing terminals, post-terminal traffic, and
Drain/Shutdown shape. The following remain host/conformance acceptance and are
not claimed by this wire freeze: wrong digest, path replacement, handshake
replay storage, bounded stderr, crash at every lifecycle phase, cancellation
and drain timeouts across a real process, activation races, generation leases,
and Windows/Unix process-tree shutdown.

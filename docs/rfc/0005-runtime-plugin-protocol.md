# RFC 0005: Runtime Tool Plugin Protocol

- **Status:** initial `plugin/v1` runtime-tool wire contract frozen and Go/Python
  conformance proven; authenticated host, atomic generations, exact leases, and
  graceful lifecycle implemented; recovery policy remains provisional
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
and digest-verifies the exact executable object before allocating a launch
identity, secret, or endpoint, and retains that lease until process containment
is proved. Its injected `process.VerifiedLauncher` has no pathname-only
fallback: Linux launches use a duplicate of the verified descriptor; Darwin
launches a digest-reverified private snapshot written only from that descriptor
in a new mode-0700 process-owned directory; and Windows launch uses the
non-sharing lease plus a recheck after suspended creation and before resume.
Both Darwin leases remain live until containment, then exact nonrecursive
cleanup removes the snapshot. The host performs another identity/digest
recheck after ownership transfer and before readiness as defense-in-depth. PATH
lookup and ambient directory scanning are forbidden.

`Initialize` exchanges protocol/build identity, a sorted immutable manifest,
tool definitions, feature capabilities, negotiated limits, a 128-bit launch
identity, a 256-bit host challenge, and a 128-bit connection session identity.
The 256-bit launch secret is never transmitted. The plugin returns an
HMAC-SHA256 proof over a domain-separated deterministic encoding of the entire
request and response with the proof field cleared. Unknown fields therefore
participate in authentication and are tamper-evident. A candidate is not
visible to runs until all facts validate.

### Canonical initialize transcript v1

Protobuf serialization is not the HMAC input. Deterministic Protobuf output is
not a cross-runtime canonicalization contract and different language runtimes
may retain or order unknown fields differently. Both peers instead construct
the following UTF-8 JSON value and authenticate its exact bytes:

```text
["spice-agent/plugin/v1/initialize",1,InitializeRequest,InitializeResponse]
```

There is no whitespace or final newline. JSON strings escape quotation marks,
backslashes, and controls in the normal JSON spelling; U+2028 and U+2029 use
lowercase `\u2028` and `\u2029`; other valid Unicode is emitted directly as
UTF-8. Invalid UTF-8 fails closed. Booleans are `true` or `false`. Integers are
exact unsigned or signed base-ten JSON integers with no leading zero, exponent,
decimal point, or precision conversion. Protobuf `bytes` values are unpadded
RFC 4648 base64url strings. An absent message is `null`. Repeated-field order is
preserved; validation separately enforces sorted order where the wire contract
requires it.

Every present known message is an array whose first item is the exact type name,
whose middle items are all known semantic fields in protobuf field-number
order, and whose final item is its unknown-atom array. The exact shapes are:

```text
InitializeRequest = ["InitializeRequest",ProtocolRange,BuildIdentity,
  CapabilitySet,CapabilitySet,Limits,bytes,bytes,unknown]
InitializeResponse = ["InitializeResponse",Status,ProtocolVersion,
  BuildIdentity,CapabilitySet,Limits,Manifest,bytes,bytes,bytes,"",unknown]

ProtocolVersion = ["ProtocolVersion",major,minor,patch,unknown]
ProtocolRange = ["ProtocolRange",ProtocolVersion,ProtocolVersion,unknown]
BuildIdentity = ["BuildIdentity",component,version,commit,runtime,unknown]
CapabilitySet = ["CapabilitySet",[name...],unknown]
Limits = ["Limits",max_message_bytes,max_tools,max_schema_bytes,
  max_call_argument_bytes,max_result_bytes,max_progress_bytes,
  max_concurrent_calls,unknown]
Manifest = ["Manifest",name,version,[ToolDefinition...],unknown]
ToolDefinition = ["ToolDefinition",name,description,input_schema_json,effect,
  replay_safety,CapabilitySet,unknown]

Status = ["Status",code,message,retryable,operation_id,detail,unknown]
detail = null | ["version_mismatch",VersionMismatch]
  | ["capability_mismatch",CapabilityMismatch]
  | ["replay_bounds",ReplayBounds] | ["overload",Overload]
  | ["stale_client",StaleClient]
  | ["snapshot_version_mismatch",SnapshotVersionMismatch]
  | ["uncertain_operation",UncertainOperation]
VersionMismatch = ["VersionMismatch",ProtocolRange,ProtocolRange,unknown]
CapabilityMismatch = ["CapabilityMismatch",[required...],[available...],
  [missing...],unknown]
ReplayBounds = ["ReplayBounds",requested_after_sequence,earliest_sequence,
  latest_sequence,recovery_sequence,unknown]
Overload = ["Overload",resource,limit,observed,unknown]
StaleClient = ["StaleClient",expected_epoch,observed_epoch,unknown]
SnapshotVersionMismatch = ["SnapshotVersionMismatch",expected,observed,unknown]
UncertainOperation = ["UncertainOperation",operation_id,operation_kind,unknown]
```

The empty string in the `InitializeResponse` proof position is the canonical
value regardless of the message's observed `handshake_proof`; this prevents
self-authentication while retaining a fixed slot for every known field.

Unknown fields are recovered separately at every present message boundary,
including nested known messages. A field with a known number but the wrong wire
type remains unknown and is authenticated. Each occurrence is retained and
normalized to one atom:

```text
[field_number,wire_type,value]
```

Wire types 0, 1, and 5 use their exact unsigned integer value. Wire type 2 uses
an unpadded base64url string. Atoms remain in their original occurrence order,
and duplicate occurrences remain present. They must never be sorted: future
repeated fields preserve occurrence order, while a future singular field may
use protobuf's last-value-wins rule. Non-minimal varint spelling is normalized
to its exact integer without changing occurrence order. Field zero, truncated
or overflowing values, unsupported wire types, and start/end groups fail
closed. Length-delimited unknown values remain opaque bytes; only known nested
messages are recursively canonicalized.

The proof is exactly `HMAC-SHA256(launch_secret, transcript_bytes)`, with no
Protobuf bytes, length framing, or additional prefix. The domain and transcript
version are the first two authenticated array items. The shared Go/Python
golden covers every wire atom kind, repeated ordered unknown occurrences, a
known-number/wrong-wire atom, nested unknowns, maximum `uint64`, Unicode
escaping, and proof self-exclusion.

Transcript v1 freezes the complete known-field inventory and array position of
every Initialize message listed above. Unknown fields can cross a same-schema
peer boundary without being silently dropped or modified, but they are not
automatically additive across peers generated from different schemas. Once a
future peer promotes an unknown field to known, it would otherwise reclassify
that occurrence from the unknown tail into a known slot and compute different
bytes. Adding or promoting any Initialize field therefore requires a negotiated
new transcript version and corresponding protocol revision; it may not silently
reuse transcript v1.

Build identity names a language-neutral runtime string (for example,
`go1.26.5` or `python3.12`) rather than a Go-only field, so independent Python
fixtures implement the same schema without inventing provenance.
`plugin/v1.ValidateBuildIdentity` is the public, value-redacting validation
boundary for application adapters that construct host or plugin provenance;
callers do not need to fabricate an initialization request to validate it.

## Generation lifecycle

Candidate activation is atomic and affects future runs only. Starting a run
leases the current generation and snapshots its identity/tool definitions. Old
processes remain alive until all leases release, then receive bounded drain and
shutdown. A failed candidate or drain never mutates the active generation.

Host restart is bounded and applies only to future calls. A call interrupted by
crash is failed. A mutating call with uncertain outcome is never automatically
replayed. Cancellation propagates through RPC then process-tree cleanup; failure
to confirm termination is observable.

The implemented host fails closed when an active generation becomes unhealthy;
it does not substitute an older generation or compiled-only dispatcher. A fresh
complete set must validate before replacement. Automatic bounded recovery is a
separate policy still pending. Plan identities contain a private random host
epoch, so exact generation recovery is intentionally limited to a retained live
host. Process restart cannot recreate a dynamic generation named by a snapshot.

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
Every normally completed stream must contain its terminal frame. Client
transport cancellation can preempt delivery of that terminal; this is a
transport-canceled operation, never a valid normally completed stream and never
authorization to replay an uncertain mutation.

## Compatibility and language neutrality

Buf lint/breaking checks govern `plugin/v1`. Additive optional fields follow the
engine-protocol rules. Go and Python 3.12+ fixtures must pass the same black-box
conformance suite. Generated client code is permitted; importing host-private Go
packages is not.

The reusable Go harness uses only the generated public client. The independent
Go fixture receives its explicit local address and one-use secret through a
bounded private stdin bootstrap, writes one exact readiness record to stdout,
and then keeps stdout silent while serving current-user local IPC. This
bootstrap is fixture infrastructure, not the production host contract. The
independent Python 3.12+ fixture passes the same public harness with pinned
dependencies and committed generated bindings. Python gRPC uses private AF_UNIX
on Windows because it cannot serve named pipes; this exception is limited to
cross-language fixture conformance.

## Trust model

Digest pinning provides provenance/change detection, not sandboxing. The process
runs with the daemon user's privileges unless a later launcher/policy extension
provides real isolation. Capability declarations are mandatory metadata but not
security theater: help/status must state whether an enforcing decorator exists.
The pin covers the selected executable file, not its dynamic loader, shared
libraries, DLLs, shebang interpreter, network dependencies, or mutable files
named by arguments. A malicious process already running as the daemon user is
outside the local trust boundary; Unix cannot portably freeze same-user in-place
mutation of an open inode.

## Wire-freeze acceptance

Repository validation covers transcript tampering and wrong secrets, old/new
versions, unknown-field round trips, duplicate/unsorted tools, unknown
capabilities and enums, malformed/oversized traffic, correlation and sequence
mismatch, uncertain mutation, missing terminals, post-terminal traffic, and
Drain/Shutdown shape. The following remain host/conformance acceptance and are
not claimed by this wire freeze: wrong digest, path replacement, handshake
replay storage, bounded stderr, crash at every lifecycle phase, cancellation
outside the cooperative RPC boundary, activation races, generation leases, and
Windows/Unix process-tree shutdown. Both independent fixtures prove RPC
cancellation and drain timeout over real processes.

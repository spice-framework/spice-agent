# RFC 0004: Local Engine Protocol

- **Status:** provisional `common/v1` and `engine/v1` schema baseline; host
  semantics and final freeze remain in progress
- **Initial packages:** `common/v1`, `engine/v1`
- **Transport:** authenticated user-local gRPC

## Scope

The protocol separates a generated headless daemon from local clients. It is not
an alternate kernel API and does not expose internal Go structs. Protobuf exists
because there is a real process/language boundary; embedded applications call Go
interfaces directly.

## Initialization

The first call presents protocol major/minor, client build identity, supported
capabilities, maximum message limits, and the endpoint authentication token. The
daemon returns its build identity, supported capabilities, configured limits,
and health. Incompatible major versions or required capabilities fail before a
run is created, with both observed and required values.

## Operations

- `Health` reports readiness, version, replay limits, active-run count, and
  bounded degraded reasons without configuration secrets.
- `StartRun` accepts a validated agent definition reference, expected
  static-plan fingerprint, initial message, and client operation ID. The server
  selects and leases the current dynamic generation, then returns the stable run
  ID, initial sequence, and immutable `plan_id`; clients cannot choose a plugin
  generation through the run definition.
- `StreamEvents(after_sequence)` replays/tails authoritative events and maps
  out-of-range/resource-exhaustion errors to typed recovery details.
- `CancelRun` is idempotent and reports whether the run was already terminal.
- `RespondInteraction` uses interaction and response IDs to reject stale or
  duplicate responses deterministically.
- `ExportSnapshot` returns a versioned provider-neutral safe snapshot at a
  supported boundary. `ImportSnapshot` is a separate explicit mutation with
  idempotency and uncertain-outcome rules and accepts only suspended snapshots.

## Bounds and backpressure

Every unary/stream request has encoded-byte, collection-count, and deadline
limits. Server queues are bounded. A slow client is disconnected with last
delivered sequence; it never backpressures kernel execution unless it explicitly
configured a required durability observer outside this protocol.

## Compatibility

Buf lint and breaking checks govern schemas. Additive optional fields are
tolerated. Unknown enum values are retained only where a safe textual fallback
exists; unknown lifecycle operations fail closed. Removed/renumbered fields and
semantic reuse are breaking. Supported client/server ranges are machine-readable
in compatibility manifests.

## Security boundary

The initial protocol has no TCP listener. Unix sockets or Windows named pipes
and endpoint metadata are current-user only. A random token protects against
accidental/ambient local connections but does not defend against code already
running as the same user. Remote access requires a new threat model and protocol
extension.

## Failure semantics

Transport failure never implies a mutating request failed before commit.
Operation IDs provide deduplication where defined. Mutating tool outcomes that
lose acknowledgement are marked uncertain and never replayed automatically.
Cancellation is cooperative and terminal events remain authoritative.

## Acceptance before freeze

The schema-foundation acceptance covers old/new versions, unknown fields,
capability mismatch, validation before authentication-token retention,
deadline/cancellation fields, overload, cursor gap/recovery, stale interaction
response, snapshot version skew, malformed input, fuzz smoke, Buf lint/breaking,
and deterministic generation. Duplicate-operation behavior, actual transport
authentication, reconnect, half-close, and Windows/Unix endpoint permissions
remain acceptance requirements for the daemon host slice.

Before final freeze, the host slice must also resolve interaction prompt replay
and run identity, reconnect ownership CAS, remote suspend/resume and imported
run identity, atomic replay-bound observation, and the invariant that a unary
RPC context never owns the lifetime of the run it creates. The committed Buf
baseline makes those amendments explicit; this provisional RFC does not claim
the seams are complete.

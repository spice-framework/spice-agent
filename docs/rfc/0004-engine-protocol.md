# RFC 0004: Local Engine Protocol

- **Status:** draft
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
- `StartRun` accepts a validated agent definition reference, initial message,
  and client operation ID and returns the stable run ID plus initial sequence.
- `StreamEvents(after_sequence)` replays/tails authoritative events and maps
  out-of-range/resource-exhaustion errors to typed recovery details.
- `CancelRun` is idempotent and reports whether the run was already terminal.
- `RespondInteraction` uses interaction and response IDs to reject stale or
  duplicate responses deterministically.
- `GetSnapshot` returns a versioned provider-neutral safe snapshot at a supported
  boundary. Import/resume is a separate explicit mutation with idempotency and
  uncertain-outcome rules.

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

Tests cover old/new versions, unknown fields, authentication, duplicate operation
IDs, deadline/cancellation, overload, cursor gap/recovery, reconnect, stale
interaction response, snapshot version skew, half-close, malformed input, and
Windows/Unix endpoint permissions.

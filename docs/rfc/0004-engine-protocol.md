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

The gRPC server applies the fixed 1 MiB initialization bootstrap receive bound
while transport framing and Protobuf decoding occur. Transport middleware then
authenticates the endpoint token from gRPC metadata before application handling
or access to daemon state; authentication before decoding is not a capability
provided by gRPC unary interceptors. `Initialize` then presents protocol major/minor, client build
identity, supported capabilities, and maximum message limits. Pure negotiation
preflight validates the complete request against both the bootstrap and selected
message bounds and proves a worst-case successful response fits before a host
allocates or reconnects a client session. Error-only initialization responses
remain under the fixed bootstrap bound.
A new owner receives a stable client ID at epoch one. Reconnect supplies that
client ID and its expected epoch; the daemon performs a compare-and-swap and
returns the same ID at exactly the next epoch. A stale claim fails without
changing ownership.

The implemented server wrapper installs unary and streaming authentication as
one non-optional assembly step and retains successful negotiations in a bounded
private registry. Each entry contains the exact daemon ownership session plus a
validated defensive response clone. Registry lookup never substitutes for
`SessionStore`: Health rechecks the stable identity and epoch immediately before
calling the transport-independent host. Registry shutdown clears only adapter
state and never owns or cancels daemon session contexts.

The daemon returns its build identity, supported capabilities, negotiated
per-connection limits, global health, and one immutable generated
`DefinitionSet`. `Health.limits` remains the server-global configured capacity;
it is not rewritten to the potentially lower selected client limits. Definitions own
model and turn-limit policy on the server. A run request selects only an exact
definition ID/revision; it cannot supply provider configuration or any static
or dynamic plan fingerprint. Incompatible major versions or required
capabilities fail before a run is created, with both observed and required
values.

The implementation advertises protocol range 1.0 through 1.2. Snapshot transfer
is atomic with authenticated authority: minor-0 negotiation removes both
`snapshots` and `snapshot-authority-v1`. A client requiring either receives a
typed missing-capability result. A minor-1 client must require
`snapshot-authority-v1` before relying on export or import.

Minor 2 defines the complete-snapshot-first interaction stream without the
provisional historical `replay_limit` field. `StreamInteractions` requires a
minor-2 negotiation; peers that negotiate minor 0 or 1 receive an explicit
version refusal rather than silently assigning a different meaning to field 3.
Because reconnect must fit the complete pending set in one frame, a minor-2
client must request message and collection limits at least as large as the
server's advertised bounds. Host construction proves its pending-interaction
admission capacity fits those server bounds, and every accepted live delta is
validated against the resulting reconnect snapshot before state advances.

The transport-independent host represents this catalog as exact immutable
`agent.Definition` values rather than reconstructing model or maximum-turn
policy from wire data. Its stable client store derives every ownership epoch
context from the daemon root and performs reconnect as an exact one-winner CAS.
Mutating RPC adapters will use the bounded idempotency ledger keyed by stable
client identity plus operation identity; an epoch change fences execution but
does not erase committed operation outcomes. An executor may explicitly abandon
an operation identity only after proving that no externally visible or durable
commit boundary was reached. This removes the pending entry and permits one
live duplicate or later caller to retry through normal bounded admission.
Arbitrary errors, panics, ambiguous writes, wrapped abandonment markers, and
abandonment returned with a result remain committed uncertain outcomes.

## Operations

- `Health` reports readiness, version, replay limits, active-run count, and
  bounded degraded reasons without configuration secrets.
- `StartRun` accepts an exact server-advertised definition reference, initial
  message, and client operation ID. The server selects and leases the current
  dynamic generation, then returns the stable run ID, initial sequence, and
  immutable `plan_id`; clients cannot choose a model or plugin generation.
- `StreamEvents(after_sequence)` atomically captures retained bounds and a
  count/byte-bounded page. Its control reports optional `page_last_sequence`,
  `has_more`, and `tailing`. Live tail registration occurs under the same lock
  only at the captured head, preventing a replay/tail gap. Current peers always
  send the page cursor; absence is accepted only as the provisional non-paging,
  non-tailing compatibility shape. For current controls, `has_more` is exactly
  equivalent to `page_last_sequence < latest_sequence`.
  Reconnect cancels the old observation and waits for its sender to exit before
  advancing ownership, but server-side observation cancellation cannot
  interrupt a gRPC `Send` already blocked by transport flow control. Managed
  clients therefore cancel and join every old stream RPC, or close its old
  transport, before attempting reconnect. A bounded reconnect may time out
  while an old send remains blocked; that timeout does not advance the epoch,
  and reconnect is retried only after the old RPC has exited.
- `StreamInteractions` always sends a complete atomic snapshot of every pending
  prompt and its captured control first. If live tailing was requested,
  revision-contiguous opened/closed deltas follow that control. Reconnect never
  relies on retained delta history, a historical cursor, or a replay limit to
  discover an unresolved prompt. Prompt/schema/response content remains outside
  authoritative run events.
- `CancelRun` is idempotent and reports whether the run was already terminal.
- `RespondInteraction` uses the client operation ID plus run and interaction IDs
  to reject stale correlation and deduplicate the mutation. It has no redundant
  response identity or synthetic event-sequence acknowledgement.
- `SuspendRun` pauses at a safe completed-turn boundary and `ResumeRun` continues
  the same locally owned run without changing its identity.
- `ExportSnapshot` returns a versioned provider-neutral safe snapshot at a
  supported boundary. `ImportSnapshot` is a separate explicit mutation with
  idempotency and uncertain-outcome rules, accepts only suspended v1alpha2
  snapshots, and preserves the run ID embedded in the snapshot. Clients cannot
  rename an imported run or assert a replacement plan. Export requires a trusted
  signer and import requires keyed HMAC verification before state mutation; an
  unkeyed structural check validates the full request shape, negotiated encoded
  size, envelope digest, authority shape, and suspended lifecycle but is never
  import authority. Root-envelope unknown fields are rejected because they are
  outside the authority MAC; request and authority unknown fields remain
  forward-compatible only within the negotiated request and opaque-envelope
  bounds. The transport performs that check before translating the
  envelope; `RunHost.Import` performs the keyed verification and remains the
  sole admission boundary.
  Export uses the daemon authority's typed `agent.Snapshot` issuer; wire run
  identity, sequence, lifecycle, and payload are derived from that single
  validated value. A successful durable signing result wins late cancellation,
  while uncertainty or post-tombstone cleanup failure wins over cancellation
  and is never automatically retried.

All unary lifecycle handlers authenticate transport metadata, validate the
request under the server hard limit, resolve the exact negotiated session and
recheck SessionStore ownership, then revalidate under that connection's limits
before constructing standard-library client values. Start accepts exactly one
user text part in the architecture-proof contract; other valid wire message
shapes receive `INVALID_ARGUMENT` rather than being silently flattened. Snapshot
RPCs require minor 1 plus both `snapshots` and `snapshot-authority-v1`.
Application failures use status-only responses with fixed safe messages and
typed recovery details. Only request/daemon cancellation and deadline failures
use gRPC error status.

The daemon adapter must use the kernel's transactional preparation boundary for
both `StartRun` and `ImportSnapshot`. A new run prepares the execution, uses its
immutable run ID to acquire durable daemon authority, then commits with a
daemon-owned run-root context. Import has a stricter transaction: prepare and
verify authority plus kernel resources, persist `IMPORTING`, register the kernel
run through `CommitPaused`, persist authority `ACTIVE`, and only then release
kernel execution through `Activate`. The setup or RPC context never becomes the
execution lifetime, and no event or extension work is visible before authority
activation. Every failure before kernel registration closes the uncommitted
preparation; every failure after inert registration explicitly aborts it. These
kernel contracts enable the adapter but do not themselves implement daemon
authority or protocol behavior.

## Bounds and backpressure

Every unary/stream request has encoded-byte, collection-count, and deadline
limits. Server queues are bounded. Exhaustion terminates the observation with
its exact last-delivered cursor; a transport already blocked in `Send` ends
when the client cancels/closes it or bounded server shutdown force-stops gRPC.
A slow client never backpressures kernel execution unless it explicitly
configured a required durability observer outside this protocol.
The reconnect fence bounds ownership rather than the transport implementation:
it deliberately remains held while a gRPC send is blocked. Client adapters must
cancel and join old stream RPCs (or close the old connection) before reconnect;
they must not assume observation cancellation can forcibly interrupt `Send`.

## Compatibility

Buf lint and breaking checks govern schemas. Additive optional fields are
tolerated. Unknown enum values are retained only where a safe textual fallback
exists; unknown lifecycle operations fail closed. Removed/renumbered fields and
semantic reuse are breaking. Supported client/server ranges are machine-readable
in compatibility manifests. Security-significant additions to snapshot authority
require a new MAC domain and versioned capability because older peers ignore
unknown Protobuf fields by design.

## Security boundary

The initial protocol has no TCP listener. Unix sockets or Windows named pipes
and endpoint metadata are current-user only. A random metadata token protects against
accidental/ambient local connections but does not defend against code already
running as the same user. Remote access requires a new threat model and protocol
extension.

Snapshot authority uses a distinct server-owned key. The public 32-byte scope ID
and positive generation select that key; neither is a secret. The HMAC proves
integrity and authority but does not encrypt conversation content. Scope keys
must never be derived from or reused as endpoint authentication tokens.

## Failure semantics

Transport failure never implies a mutating request failed before commit.
Operation IDs provide deduplication where defined. Mutating tool outcomes that
lose acknowledgement are marked uncertain and never replayed automatically.
Cancellation is cooperative and terminal events remain authoritative.
Executor panics and unexpected errors are contained as one bounded canonical
uncertain outcome and a secret-safe sentinel; expected business failures are
explicit canonical outcomes. A canceled duplicate waiter never cancels or
replaces the operation owner.

## Acceptance before freeze

The schema-foundation acceptance covers old/new versions, unknown fields,
capability mismatch, transport-metadata authentication separation,
deadline/cancellation fields, overload, cursor gap/recovery, stale interaction
response, snapshot version skew, malformed input, fuzz smoke, Buf lint/breaking,
and deterministic generation. Duplicate-operation behavior, actual transport
authentication, reconnect, half-close, and Windows/Unix endpoint permissions
remain acceptance requirements for the daemon host slice.

The pre-host contract repair resolves interaction prompt discovery, reconnect
ownership CAS, remote suspend/resume, imported run identity, and atomic replay
bounds in the provisional schema. The host slice must still prove those
contracts over real RPCs and enforce the invariant that a unary RPC context
never owns the lifetime of the run it creates. The committed Buf baseline makes
the amendments explicit; this provisional RFC does not claim a daemon exists.

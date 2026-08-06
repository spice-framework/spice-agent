# RFC 0002: Message, Stream, and Event Model

- **Status:** accepted for preview
- **Stability:** pre-1.0 with versioned persistence/transport encodings

## Message and model values

Messages have a stable validated ID, role, and bounded ordered content parts.
Parts represent provider-neutral text, tool calls, and tool results. Constructors
deep-copy byte slices and accessors return copies; mutable JSON supplied by a
caller can never mutate an in-flight request.

A model request carries a host-issued operation ID, model name, immutable
message history, and immutable tool definitions. Streams are strict tagged
unions: text delta, finalized tool call, completion with usage/metadata, or typed
failure. Empty, contradictory, oversized, duplicate, or post-terminal items fail.

Provider-specific safe facts use count/byte-bounded namespaced JSON. Namespaces
must be explicitly allowlisted by engine composition before metadata enters
events. Credentials, headers, prompt/tool content, arbitrary provider payloads,
and secrets are prohibited even when they fit the byte bound.

Provider start and receive failures carry a bounded typed problem. The host
tracks whether any valid stream item has been observed and computes retry
position; adapters cannot incorrectly label a partial stream as pristine.

## Event lifecycle

An event contains one immutable run ID, strictly increasing sequence, engine-
clock timestamp, stable kind, and bounded canonical JSON payload. Every committed
Run/Turn/Model/Tool/Interaction Started event receives exactly one matching
Completed, Failed, or Cancelled terminal. Cancellation preserves history and no
replay or import operation renumbers committed events.

## Authoritative replay

Each run owns one count-and-encoded-byte-bounded authoritative in-memory log.
Retention eviction is oldest-first and terminal events receive reserved
capacity. A subscriber atomically captures retained entries after a cursor and
joins the live tail while the log lock is held, preventing a replay/live gap.

Each subscription has independent count/byte queue bounds. A slow consumer is
terminated rather than blocking execution. Typed errors contain the requested,
earliest/latest, recovery cursor, last delivered sequence, and configured bounds
needed by daemon/client recovery.

## Observer ordering

The local log commits first. Required observers are called in deterministic
construction order and may backpressure within the operation context. A nil
return acknowledges durable acceptance according to that observer's documented
contract. An error may follow a partial external side effect, but the local
sequence remains committed and is never reused.

Best-effort observers receive an event only after every required observer
acknowledges it. They use bounded queues, count drops, cannot block execution,
and close without send/close races.

## Encoding and compatibility

Event kind and typed payload contracts are additive within a protocol major
version. Unknown optional data is retained or ignored according to the protocol
RFC; unknown lifecycle kinds cannot be guessed. Persistence snapshots record
their schema version and last committed sequence rather than serializing Go
private fields.

## Rejected alternatives

- One shared event channel loses reconnect/replay and lets one client starve all
  others.
- Unbounded histories and subscriber queues convert a slow UI into process OOM.
- Publishing best-effort observers before durable observers lets telemetry claim
  an event that durability rejected.
- Reusing a sequence after an observer error creates ambiguous external history.
- Raw provider event passthrough leaks churn and potentially secrets.

## Acceptance

Table, race, and fuzz tests cover message immutability, malformed stream unions,
partial failures, all lifecycle terminals, required-observer partial failure,
retention boundaries, cursor recovery, slow consumers, cancellation, shutdown,
and byte-identical event reconstruction.

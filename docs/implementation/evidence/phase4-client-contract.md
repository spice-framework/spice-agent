# Phase 4 transport-neutral client evidence

## Implemented boundary

The public `client` package is the UI-facing, transport-neutral contract for a
negotiated daemon session. It imports only the Go standard library and exposes
no Protobuf, gRPC, daemon-host, kernel, discovery, authentication-token, or OS
endpoint type. A future local adapter owns those details and translates them
into the immutable values and interfaces in this package.

One `client.Session` represents an exact stable-client ownership epoch. Its
methods are safe for concurrent use, use caller-owned operation contexts, and
never make a mutation retry decision on behalf of the caller. `Connection`
remains an immutable local negotiation snapshot after close. Session and
stream close operations are context-free, nonblocking, idempotent, race-safe,
and unblock active stream reads.

Event streams preserve every kernel event payload needed by a client without
requiring prior-event reconstruction. Successful page and tail state is an
explicit control frame carrying requested and next cursors, retained bounds,
latest sequence, continuation state, and tail state. Interaction streams
similarly carry a complete pending snapshot followed by revision-contiguous
changes and explicit stream controls.

Managed transport adapters own one additional reconnect obligation. They must
cancel and join all stream RPCs from the old ownership epoch, or close the old
transport, before reconnecting. The server cancels an old observation and holds
its reconnect fence until the stream sender exits, but gRPC does not provide a
server-side mechanism to interrupt a `Send` already blocked by transport flow
control. A deadline-bounded reconnect therefore may time out without advancing
the epoch; after the old RPC exits, retrying the same expected epoch is safe.

That statement applies only when reconnect did not reach the ownership CAS.
Protocol versions 1.0 through 1.2 cannot distinguish that case from a response
lost after a successful CAS, and likewise cannot replay a lost fresh-allocation
response. The concrete gRPC adapter therefore reports ambiguous initialization
transport loss as non-retryable. Caller-generated initialization-attempt replay
is reserved for protocol 1.3; no current adapter hides the uncertainty with an
automatic retry.

## Safety and fidelity

All public values validate the same count, byte, token, health, replay, and
protocol invariants as the wire boundary. Collections and byte slices are
defensively copied. Interaction schemas and responses use bounded arbitrary
JSON rather than a text-only shortcut. `StructuredValue` is secret-bearing and
safe by default under ordinary formatting, Go-syntax formatting, structured
logging, nested JSON encoding, errors, and diagnostics. Exact JSON bytes are
available only through the deliberately named `EncodeTransfer` adapter method.

Every failed wire status maps without discarding its safe message, retryable
flag, or optional status operation ID. Dedicated errors retain their typed
recovery detail in addition to those common `ErrorFacts`; uncertain-operation
detail keeps its distinct operation ID. Callers can use `errors.As` against a
specific recovery type or the common `StatusFailure` interface without
depending on transport status values.

The package's architecture test derives the exact standard-library package set
from the active Go 1.26.5 installation. It does not rely on import-path naming
heuristics and performs no network access.

## Verification scope

Tests cover positive and negative construction, defensive copying, all event
payload variants, page and tail controls, interaction snapshot/delta ordering,
cursor gaps in both directions, every generic and structured failure, token
control characters, arbitrary JSON kinds, secret canaries under nested
formatting/logging/encoding, cancellation, concurrent session use, stream
single-reader ownership, close races, and immutable post-close connection
inspection. The gRPC event-stream acceptance additionally uses a deliberately
flow-control-blocked `Send`: reconnect times out without advancing ownership,
canceling and joining the old RPC releases the fence, and the retry advances
exactly one epoch.

Exact whole-repository acceptance commands and timings are recorded with the
commit that accepts this slice. This evidence establishes a public client
contract; it does not claim that a gRPC/local-IPC adapter, endpoint discovery,
managed daemon, or TUI process workflow exists yet.

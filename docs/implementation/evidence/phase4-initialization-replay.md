# Phase 4 protocol-1.3 initialization replay evidence

## Implemented boundary

Protocol minor 3 makes fresh-client allocation and reconnect ownership claims
safe to repeat after a lost response without making all initialization failures
generically retryable. The wire request carries one caller-owned, nonzero
128-bit attempt identity and a successful response echoes it. A 1.3 request
selects minor 3 exactly; legacy 1.0 through 1.2 requests omit the identity, and
no request range may cross the 1.2/1.3 semantic boundary.

The transport-neutral client exposes an immutable, comparable
`InitializationAttemptID`. It has CSPRNG generation, canonical lowercase
hexadecimal parsing, a by-value wire representation, and explicit
`NewInitializeRequestWithAttempt` and `NewReconnectRequestWithAttempt`
constructors. The older constructors remain as deprecated legacy adapters and
cap their advertised range at minor 2, so existing callers cannot accidentally
acquire replay semantics without retaining an identity.

## Server transaction and bounds

After endpoint authentication and fixed request validation, the gRPC adapter
hashes a deterministic Protobuf encoding with only the attempt field removed.
Authentication remains transport metadata and is not part of the semantic
request. A new identity reserves one bounded owner before `Describe`, session
allocation, or reconnect CAS. Exact concurrent duplicates wait on that owner;
reuse with a different fingerprint fails closed.

Fresh installation or reconnect publishes the negotiated session, exact
defensive response clone, and replay link under one registry mutex. The
reconnect gate is released only after that publication. Cancellation before
mutation removes the reservation and lets a waiter become owner. Cancellation
after allocation or CAS retains the exact response, so a later request removes
the transport uncertainty without executing the mutation again.

Pending attempts are capped by negotiated-session capacity, each attempt has at
most 64 waiters, and pending records are never evicted. An active session keeps
its creation response and latest reconnect response; a newer reconnect removes
the superseded reconnect record. This bounds committed records to two per
session and preserves the response required at each live ownership boundary.

## Client retry behavior

`client/grpcclient` sends independent clones of the same wire request and makes
one automatic retry only for gRPC `Unavailable`. It does not retry application
statuses, authentication or validation failures, cancellation, deadlines,
resource exhaustion, or other transport codes. The shared engine validator
must accept the response and prove the echoed attempt identity matches before a
public session exists.

Every call disables gRPC's service-config retry buffer, so an injected transport
policy cannot multiply Spice's explicit attempt count or replay a mutation.
After two lost responses, cancellation, a deadline, or another ambiguous
transport outcome, `InitializationReplayError` carries the exact attempt ID and
permits only the same immutable request to be replayed. Caller context causes
remain discoverable through `errors.Is` without discarding that recovery fact.
Legacy fresh and reconnect transport loss remains non-retryable and uncertain.
The retry ledger is daemon-process-local; the architecture-proof release has no
persistent session store or remote/load-balanced daemon path.

## Acceptance coverage

Tests cover:

- fresh and reconnect schema round trips, exact echo, and defensive cloning;
- missing, short, long, zero, legacy, crossing-range, and mismatched identities;
- canonical/comparable public IDs, entropy failure redaction, and zero retries;
- deterministic request fingerprints and conflict detection;
- duplicate coalescing, waiter and pending capacity, cancellation, owner abort,
  post-CAS response loss, shutdown, and creation/latest-reconnect retention;
- atomic visibility of the ownership epoch and its replay record;
- exact two-call client replay for fresh and reconnect, retry classification,
  typed application failures, constrained cancellation/deadline recovery,
  retry-cap behavior, and hostile gRPC service-config retry policies; and
- shuffled, repeated, race-enabled, 32-bit, Protobuf freshness, architecture,
  vet, lint, security, vulnerability, fuzz, coverage, and offline-vendor gates
  through the repository verifier.

Managed process supervision, generated daemon/TUI distribution targets, and
real installed Windows/Linux terminal acceptance remain the next Phase 4
delivery boundary.

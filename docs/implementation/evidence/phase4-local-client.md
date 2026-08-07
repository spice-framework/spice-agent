# Phase 4 authenticated local-client evidence

## Implemented boundary

This slice connects the existing authenticated engine service to a public,
transport-neutral client without adding a remote network path:

- `daemon/endpoint` owns opaque 256-bit endpoint credentials, canonical
  current-user metadata, process-lifetime identity, bounded publication, stale
  detection, and the cross-process startup lease;
- `daemon/localipc` opens only a private Unix socket on Linux/macOS or a
  current-user Windows named pipe and has no TCP, DNS, proxy, PATH lookup, or
  network fallback;
- `client/grpcclient` validates and translates the complete engine protocol,
  authenticates every RPC, preserves typed recovery facts, and owns no endpoint
  discovery or process policy;
- `client/managed` implements one bounded attach-or-start decision and starts a
  candidate only after discovery returns its exact absence sentinel; and
- `client/localclient` composes endpoint discovery, local dialing, gRPC channel
  ownership, and startup-lock adaptation without introducing another service
  registry.

The operating-system storage implementation shared by endpoint state and run
authority lives in `daemon/internal/userstorage`. It retains the verified
directory descriptor or handle and performs reads, atomic replacements, safe
removal, and stable locking relative to that identity. The extraction does not
change the public run-authority contract.

## Endpoint and transport safety

Endpoint metadata is strict canonical JSON in a current-user-only file. It
contains one local transport kind, an exact address, server build and protocol
facts, a random authentication credential, and a random process-instance
identity. Unknown fields, trailing JSON, noncanonical encoding, invalid tokens,
foreign transports, unsafe addresses, and untrusted filesystem objects are hard
failures. They never become “not found” and therefore never authorize startup.

Publication and discovery serialize metadata transitions with a stable metadata
lock. A single stable daemon-liveness lock is retained for the publication
lifetime, so restarts do not create an unbounded lock-file namespace. Discovery
reads and probes liveness under the metadata lock; stale cleanup removes only a
byte-identical validated record. Startup-lock contention uses nonblocking lock
attempts plus caller-owned cancellation and deadlines. Store close prevents new
work immediately and releases its retained directory only after outstanding
leases and publications drain.

Unix listeners require a clean absolute bounded path, a leaf directory owned by
the effective user with mode `0700`, safe ownership/write semantics for every
ancestor, and a private owned socket. An existing socket is removed only after
an active connection probe proves `ECONNREFUSED`, with an identity check before
unlink. Windows accepts only canonical local `\\.\pipe\spice-agent-*` names,
installs a protected DACL for the current user, and rejects remote pipe clients.

## Client and failure semantics

The gRPC adapter checks negotiated limits before platform integer conversion,
validates every response before constructing public values, and maps daemon
status details without exposing Protobuf types. Event replay is count- and
byte-bounded, strictly contiguous, and completely validated before disclosure.
Live event and interaction streams use one-frame local backpressure, honor each
`Next` context, and are canceled and joined before a local reconnect claim is
sent. Session and stream close remain idempotent, concurrent-safe, local, and
nonblocking.

Local channels use the exact IPC dialer with transport retries and proxies
disabled. One `localclient.Connector` owns one lazy shared channel and gRPC
adapter for an unchanged endpoint identity, so reconnect can fence and join the
prior session's receive goroutines before sending the ownership CAS. Discovery
reuses that connector while every endpoint metadata field remains identical;
identity change or exact endpoint absence closes it. Session close fences only
that protocol session, while `Connector.Close` fences every active session and
then closes the shared transport. Endpoint credentials are opaque noncomparable
values and all credential-bearing client, endpoint, and managed values redact
ordinary formatting, Go formatting, structured logging, and JSON.

Protocol versions 1.0 through 1.2 do not carry an initialization-attempt
identity. A response lost after fresh client allocation or reconnect CAS is
therefore uncertain. The adapter reports every ambiguous initialization
transport failure as non-retryable and does not replay it. Protocol 1.3
initialization-attempt replay is the next bounded protocol slice and must land
before one-command managed startup is presented as loss-safe.

## Verification and exclusions

Tests exercise canonical and hostile metadata, exact absence mapping, active and
stale publication, discovery lifecycle, startup-lock cancellation,
platform transport rejection, local listener permissions, endpoint identity
replacement, authenticated initialization, every unary translation, replay and
tail streaming, status/recovery mapping, malformed responses, cancellation,
resource ownership, redaction, 32-bit bounds, and race behavior.
The controlled gRPC service test proves reconnect waits for the prior stream's
`rpcDone` join before issuing `Initialize`; the real local-IPC acceptance proves
managed discovery reuses that same adapter and reconnect closes a blocked old
`Next` with `client.ErrClosed`.

The exact commit, whole-repository coverage, and full verification timing are
recorded in the implementation ledger only after the final tree passes
`make verify` and is pushed. This slice deliberately does not claim a daemon
process launcher, distribution wiring, protocol-1.3 replay, TUI attachment, or
real installed Windows/Linux terminal workflow.

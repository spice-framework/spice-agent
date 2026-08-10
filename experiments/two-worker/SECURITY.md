# Security boundary

The experiment inherits the authority of its injected `client.Session` and
therefore declares `network.access`, a mutating effect, and idempotent replay.
Those facts are policy metadata, not a sandbox.

The process conformance path uses only the reviewed current-user local IPC
listener and endpoint-token authentication. The random token is sent over
inherited stdin and is absent from arguments, environment variables, stdout,
stderr, events, generated Go, benchmark data, and tool results. Unix socket
directories and Windows named-pipe ACLs are owned by the existing core APIs.

Tasks are trimmed and bounded to 64 KiB. Events and returned text have explicit
bounds. Remote failure details, OS paths, transport messages, credentials, and
provider data are not copied into model-visible failures. Cancellation uses a
separate deterministic operation ID and a two-second local timeout.

There is no containment claim. A production application must decide whether
the explicitly configured worker endpoint and its tools are trusted. Promotion
requires a fresh threat model for remote transport, approval routing, durable
distributed recovery, and worker-specific capabilities.

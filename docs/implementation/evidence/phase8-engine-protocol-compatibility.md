# Phase 8 Engine-Protocol Compatibility Evidence

## Bounded outcome

The first Phase 8 compatibility slice makes the supported engine-protocol
semantics machine-readable and proves them through source-built, real-process
peers on Linux and Windows. It does not claim released-binary N/N-1
compatibility and does not freeze the pre-1.0 Go APIs.

[`engine/v1/compatibility.json`](../../../engine/v1/compatibility.json) is the
canonical compatibility manifest. The repository quality gate rejects unknown
fields, non-canonical JSON, range drift, retry-policy drift, missing platform
coverage, a false released-binary claim, or premature inclusion of the plugin
protocol.

## Matrix and boundary

The matrix exercises two source-built semantic profiles:

- `previous-semantics` caps the server at engine protocol 1.2. Initialization
  has no attempt identity, an acknowledgement loss is ambiguous and
  non-retryable, and the client performs no automatic unavailable retry.
- `current` advertises the production 1.0–1.3 range. Protocol 1.3 requires an
  initialization attempt, permits one byte-identical retry after an
  unavailable response loss, rejects conflicting reuse, and preserves the
  attempt identity when cancellation makes acknowledgement ambiguous.

`grpcserver.NewServer` continues to advertise the complete production range.
The capped 1.2 range is a private dependency seam accepted only when building
the process fixture; it is not a public configuration option and it can only
select a prefix of the production range.

The process matrix builds its child from the exact test source, opens a real
current-user Unix socket or Windows named pipe through `daemon/localipc`, and
uses the public `client`, `client/grpcclient`, and `grpcserver` boundaries. The
authentication token is delivered through the child's private bootstrap input,
never through command arguments or logs. Every case closes the client and
server and asserts Unix socket removal where applicable.

The exact cases are:

1. exact legacy 1.2 negotiation;
2. adaptive current 1.3 negotiation;
3. explicit downgrade only after a typed 1.3-to-1.2 version refusal, with no
   failed-attempt session allocation;
4. definitive authentication failure with no replay classification;
5. one current exact replay after an unavailable post-commit response loss;
6. legacy post-commit ambiguity with no retry;
7. canceled current acknowledgement, conflicting-request refusal, then
   byte-identical recovery.

## Verification

The focused local command is:

```text
go test ./internal/qualitygate ./daemon/grpcserver -run 'TestEngineProtocol|TestServerProtocol|TestEngineProtocolCompatibilityManifest' -count=1
```

The normal `make fast`, `make check`, and `make verify` gates run the manifest
validator and process matrix. Hosted CI supplies the independent Linux and
Windows executions. Exact commit and hosted-run identifiers are recorded only
after those gates are terminal.

## Explicit exclusions and next slice

This is source-built current/previous *semantic* compatibility. No previously
released daemon binary is invoked, so the repository makes no released-binary
N/N-1 claim. The plugin protocol has a separate lifecycle, framing, and
language-fixture matrix; extending its Go/Python process fixtures across
compatible protocol generations is the next bounded compatibility slice.

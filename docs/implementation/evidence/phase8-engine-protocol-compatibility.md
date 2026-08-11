# Phase 8 Engine and Plugin Compatibility Evidence

## Bounded outcome

The first Phase 8 compatibility slices make the supported engine-protocol
semantics and runtime-plugin language breadth machine-readable. Source-built
engine peers prove the 1.2 and 1.3 semantics on Linux and Windows. Independent
Go and Python plugin/v1 process services then prove the same ordinary engine
workflow behind each exact mode. This evidence does not claim released-binary
N/N-1 compatibility and does not freeze the pre-1.0 Go APIs.

[`engine/v1/compatibility.json`](../../../engine/v1/compatibility.json) and
[`plugin/v1/compatibility.json`](../../../plugin/v1/compatibility.json) are
independent canonical protocol manifests under the top-level
[`compatibility/policy.json`](../../../compatibility/policy.json). The repository
quality gate rejects unknown fields, non-canonical JSON, history removal,
range or retry-policy drift, missing platform/language coverage, a false
released-binary claim, engine-coupled plugin versioning, or a false Python
production-host claim.

## Engine matrix and boundary

The matrix exercises two source-built semantic profiles:

- `previous-semantics` caps the server at engine protocol 1.2. Initialization
  has no attempt identity, an acknowledgement loss is ambiguous and
  non-retryable, and the client performs no automatic unavailable retry.
- `current` advertises the production 1.0-1.3 range. Protocol 1.3 requires an
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
6. legacy post-commit ambiguity with no retry; and
7. canceled current acknowledgement, conflicting-request refusal, then
   byte-identical recovery.

## Runtime-plugin breadth

The second slice launches the independent Go and locked Python fixtures as real
plugin/v1 processes. It performs the production initialization handshake and
manifest validation through the public plugin/v1 client, translates only the
validated portable tool catalog, and contributes those tools through an
immutable run-leased `stage.ToolPlanSource`. Runs then use an ordinary
`agent.Engine`, `daemon.RunHost`, authenticated local IPC server, and public
client in exact engine 1.2 or 1.3 mode. There is no in-memory substitute for
plugin/v1 traffic and no mutable runtime registry.

For both languages and both engine modes the matrix proves:

1. the same validated echo result reaches the model continuation;
2. cancellation crosses the engine and plugin stream and produces exactly one
   terminal event for the run, model operation, and tool operation;
3. loss of the fixture process fails closed through ordinary tool and run
   terminal events; and
4. every immutable plugin-generation lease is released after success,
   cancellation, and process loss.

Plugin protocol `1.0.0` remains versioned independently from engine protocol
1.2/1.3. The matrix is language and protocol breadth, not a claim that
`pluginhost.NewHost` launched both fixtures. Host launch, containment, and
generation behavior remain separately proven with the source-built Go
executable. Equivalent Host launching for Python requires a future reviewed,
digest-pinned native Python artifact rather than an environment-dependent
interpreter command.

## Verification

The focused local commands are:

```text
go test ./internal/qualitygate ./daemon/grpcserver -run 'TestEngineProtocol|TestServerProtocol|TestEngineProtocolCompatibilityManifest' -count=1
go test ./internal/pluginconformanceacceptance -run 'TestRuntimePluginLanguagesBehindExactEngineModes/go' -count=1
make verify-python
```

The same gate inventories all 26 written public Go packages, including
generated protocol exports, and computes declaration digests independently for
Darwin/arm64, Linux/amd64, and Windows/amd64. Preview5 is an immutable historical
baseline; current source changes require reviewed platform digests and an
append-only migration record. This review mechanism does not stabilize the API.

The normal `make fast`, `make check`, and `make verify` gates run the manifest
validator and Go process matrix. Hosted CI supplies independent Linux and
Windows execution. `make verify-python` additionally uses only the committed
lock, offline environment, generated protocol bindings, Python unit suite, and
Go acceptance bridge. Exact commit and hosted-run identifiers are recorded only
after those gates are terminal.

## Explicit exclusions

This is source-built current/previous *semantic* compatibility. No previously
released daemon binary is invoked, so the repository makes no released-binary
N/N-1 claim. The plugin protocol has a separate lifecycle, framing, and
compatibility policy. This slice does not add a second released plugin-protocol
generation, stabilize a Go API, or claim native Python Host containment.

The later preview5/preview6 public-module generation proof is deliberately
separate and recorded in
[`phase8-released-generation-compatibility.md`](phase8-released-generation-compatibility.md).
It does not rewrite this source-built semantic history or invent prebuilt
binary assets.

# Phase 5 Candidate and Remote Tool Evidence

## Boundary delivered

This slice connects the pinned-executable security foundation to the frozen
`plugin/v1` protocol without activating a dynamic generation:

- `plugin/host.LocalEndpointFactory` allocates one caller-owned address from an
  exact random launch identity. The production implementation uses the existing
  current-user local IPC scope, never listens, and never falls back to DNS,
  proxies, discovery, or TCP.
- Candidate launch opens and holds the verified executable, generates nonzero
  launch identity, challenge, and 256-bit secret material, sends only the
  bounded public bootstrap record through clearable stdin, and starts with no
  arguments or ambient environment.
- The executable identity and digest are rechecked immediately after process
  ownership transfers. Exact stdout readiness precedes an endpoint-only gRPC
  connection with retries disabled and negotiated message bounds.
- Authenticated initialization validates the complete transcript, exact
  configured manifest identity, negotiated protocol/limits, and the subset of
  explicitly approved tool capabilities.
- Every resource acquired after executable verification is returned through an
  internal candidate even when launch fails. Rejection cleanup retains the
  endpoint and verification lease until `process.Wait` proves containment.
- Each accepted manifest tool is translated to the existing `tool.Tool`
  contract with negotiated concurrency admission, a bounded call lifetime,
  strict stream validation, correlated progress, exactly one terminal outcome,
  and no automatic replay.
- A possibly started mutating call that loses a valid terminal becomes
  `ExecutionUncertain` and `RetryNever`. Definitive pre-admission rejection is
  distinguished only when the host can prove no remote execution began.
- `plugin/host.Set` is an immutable, canonically ordered, complete desired
  configuration. It rejects duplicate stable IDs and manifest names and is not
  an incremental runtime registry.

## Security and ownership limits

The process-launch contract still accepts a pathname, so no portable Go API can
atomically execute the already-open verified file on every supported platform.
The held file-identity lease plus immediate post-launch recheck detects a path
swap and fails the candidate; it does not claim to make that operating-system
race impossible.

Plugins remain trusted native processes. Capability declarations are validated
against explicit configuration but are not a sandbox or permission policy.
The endpoint, candidate, and remote-tool types in this slice do not activate a
generation and cannot mutate the kernel's static Spice graph.

## Acceptance exercised

Focused acceptance covers successful authentication; missing dependencies;
bad entropy; endpoint and process ownership returned with errors; absent
ownership; early exit; stdout contamination; startup cancellation; handshake
tampering; manifest/capability mismatch; private-input destruction; exact
no-argument/no-ambient-environment launch; cleanup retry after failed
containment; cached post-containment cleanup errors; current-user Windows and
Unix address derivation; stale endpoint safety; concurrency admission; stream
ordering; missing and malformed terminals; reporter failure; late
cancellation; and read-only versus mutating uncertainty.

The exact tree must pass:

```text
go test ./plugin/host/... -count=1
go test -race ./plugin/host/... -count=1
go vet ./plugin/host/...
make fast
make check
make verify
```

The commit and complete verification timing are recorded in the implementation
ledger only after the exact tree is committed and pushed.

## Remaining work

This slice does not claim atomic activation, run-scoped generation leases,
active-generation crash replacement, graceful Drain/Shutdown, Spice
auto-configuration, a real installed OS-process launch acceptance, or
last-known-good `spice dev` behavior. Those remain the next Phase 5 boundaries.

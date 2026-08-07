# Phase 5 Generations and Lifecycle Evidence

## Boundary delivered

The production runtime-plugin host now owns complete immutable tool
generations without creating a second application container:

- `plugin/host.Host` starts from the compiled Spice tool dispatcher, stages an
  entire desired `Set` outside its state lock, rejects compiled/runtime and
  runtime/runtime name collisions, applies the generated ordered decorators to
  the complete merged dispatcher, and changes the current generation at one
  lock-protected publication point.
- Every plan identity contains a cryptographic per-host epoch, a monotonic
  activation sequence, candidate launch identities, and canonical tool
  definition fingerprints. An identity is stable for one generation and is
  never reused by another host or later activation.
- `LeaseCurrent` fails closed when the current generation is unhealthy.
  `LeaseGeneration` returns only the exact healthy generation requested and
  only while another lease still retains a retired generation. The final
  release makes that retired generation unavailable before asynchronous
  cleanup begins, preventing resurrection.
- Existing leases keep their exact dispatcher and definition snapshot across
  later activations. A plugin crash does not redirect or replay an existing
  call and never falls back silently to the compiled-only dispatcher.
- Candidate execution admission closes before remote `Drain`. Drain waits for
  every locally admitted call, validated `Drain` and `Shutdown` responses are
  each attempted at most once, and a successful shutdown receives a bounded
  process-exit grace period before forced containment.
- Retirement releases only constant-time reference bookkeeping on the run
  path. Reverse-order Drain, Shutdown, connection/process containment,
  endpoint release, and executable-lease release occur asynchronously with
  bounded contexts. Failed containment remains host-owned for an explicit
  later `Close` retry.
- `Host.Close` rejects new activation and leasing, cancels and joins staging,
  waits for live plan leases, serializes concurrent close attempts, and clears
  the private host epoch only after every owned generation is contained.

The kernel still sees only `stage.ToolPlanSource` and ordinary
`stage.ToolDispatcher` values. There is no `RuntimeGraph`, runtime service
registry, reflection lookup, or mutation of generated Spice DI.

## Failure and concurrency acceptance

Focused tests cover complete-set invisibility while staging; cancellation and
candidate-position failure; atomic current preservation; compiled/runtime
collisions; exact retired leases and the zero-reference no-resurrection
boundary; active crash fail-closed behavior; stable existing lease identity;
decorator application after the complete merge; cross-host and repeated-set
plan identity uniqueness; retained-generation bounds; constant-time final
release; reverse cleanup; failed containment retry; concurrent activation,
lease, release, crash, and close; pre-canceled close; and serialized concurrent
close calls.

Candidate lifecycle tests additionally cover local call admission during
drain, delayed graceful process exit, malformed/status/transport failures,
drain and shutdown timeouts, process and connection failure, ownership retained
until `Wait` proves containment, terminal endpoint-release failure, private
error redaction, and idempotent shutdown. Remote execution tests preserve the
typed uncertain/non-retryable result for any possibly started mutating call
without a valid terminal response.

The exact tree is accepted only after these commands pass:

```text
go test ./plugin/host/... -count=20
go test -race ./plugin/host/... -count=5
go vet ./plugin/host/...
make fast
make check
make verify
```

The implementation ledger records the commit and complete gate timing only
after the exact tree is committed and pushed.

## Remaining work

This slice deliberately does not auto-restart a crashed current generation.
New leases fail until a caller activates a fresh complete `Set`; an unsuccessful
replacement leaves the previous generation identity unchanged. Bounded
restart/recovery policy, public health projection, Spice auto-configuration,
distribution cutover, real OS-process activation acceptance, and last-known-
good `spice dev` remain later Phase 5 boundaries.

Because a dynamic plan identity contains the live host epoch, a snapshot that
names such a generation can resume only while that exact healthy generation is
retained by the same host. Process restart never reconstructs or substitutes a
plugin generation.

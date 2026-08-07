# Phase 5 Recovery and Health Evidence

## Boundary delivered

`plugin/host.Host` now owns one bounded automatic recovery controller without
creating a second runtime registry or changing the compiled Spice graph.

- `RestartPolicy` is immutable and validated. Its zero value disables recovery;
  the explicit production default is three attempts, 250 ms initial backoff,
  one-second maximum backoff, and a 30-second attempt deadline. Attempt one is
  immediate and the hard limit is eight attempts.
- A successful explicit `Activate` retains a defensive clone of the complete
  desired `Set`. Recovery stages that whole set invisibly and publishes only if
  the exact current generation pointer and plan identity, desired revision, and
  explicit-activation revision still match.
- A current candidate failure immediately makes new current/exact leases fail
  closed. Existing leases retain their original plan ID and dispatcher. A
  recovered set receives a distinct plan ID; no call is redirected or replayed.
- Explicit activation cancels or stales an in-progress recovery. If that
  explicit replacement fails, the last successfully activated desired set is
  eligible for a fresh recovery episode. Successful recovery and each later
  crash reset the bounded episode counter.
- Shutdown cancels and joins recovery during either staging or backoff, then
  preserves the existing generation/candidate cleanup ownership rules. No
  recovery attempt owns an unbounded queue or a watcher goroutine.
- `Health` is an immutable, lock-only snapshot. It never polls a candidate or
  schedules work. It exposes only fixed ready/degraded/recovering/unavailable/
  stopping/stopped states, fixed issue codes, the current plan ID, restart
  counters, active leases, and retained-generation count. Arbitrary process,
  endpoint, path, environment, manifest, digest, handshake, and error text is
  excluded from formatting and JSON.

Core and leaf auto-configuration retain the zero disabled policy. Merely blank
importing `plugin/host/autoconfigure` does not silently enable process restart.
The reference distribution is responsible for explicitly contributing its
chosen policy in its generated Spice graph.

## Failure and concurrency acceptance

Tests prove complete two-process set replacement, distinct plan identity, old
lease stability, caller-mutation isolation, disabled/exhausted policies, exact
second/third-attempt backoff, attempt deadlines, explicit-activation
precedence, failed-explicit recovery, new-episode reset, retired-crash
isolation, no mutating-call replay, cleanup issue reduction, and Close during
both staging and backoff. Seeded secret values are absent from every Health
format and JSON path.

A combined race test exercises Health, leasing, current crash, explicit
activation, recovery cancellation, and concurrent Close. Repeated package race
testing also found and corrected a pre-existing initialization/cleanup race:
the initializer watcher now captures stable owned process/stdout handles before
cleanup may clear candidate fields.

Focused acceptance on the implementation tree:

```text
go test ./plugin/host -count=1
go test -race ./plugin/host -count=20
go vet ./plugin/host
golangci-lint run --timeout=10m ./plugin/host/...
nilaway -include-pkgs=github.com/spice-framework/spice-agent ./plugin/host/...
make fast
make check
```

The implementation ledger records the final `make verify` result and commit
only after the exact tree is committed and pushed.

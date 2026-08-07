# Phase 4 exact run-identity ledger evidence

## Outcome

`agent.Engine` now owns one exact in-memory run-identity ledger. Its validated
limits default to 65,536 entries and 16 MiB, are hard-capped at 1,048,576
entries and 256 MiB, and charge 128 bytes plus the exact run-ID byte length per
entry. The ledger contains no TTL, LRU, Bloom filter, probabilistic eviction,
filesystem access, or background goroutine.

Preparation atomically reserves identity. Aborting an uncommitted preparation
removes that exact reservation. An inert `CommitPaused` remains reserved while
it is registered behind the execution gate: `Activate` changes it to active,
whereas a side-effect-free inert abort reclaims it without inventing a terminal
history. Terminal finalization changes active to tombstone before `Run.Wait`
can return. Duplicate preparation, active execution, and unretired terminal
history therefore all fail closed with the existing deterministic duplicate
diagnostic. Capacity produces `ErrRunIdentityCapacity` plus exact bounded
dimension, limit, observation, and aggregate statistics without retained IDs.

Each registered `Run` exposes one opaque `RunIdentityRetirement` capability.
It can remove only the matching terminal generation, rejects active or stale
generations, and is concurrency-safe and idempotent after success. The token
and run identity have no exported representation. The kernel deliberately does
not decide external durability and does not expose retirement from an
uncommitted or inert `PreparedRun`.

## Daemon authority integration

`daemon.RunHost` is the first retirement authority. Its monitor waits for the
kernel terminal, durably issues the terminal authority envelope, closes that
authority, publishes the bounded terminal cache entry, and drains the accepted
interaction binding. Only if every one of those boundaries succeeds does it
retire the exact kernel tombstone. Snapshot export failure, authority failure
or uncertainty, cleanup failure, or retirement failure retains identity and
adds an existing fixed health degradation reason.

Engine identity capacity is translated to `RunHostCapacityError` with resource
`run identities`. It is treated like active host capacity: the operation-ledger
attempt is abandoned before an external commit and can retry under the same
operation ID once a durable retirement creates space.

## Verification

Focused tests prove exact default and hard limits, fixed byte charging,
reservation and abort reclamation, duplicate preparation, active-to-tombstone
ordering before `Wait`, active retirement rejection, terminal reuse only after
retirement, stale-handle fencing across reuse, concurrent idempotent retirement,
daemon capacity translation and same-operation retry, successful daemon-owned
retirement, and tombstone retention on terminal-authority or cleanup failure.
The affected `agent` and `daemon` packages pass ordinary and race-enabled tests.

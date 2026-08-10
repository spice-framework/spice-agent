# Two-worker distributed extension experiment

This removable Phase 7 module proves that two Spice Agent workers cooperate
without adding a parent/child run, subagent, swarm, scheduler, dynamic registry,
or new wire contract to the kernel.

`worker.delegate` is an ordinary constructor-injected `tool.Tool`. It receives
one public `client.Session`, starts the exact generated definition with a
deterministic operation ID, tails ordinary sequenced events, returns bounded
text at the run terminal, and propagates caller cancellation through the normal
`Session.Cancel` mutation. Repeating the same tool call reuses the same remote
start identity; an ambiguous mutation outcome is never retried automatically.

The decisive conformance test launches a second OS process containing an
ordinary public `daemon.RunHost`. The two processes communicate over the
existing current-user Unix socket or Windows named pipe and the existing random
endpoint token. The token travels only through inherited stdin. A wrong token
is rejected before the real injected Session completes a canceled run and a
successful run. No test opens TCP or accesses the Internet.

The committed `TwoWorkerProof` generated application proves normal Spice
interface/map injection. It contains no runtime lookup and is generated from
valid Go comments exactly like a product application.

## Deliberate limits

- This is an SDK stress prototype, not a production worker pool.
- It delegates one bounded text task to one explicitly configured definition.
- The owning application creates, authenticates, reconnects, and closes the
  injected Session; the tool never discovers or starts a daemon.
- There is no durable distributed transaction. Once a remote start might have
  committed, transport loss is reported as uncertain and non-retryable.
- Provider selection, planning, fan-out, scheduling, and run lineage remain
  application concerns built from ordinary runs, tools, messages, and events.

## Verification and deletion

`make fast`, `make check`, `make verify`, and `make benchmark` are deterministic,
offline after dependency bootstrap, and also entered by the root quality gate.
The module pins released `spice-agent v0.1.0-preview.5`, has no `replace`, and
commits its complete vendor and generated trees.

Delete this directory and its root ledger/evidence links to remove the proof.
No core package, protocol schema, root dependency, release metadata, or runtime
artifact depends on it.

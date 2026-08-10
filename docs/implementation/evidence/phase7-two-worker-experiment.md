# Phase 7 two-worker experiment evidence

## Boundary

The `experiments/two-worker` nested module consumes only released public
contracts from `spice-agent v0.1.0-preview.5`, contains no `replace`, and leaves
root product dependencies and releases unchanged. Its generated
`TwoWorkerProof` application injects a public `client.Session` into an ordinary
`tool.Tool` and exposes the canonical `worker.delegate` bean without a runtime
registry.

There is no parent/child run, subagent, swarm, scheduler, worker pool, package
scan, protocol revision, or kernel API. Delegation is one normal tool call that
starts one exact generated definition, consumes normal events, and returns one
bounded result.

## Proven invariants

- the same tool call deterministically reuses its remote start operation ID;
- model input, returned text, event count, and progress are bounded;
- a definitive pre-start failure may retry, while any possibly committed
  remote mutation is reported uncertain and never retried automatically;
- caller cancellation uses a distinct deterministic `Session.Cancel` mutation;
- concurrent calls share only the thread-safe injected Session;
- a second OS process builds a normal public `daemon.RunHost` and communicates
  over the existing current-user Unix socket or Windows named pipe;
- its random endpoint token enters through inherited stdin, a wrong token is
  rejected, and no credential or OS path enters events, tool output, generated
  Go, stdout, or stderr;
- the real process path cancels a blocked remote model operation, observes zero
  active runs, then completes a subsequent delegated run and shuts down cleanly;
  and
- compatibility, generation, offline vendor, race, coverage, and provisional
  benchmark paths are owned by the deletable experiment.

## Related alternate-client evidence

The other client-side Phase 7 proof is independently owned by the TUI
repository. Commit `0bacac3d5a2541abfde41fd9686b763f622f84c0` adds a removable
standard-library semantic shell pinned to released TUI contracts. It imports no
Bubble Tea and emits deterministic portable JSONL. Core records that evidence
without importing or duplicating its implementation.

## Verification and deletion

The nested `make fast`, `make check`, `make verify`, and `make benchmark` are
entered by repository-owned workflows where applicable. Initial Windows
allocation/latency samples are recorded in the experiment benchmark guide and
remain non-gating.

This proof does not stabilize a worker, scheduler, delegation, or distributed
recovery API. Delete the nested directory plus these ledger links to remove it;
no core package, schema, root module graph, or release artifact changes.

# Phase 7 Kernel Runtime Baseline

## Contract

`make benchmark` is the repository-owned, offline, single-CPU kernel comparison
command. It runs four public-runtime paths for exactly 500 iterations and five
independent samples with allocation reporting:

- engine construction plus an empty bounded close;
- one provider-neutral text run through its authoritative terminal;
- one compiled tool call, typed start/terminal occurrences, and continuation;
- cooperative cancellation from an active model stream through the run
  terminal.

The quality gate rejects a missing benchmark or sample, computes the median of
time, bytes, and allocations independently, and rejects the stable ceilings in
`benchmarks/budgets.json`. The same budget step is mandatory in `make verify`.
The command uses the
committed vendor graph with `GOPROXY=off`, `GOTOOLCHAIN=local`, and `GOWORK=off`.
It does not contact a provider, launch a plugin, start a daemon, or include model
latency.

## Windows baseline

The first baseline was captured on Go 1.26.5, Windows/amd64, an AMD Ryzen 9
5900X, from the candidate tree based on parent `54bb8c4`:

| Path | Five ns/op samples | Median ns/op | Steady B/op | Steady allocs/op |
| --- | --- | ---: | ---: | ---: |
| Engine construct/close | 2485, 1696, 1521, 1581, 1523 | 1581 | 1664 | 29 |
| Text run | 22825, 19287, 18597, 20500, 18556 | 19287 | 11193 | 140 |
| Compiled tool round | 51857, 51585, 63636, 67671, 52205 | 52205 | 27785 | 383 |
| Cancellation | 23325, 21901, 22855, 30383, 33860 | 23325 | 12623 | 160 |

The first sample may include one-time process/runtime warming; all five values
remain recorded rather than selectively discarding it. These results show the
kernel-only paths are far below the provisional millisecond-scale product
budgets, but they do not prove full application startup, daemon connection,
runtime-plugin RPC, TUI rendering, or cross-process p95 latency.

## Regression policy

Comparable runs use the same Go version, GOOS/GOARCH, CPU, power policy, command,
and clean source tree. A median time increase above 20% or steady allocation
count increase above 10% is material and requires investigation and recorded
rationale before release. Absolute machine-specific values are evidence, not a
portable regression baseline. Cross-host enforcement instead uses the reviewed
absolute ceilings in the canonical budget manifest. A ceiling change requires
measured evidence and reviewed rationale in the manifest and compatibility
gate; widening only the JSON fails closed. Distribution-level startup,
connection, event-latency, and cancellation budgets remain separate
installed-artifact proofs.

## Stable budget adoption

The preview.6 candidate tree at `85ab0ecf9101de2765616af15c92d4fd333f979c`
replayed the exact command on the original Windows/amd64 Ryzen 9 5900X host.
Median time was 1,673 ns/op for construction, 17,186 for text, 49,053 for the
tool round, and 20,271 for cancellation. Median bytes/allocations were
1,664/29, 11,193/140, 27,785/383, and 12,623/160 respectively. Every path
remained below both its original material-regression threshold and its stable
cross-host ceiling before the budget became mandatory.

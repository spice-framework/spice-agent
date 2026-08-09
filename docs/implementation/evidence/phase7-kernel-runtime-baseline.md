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

The quality gate rejects a missing benchmark or sample. The command uses the
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
flaky per-commit pass/fail threshold. Distribution-level startup, connection,
event-latency, and cancellation budgets remain separate installed-artifact
proofs.

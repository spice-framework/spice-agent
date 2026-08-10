# Planning benchmark evidence

`make benchmark` runs five 500-iteration, single-CPU samples for canonical
64-step finalization and attach/extract. Initial targets are below 1 ms for
64-step finalization and below 500 microseconds for attach/extract, excluding
application planner work. They remain observational until Linux and Windows
baselines establish stable thresholds.

Record exact Go version, platform, processor, nanoseconds, bytes, and
allocations before promotion. Security and deterministic canonical validation
must never be removed to meet a timing target.

Initial Windows amd64 baseline (Go 1.26.5, AMD Ryzen 9 5900X, 2026-08-10):

| Benchmark | Five-sample range | Bytes/op | Allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkFinalize64Steps` | 24,149–28,261 ns/op | 31,161–31,217 | 44 |
| `BenchmarkAttachExtract` | 17,116–18,225 ns/op | 12,272 | 157 |

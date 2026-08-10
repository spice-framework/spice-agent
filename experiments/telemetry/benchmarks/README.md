# Provisional telemetry benchmarks

Run `make benchmark` for deterministic single-CPU, five-sample baselines of 500
HMAC envelope transformations and local JSONL projections. The provisional
budgets are under 10 microseconds and 2 KiB per envelope transformation, and at
least 50,000 bounded local JSONL records per second on the reference machine.

These values are experimental evidence, not compatibility promises. Record the
exact Go version, platform, CPU, ns/op, bytes/op, and allocations/op before
setting a regression threshold. Network and model latency are excluded because
this module performs neither operation.

Initial Windows amd64 baseline on Go 1.26.5 (Ryzen 9 5900X), five 500-operation
single-CPU samples:

- envelope transformation: 1.32–1.96 microseconds, 1,920 bytes, 26 allocations;
- deterministic JSONL export of a five-record batch: 2.86–3.87 microseconds,
  2,591 bytes, 17 allocations (more than 1.2 million records/second at the slow
  sample).

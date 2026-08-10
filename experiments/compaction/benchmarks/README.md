# Provisional compaction benchmark

Run `make benchmark` to execute `BenchmarkCompact` for five deterministic
single-CPU samples of 500 rewrites over 32 complete tool rounds. The benchmark
reports time and allocations but is not a release gate while the contract is
experimental. A future promotion proposal must record representative request
sizes, provider-specific token savings, and a material-regression threshold;
model latency is deliberately outside this local transformation benchmark.

Initial Windows amd64 baseline on Go 1.26.5 (Ryzen 9 5900X), five 500-rewrite
samples over 32 complete tool rounds: 179–193 microseconds per rewrite,
210,978–210,980 bytes and 2,097 allocations per operation. These values are
evidence, not a compatibility promise.

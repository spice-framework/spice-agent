# Provisional permission benchmarks

These benchmarks measure the terminal guard's allow, deny, and synchronous
prompt paths. They are diagnostic baselines, not compatibility promises or
hard thresholds. They use fixed in-memory policies, requests, responses, and
tools; they require no credentials, process launch, filesystem access, or
network access.

Run five deterministic samples on one logical CPU:

```text
make benchmark
```

The exact underlying command is:

```text
go test -run=^$ -bench=^BenchmarkGuard -benchmem -benchtime=500x -count=5 -cpu=1 .
```

Record the Go version, OS/architecture, CPU, and median `ns/op`, `B/op`, and
`allocs/op` only after the implementation is green. Re-baseline when the core
dispatch scope or interaction contract changes; do not turn noisy workstation
results into gates.

## Initial provisional baseline

Recorded 2026-08-09 with Go 1.26.5 on Windows/amd64, AMD Ryzen 9 5900X,
`benchtime=500x`, five samples, and one logical CPU:

| Path | Median ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| allow and execute | 5,195 | 3,144 | 66 |
| deny | 4,105 | 2,808 | 56 |
| synchronous prompt approval and execute | 6,815 | 4,144 | 88 |

These values include the released core pipeline's defensive snapshots and
validation. They are descriptive workstation medians only.

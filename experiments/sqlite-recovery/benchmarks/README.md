# Provisional SQLite recovery benchmarks

These deterministic offline benchmarks measure required-observer commit,
checkpoint commit, and full restore validation. They are diagnostic baselines,
not compatibility promises or hard thresholds. Each uses a private temporary
WAL database and fixed non-secret data.

Exact command:

```text
go test -run=^$ -bench=^Benchmark -benchmem -benchtime=50x -count=5 -cpu=1 .
```

Initial provisional budgets for design review are observer commit below 5 ms,
checkpoint commit below 20 ms, and restore validation below 10 ms on a local
developer SSD. They are not gates until Linux and Windows CI baselines establish
variance.

Recorded 2026-08-09 with Go 1.26.5 on Windows/amd64, AMD Ryzen 9 5900X,
`benchtime=50x`, three samples, one logical CPU:

| Path | Median ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| required observer commit | 3,347,870 | 2,431 | 50 |
| checkpoint commit | 9,905,286 | 11,187 | 238 |
| restore validation | 3,264,316 | 10,162 | 184 |

Re-baseline after schema, snapshot, occurrence, or durability-policy changes.

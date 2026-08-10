# Provisional two-worker baselines

Run from this module with Go 1.26.5 and committed vendor contents:

```text
GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=vendor GOPROXY=off \
  go test -run=^$ -bench=^BenchmarkDelegate -benchmem \
  -benchtime=500x -count=5 -cpu=1 .
```

The initial Windows 11 amd64 sample on an AMD Ryzen 9 5900X recorded these
five-sample medians on 2026-08-10:

| Path | Median | Bytes/op | Allocs/op |
| --- | ---: | ---: | ---: |
| completed in-memory Session event stream | 4,749 ns/op | 3,040 | 24 |
| caller cancellation plus remote cancel dispatch | 2,852 ns/op | 1,920 | 28 |

These isolate Delegate translation and do not include local IPC, gRPC, model,
or scheduler time. They are provisional evidence, not compatibility promises or
hard thresholds. Establish cross-platform distributions and a material-change
policy before adopting a regression gate.

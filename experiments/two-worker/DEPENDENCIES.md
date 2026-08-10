# Dependency review

The module pins the released Apache-2.0 Spice and Spice Agent previews and the
same generation toolchain used by core. The only directly imported transport
dependency is the existing Apache-2.0 gRPC-Go `v1.83.0` graph already reviewed
and vendored by Spice Agent. Windows local IPC selects the existing MIT-licensed
`go-winio` dependency transitively; Unix selects `x/sys`.

There is no runtime dependency discovery, module download, reflection-based
lookup, TCP listener, DNS, credential provider, database, telemetry exporter,
or plugin loader. The root bootstrap is the only network-enabled dependency
step. Ordinary experiment tests, generation checks, race, coverage, and
benchmarks use `GOWORK=off`, `GOPROXY=off`, `GOTOOLCHAIN=local`, and committed
vendor contents.

This dependency cost is selected only by the nested experiment. Removing the
directory removes the entire graph from the repository quality path without
changing the core module.

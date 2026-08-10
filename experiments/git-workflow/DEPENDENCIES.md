# Dependency review

The module pins released `github.com/spice-framework/spice-agent
v0.1.0-preview.5` and `github.com/spice-framework/spice v0.1.0-preview.2`
without `replace`. Generated composition uses the same pinned Spice toolchain
as the other nested experiments.

`golang.org/x/sys v0.47.0` is the only added runtime dependency. It is the Go
project's BSD-3-Clause system-call module and supplies maintained Windows Job
Object and Unix signal/process-group primitives unavailable in the standard
library. The experiment uses no Git library, network client, credential helper,
shell, database, telemetry SDK, or parser dependency. Cancellation and cleanup
remain owned by the narrow public `process.Process` contract.

Exact module and go.mod sums are locked in `compatibility.json`; `go.sum`,
`vendor/modules.txt`, notices, offline tests, govulncheck, gosec, and the root
dependency bootstrap/verification gate cover the graph.

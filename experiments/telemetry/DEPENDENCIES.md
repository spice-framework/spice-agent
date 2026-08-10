# Dependency review

The runtime implementation uses only the Go standard library and exact public
packages from `github.com/spice-framework/spice-agent v0.1.0-preview.5` and
`github.com/spice-framework/spice v0.1.0-preview.2`. The generated proof uses
the repository's existing exact annotation/toolchain graph.

There is no OpenTelemetry SDK, logging SDK, database, file watcher, network
transport, retry library, or global provider dependency. Standard-library HMAC,
SHA-256, JSON, synchronization, and contexts own the implementation.

The compatibility manifest, `go.sum`, and committed vendor tree lock the exact
graph. `replace` directives are rejected. A future OpenTelemetry adapter must
remain isolated and pass a new license, maintenance, cancellation,
cardinality, privacy, and default-network review.

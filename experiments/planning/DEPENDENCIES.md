# Dependency review

The experiment pins only released ecosystem modules:

- `github.com/spice-framework/spice-agent v0.1.0-preview.5`
- `github.com/spice-framework/spice v0.1.0-preview.2`

The runtime implementation uses their public `agent`, `message`, and `stage`
packages plus the Go standard library. There is no planning library, model
provider, network, storage, process, or telemetry runtime dependency.

The existing Toolchain pseudo-version is build-time-only and generates
committed ordinary Go. All sums and vendor contents are committed; normal
verification uses `GOPROXY=off`, `GOWORK=off`, and `-mod=vendor`.

Spice and Agent are Apache-2.0. Their existing dependency and security reviews
remain authoritative. Removal requires deleting this nested module and its root
quality-gate entry; no data migration or external cleanup exists.

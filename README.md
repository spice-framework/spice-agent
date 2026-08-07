# Spice Agent

Unified documentation: [spiceframework.dev/agent](https://spiceframework.dev/agent/).

Spice Agent is a Go-native agent SDK assembled by Spice's generated, exact-type
dependency graph. The repository owns provider-neutral messages, model and tool
contracts, deterministic execution, events, interactions, protocols, and
conformance support. Providers, coding tools, user interfaces, and distributions
live in independently versioned repositories.

This project is pre-1.0. See [the implementation ledger](docs/implementation/README.md)
and [architecture](ARCHITECTURE.md) before adopting its APIs.

```text
make tools-bootstrap # explicit fresh-cache dependency download
make proto           # regenerate committed Protobuf Go with local tools
make fast            # affected feedback
make check           # broad edit loop, including protocol compatibility
make verify          # commit gate
```

The repository also owns the transport-only `common/v1` and `engine/v1`
Protobuf contracts. These packages define daemon/client wire messages and
validation; they do not implement a daemon and are not imported by the kernel.
Normal generation and verification are offline. See
[verification](docs/verification.md) for the one explicit bootstrap exception.

The standard-library-only `client` package is the public high-level port for
TUI and other local clients. It models one negotiated ownership epoch,
concurrent operations, explicit replay/tail controls, pending interactions,
snapshots, health, and lossless typed recovery without exposing gRPC or
Protobuf. The `daemon` package contains the matching transport-independent host
primitives, bounded reconnect-safe mutation/stream gates, and an OS-backed
snapshot/run authority; local IPC and protocol translation remain separate
adapters.

Daemon authority publication is explicitly two-phase. Prepared kernel runs can
be registered behind an inert activation gate, allowing a durable authority
transition to complete before any event, provider, tool, observer, or
interaction becomes visible. Cancellation is latched until the host explicitly
activates or aborts that gate.

Tool contracts fail closed: each definition declares `read_only` or `mutating`
effect, replay safety, and a canonical capability set. Tools return ordinary
model-visible results separately from bounded, call-correlated infrastructure
failures, including explicit uncertain mutation outcomes.

Every run leases one source-owned immutable `stage.ToolPlanLease` before the
engine allocates an ID or commits an event. `Run.PlanIdentity` combines the
compiler-generated identities of every executable static bean, an explicit
snapshot-compatibility identity, the exact tool-plan generation, and canonical
definition fingerprints. Portable import is disabled when that compatibility
identity is absent; configured import rejects static mismatches before leasing
the exact generation. Lease release is bounded and happens once before the
authoritative terminal is chosen, so failure becomes `RunFailed` rather than
hidden cleanup.

Spice Agent is licensed under Apache-2.0.

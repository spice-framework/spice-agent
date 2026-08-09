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

Snapshot publication is likewise typed and fail closed: the daemon accepts one
validated kernel snapshot and derives every signed envelope field from it.
Callers cannot supply competing run, sequence, lifecycle, or payload metadata,
and ambiguous durable outcomes are never automatically replayed.

Tool contracts fail closed: each definition declares `read_only` or `mutating`
effect, replay safety, and a canonical capability set. Tools return ordinary
model-visible results separately from bounded, call-correlated infrastructure
failures, including explicit uncertain mutation outcomes.

Every run leases one source-owned immutable `stage.ToolPlanLease` before the
engine allocates an ID or commits an event. `Run.PlanIdentity` combines the
compiler-generated identities of every executable static bean, an explicit
snapshot-compatibility identity, a workspace SHA-256, the exact tool-plan
generation, and canonical
definition fingerprints. Portable import is disabled when that compatibility
identity is absent; configured import rejects static mismatches before leasing
the exact generation. Lease release is bounded and happens once before the
authoritative terminal is chosen, so failure becomes `RunFailed` rather than
hidden cleanup.

Every engine dispatch also carries those immutable facts plus run, turn, and
interaction authority through `stage.ToolDispatchScope`. Ordered
`ToolDispatchGuard` beans form the terminal policy seam immediately above the
merged compiled/runtime dispatcher. Spice Agent ships no permission policy by
default; trusted decorators remain outside that seam with their existing
ordering and trust contract. A guard that needs user input calls the scope's
run-owned interaction requester, which delegates to `Run.Interact` without
exposing forgeable broker scope. The normal exactly-once interaction lifecycle,
snapshot refusal, cancellation join, and terminal ordering still apply.

Before that seam is entered, the kernel commits a strict typed `ToolStarted`
occurrence containing only call identity, declared definition security facts,
and exact plan/workspace authority. Arguments, paths, schemas, descriptions,
provider payloads, and secrets cannot enter this durable record. Unknown model
tool names fail before guard or executable dispatch. The current daemon wire
retains its legacy `call_id`/`name` projection.

Tool completion and failure events use a second strict versioned occurrence
that closes the call with identity, name, exact terminal kind, and optional safe
execution-state/retry facts. It cannot contain result output, problem/error
text, paths, or secrets. Daemon clients retain their legacy payload through an
explicit projection with a fixed safe failure message.

The production runtime-plugin host merges a complete authenticated runtime-tool
set with the compiled Spice dispatcher and atomically publishes it for future
runs. Existing runs retain their exact generation. Candidate crashes fail new
leases closed, mutating calls with unknown outcomes are never replayed, and the
last lease schedules bounded Drain, Shutdown, and process containment without
blocking run finalization. Dynamic generations never mutate generated Spice DI.
Applications enable the host through an explicit blank import of
`plugin/host/autoconfigure`; its replaceable defaults remain ordinary generated
beans rather than runtime discovery.

## Release contract

`spice-release.json` is inert, canonical metadata for the centrally authorized
`go-module-v1` release profile. `make verify-release` runs the repository's
complete local gate. The organization release authority independently binds
the repository name, module path, exact preview version, required module graph,
commit, and tag before it creates any artifact or release.

Spice Agent is licensed under Apache-2.0.

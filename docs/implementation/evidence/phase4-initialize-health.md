# Phase 4 authenticated Initialize and Health evidence

## Bounded outcome

`daemon/grpcserver.NewServer` is now the only supported engine-service assembly
path. It installs unary and streaming endpoint authentication together, applies
global receive/send bounds, registers the generated service, and opens no
listener. Authenticated clients can negotiate a fresh or reconnected ownership
epoch and query readiness over real gRPC.

## Proven invariants

| Invariant | Evidence |
| --- | --- |
| Mandatory security | Authentication middleware remains private and the public server constructor installs both RPC shapes before registering `engine/v1`. Service methods independently require the private authenticated-context marker. |
| Pure preflight | Initialize converts the current validated `RunHostDescription`, then calls `PreflightInitialize` before `SessionStore.Fresh` or `ReconnectContext`. Invalid protocol, limits, or capabilities allocate no client. |
| Exact ownership | Fresh clients begin at epoch one. Reconnect advances the stable identity by exactly one through SessionStore CAS and replaces the private registry entry only from the expected prior epoch. |
| Defensive registry | Each bounded registry entry retains the exact daemon session plus a validated cloned `InitializeResponse`. Caller mutation and concurrent lookup cannot change stored build, protocol, limits, health, or definitions. Registry close drops only its own references and never fences its non-owned SessionStore. |
| Recovery without disclosure | A known stale owner receives exact expected and observed epochs. An unknown identity receives a generic unavailable status with no invented authoritative epoch. |
| Health authority | Health validates against server bounds, resolves the exact negotiated epoch, rechecks SessionStore to close reconnect races, and calls `RunHost.Health`; it reuses the registered server/protocol contract and current global readiness. |
| Error separation | Negotiation and application failures are bounded typed response statuses with nil gRPC error. Authentication, canceled RPCs, deadlines, and unavailable transport remain gRPC failures. |
| Real boundary | `bufconn` tests execute generated Initialize and Health stubs through serialization, interceptors, registry, SessionStore, and the host port. Repeated race tests cover fresh initialization, mutation isolation, reconnect, stale health, unknown clients, and capacity. |

## Verification

Focused package tests pass repeated shuffled and race-enabled execution plus
`go vet`; repository `make fast` and `make check` pass. The exact commit tree is
required to pass `make verify` before this evidence is complete.

## Deliberate exclusions

No OS listener, Unix socket, Windows named pipe, endpoint metadata file,
discovery, managed startup, lifecycle mutation RPC, snapshot transfer, event or
interaction stream, or client-side gRPC adapter is included. SessionStore still
owns actual ownership contexts; the adapter registry is only a bounded wire-to-
host binding. The service-wide gRPC limit is enforced before decode, and each
implemented handler additionally applies negotiated structural and encoded-size
validation.

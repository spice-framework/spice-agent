# Phase 4 RunHost description evidence

## Bounded outcome

The engine-protocol initialization adapter can now obtain one authoritative
transport-neutral view of the generated definition catalog and daemon readiness
before creating or reconnecting a client session. `RunHost.Describe` does not
allocate, authenticate, reconnect, or mutate session state.

## Proven invariants

| Invariant | Evidence |
| --- | --- |
| Immutable value | `RunHostDescription` is constructed only from a validated `DefinitionSet` and `client.Health`; returned definitions are defensive copies and the exact revision is revalidated. |
| One synchronization boundary | The host lock covers stopping state, degradation reasons, active reservations, and configured limits. Immutable definitions are captured at that same boundary; passive sources run only after release. |
| Generic extension health | `RunHostConfig.HealthSources` is count-bounded and defensively cloned. Every source returns an immutable `HealthContribution` with a closed fixed-code vocabulary, bounded input, canonical sorting/deduplication, and no error or free-form text channel. |
| Containment and stopping | Source panic or forged contribution becomes only `dependency_unavailable`; secret-bearing panic/error text is discarded. Stopping wins without sampling any source and exposes no degraded reasons. |
| Sessionless initialization | Description succeeds after the fixture session store is closed and never asks for client identity or epoch. |
| Health parity | Session-bound `RunHost.Health` validates ownership and then reuses the same private description snapshot rather than maintaining duplicate readiness logic. |
| Cancellation and failure | Nil, already-canceled, and concurrently canceled callers receive no partial value. Invalid description members and forged revisions fail closed. |
| Determinism and concurrency | Internal and contributed degradation reasons are cloned, merged, sorted, and deduplicated. Repeated concurrent readers observe the same definitions, health state, limits, and reasons under the race detector. A reentrant test proves source sampling occurs outside `RunHost.mu`. |

## Verification

Focused daemon tests pass repeated shuffled and race-enabled execution plus
`go vet`. The exact combined authenticated-host foundation tree is required to
pass repository `make verify` before commit.

## Deliberate exclusions

This seam performs no protocol negotiation, Protobuf translation, endpoint
authentication, client-session allocation, gRPC serving, event streaming, or OS
IPC. Those responsibilities remain with the later `daemon/grpcserver` adapter.

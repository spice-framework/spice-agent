# Spring comparison for Agent extensions

This file records conceptual Spring parity relevant to Spice Agent only. The
Spice platform's broader Spring coverage remains owned by the Spice repository.

| Spring concept | Spice Agent experiment | Status and deliberate difference |
| --- | --- | --- |
| `AuthorizationManager` decision | experimental `permission.Policy` | proven for typed tool-dispatch facts; pre-1.0 and not installed by default |
| Method/security interceptor | terminal `stage.ToolDispatchGuard` | proven on canonical compiled and runtime-host generation dispatch; no reflection or proxy |
| User approval flow | run-owned `ToolDispatchScope.RequestInteraction` | proven with normal interaction lifecycle; the policy never receives mutable Broker authority |
| ApplicationContext collection injection | generated `[]stage.ToolDispatchGuard` | proven by committed ordinary Go; no registry, scan, or runtime container mutation |
| `@PreAuthorize` expression policy | no direct equivalent | deliberately deferred until independently authored typed policies show annotation value |
| JVM/process sandboxing | no direct equivalent | explicitly not claimed; interception and capability metadata do not contain arbitrary Go code |
| durable event listener | experimental SQLite required observer | proves synchronous commit-before-ack with exact typed occurrence correlation; persistence remains opt-in |
| restart/checkpoint state | experimental SQLite checkpoint/restorer | explicit safe snapshot return and immutable branch lineage; no transparent context or daemon restart |
| alternate application client | TUI semantic-shell experiment at `0bacac3` | consumes released UI-neutral Session values and portable views without Bubble Tea or executable plugin UI |
| task delegation/executor | experimental `worker.delegate` tool | delegates through an injected public Session to an ordinary second run; no parent/child context, scheduler, or proxy container |
| distributed cancellation | `Session.Cancel` from `worker.delegate` | proven across authenticated current-user IPC and a second RunHost; uncertain mutation outcomes fail closed |

All four Phase 7 experiments are removable without affecting core. Their
completion is evidence for contract review, not an API-stability or Spring
runtime-equivalence claim.

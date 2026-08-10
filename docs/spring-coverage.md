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

The permission and SQLite recovery experiments are removable without affecting
core. An alternate client and a two-worker distributed extension remain the
next Phase 7 proofs before any API stabilization claim.

# Phase 5 Runtime Host Auto-Configuration Evidence

## Boundary delivered

`plugin/host/autoconfigure` is the optional leaf adapter between generated
Spice composition and the runtime-plugin host. It contributes four ordered,
named fallback beans only when an application explicitly blank-imports the
package:

1. `runtimePluginCompiledDispatcher` snapshots the complete generated
   `map[string]tool.Tool`.
2. `runtimePluginEndpointFactory` supplies the replaceable current-user local
   endpoint allocator.
3. `runtimePluginHost` constructs the concrete `*pluginhost.Host` from the
   application-owned protocol build identity, compiled dispatcher, ordered
   dispatch decorators, process launcher, and endpoint factory; generated
   cleanup calls `Host.Close`.
4. `runtimePluginToolPlanSource` exposes that exact host pointer through the
   `stage.ToolPlanSource` interface consumed by the engine.

All four beans use ordinary Spice exact-type injection and fallback selection.
An application can replace any default with an exact typed bean. An absent
mandatory build identity or process launcher leaves construction impossible and
visible through normal Spice graph diagnostics; the adapter performs no
discovery, hidden installation, or network operation.

The adapter package alone imports Spice `starter` and `lifecycle`. The kernel,
stage, protocol, and host implementation packages do not import the adapter.
Construction begins with the compiled-only immutable generation and does not
start a plugin process. Runtime `Set` activation remains an explicit host
operation outside the kernel and static bean graph.

## Acceptance exercised

Tests prove exact descriptor review metadata, names, order, fallback flags, and
factory identities; an empty compiled dispatcher; the concrete current-user
endpoint implementation; failure for each missing mandatory dependency; exact
Host pointer preservation through the interface adapter; a valid initial plan
lease; idempotent generated cleanup; and no process launch during construction.
The package participates in package, race, lint, NilAway, security, coverage,
and offline-vendor verification.

## Remaining work

The reference distribution must blank-import this package, contribute its
application build identity, delete its handwritten static dispatcher/source
factories, regenerate the daemon target, and prove reverse cleanup ordering.
This core adapter does not yet load desired plugin configuration, implement
bounded crash recovery, or expose public health. Those are separate Phase 5
boundaries.

# Phase 5 Runtime Host Auto-Configuration Evidence

## Boundary delivered

`plugin/host/autoconfigure` is the optional leaf adapter between generated
Spice composition and the runtime-plugin host. It contributes five ordered,
named fallback beans only when an application explicitly blank-imports the
package:

1. `runtimePluginCompiledDispatcher` snapshots the complete generated
   `map[string]tool.Tool`.
2. `runtimePluginEndpointFactory` supplies the replaceable current-user local
   endpoint allocator.
3. `runtimePluginRestartPolicy` supplies the valid disabled zero policy. Merely
   enabling auto-configuration never opts an application into process restart.
4. `runtimePluginHost` constructs the concrete `*pluginhost.Host` from the
   application-owned protocol build identity, compiled dispatcher, ordered
   dispatch decorators, exact restart policy, process launcher, and endpoint
   factory; generated cleanup calls `Host.Close`.
5. `runtimePluginToolPlanSource` exposes that exact host pointer through the
   `stage.ToolPlanSource` interface consumed by the engine.

All five beans use ordinary Spice exact-type injection and fallback selection.
An application can replace any default with an exact typed bean. An absent
mandatory build identity or process launcher leaves construction impossible and
visible through normal Spice graph diagnostics; the adapter performs no
discovery, hidden installation, or network operation.

The adapter package alone imports Spice `starter` and `lifecycle`. The kernel,
stage, protocol, and host implementation packages do not import the adapter.
Construction begins with the compiled-only immutable generation and does not
start a plugin process. The disabled fallback policy is visible and replaceable
through generated exact-type composition; a distribution must deliberately
contribute a non-zero `RestartPolicy` to enable automatic recovery. Runtime
`Set` activation remains an explicit host operation outside the kernel and
static bean graph.

## Acceptance exercised

Tests prove exact descriptor review metadata, names, order, fallback flags, and
factory identities; an empty compiled dispatcher; the concrete current-user
endpoint implementation; failure for each missing mandatory dependency; exact
disabled default and explicit non-zero policy injection; exact Host pointer
preservation through the interface adapter; a valid initial plan lease;
idempotent generated cleanup; and no process launch during construction.
The package participates in package, race, lint, NilAway, security, coverage,
and offline-vendor verification.

## Remaining work

The reference distribution must deliberately replace the disabled restart
policy when it wants production recovery and adapt the host's fixed health
states/issues into the daemon's fixed `HealthContribution` codes. This core
adapter does not load desired plugin configuration; explicit Set construction
and activation remain application-owned Phase 5 boundaries.

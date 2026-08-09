# RFC 0009: Terminal Tool Dispatch Guards

- **Status:** accepted as the Phase 7 generic core seam
- **Depends on:** RFC 0001, RFC 0003, RFC 0007, and RFC 0008

## Decision

Every leased compiled or runtime-plugin tool dispatch carries one immutable
`stage.ToolDispatchScope`. The scope contains the run ID, positive turn, exact
leased `PlanID`, combined plan SHA-256, workspace SHA-256 when the engine is
portable, and the validated `interaction.Scope` owned by that run. These are
policy inputs, not a permission grant.

`ToolDispatchGuard` is a narrow terminal interception seam. Spice injects its
ordered collection and `ApplyToolDispatchPipeline` installs it exactly once,
closest to the merged compiled/runtime base. The first guard is outermost among
guards. Existing trusted `ToolDispatchDecorator` values remain outside the
guard layer, with their existing first-decorator-outermost ordering. A trusted
decorator may intentionally short-circuit before terminal policy; replacing
that trust model requires a separate contract and is not silently implied here.

A guard may deny or invoke its bound `ToolDispatchNext` once. The continuation
cannot replace the context, scope, definition, call, or reporter. It closes when the
guard returns, including denial and panic paths; retained, concurrent, second,
or recursive use fails before another execution. Guard panics and dispatcher
panics become fixed secret-safe errors. Guard-supplied results must be valid and
correlated and cannot accompany an error.

The outer composed dispatcher binds the engine-supplied scope to a private
context capability before any trusted decorator runs. The inner guard boundary
requires the exact bound value, so a decorator may retry with the same scope
but cannot substitute a run, turn, plan, workspace, or interaction authority or
drop the binding. Re-entering the composed dispatcher with an already-bound
context fails before guard or tool execution.

Guards request approval or other UI-neutral input only through
`ToolDispatchScope.RequestInteraction`. The scope contains a private pointer
capability for one run-owned `interaction.Requester`; the requester accepts a
request and context but no caller-supplied `interaction.Scope`. The engine binds
that capability to `Run.Interact` when it constructs the run. Capability pointer
identity participates in scope equality, so even a scope reconstructed from the
same public facts and requester cannot substitute the engine-owned scope.
Requester cancellation, validation, correlation, and panic containment fail
closed without exposing recovered values.

The engine constructs the scope and remains the only lifecycle-event owner.
Guards cannot emit `ToolStarted`, tool terminal, turn terminal, or run terminal
events. Denial, panic, cancellation, or malformed guard output returns through
ordinary dispatch failure and the engine finalizes each started lifecycle once.
An interaction requested by a guard uses the normal run lifecycle. Its
`InteractionStarted` and exactly one interaction terminal occur after
`ToolStarted` and before the tool terminal. A pending interaction makes the run
unsafe to snapshot; run cancellation cancels and joins the interaction before
the run terminal.
Before guard entry, the engine commits the strict agent-owned typed
`ToolStarted` occurrence from RFC 0002. It contains the same exact plan and
workspace authority plus bounded definition security facts, never executable
arguments. An undeclared model tool records false/false declaration facts and
fails before any guard or base dispatcher.

## Workspace and snapshot compatibility

Portable engines must provide a canonical lowercase SHA-256 workspace
fingerprint. `PlanIdentity` version 3 incorporates it alongside compiled beans,
snapshot compatibility, exact tool plan, and definitions. Snapshot JSON is
therefore `spice.agent.snapshot/v1alpha3`; v1alpha2 is rejected rather than
guessed. Resume rejects a different workspace before acquiring a tool
generation. Non-portable convenience engines may use an empty workspace only
while snapshot compatibility is also empty.

## Exclusions

This RFC does not install a permission policy, prompt the user, sandbox trusted
code, add retry, or turn capability declarations into enforcement. The first
external permission prototype must use only this public seam and ordinary Spice
DI. Process-launch policy remains a separate future seam.

## Acceptance

Tests cover generated collection injection, compiled and runtime routes,
ordering, exact plan binding, workspace-bound resume, cancellation, denial,
panic secrecy, re-entry, retained and double continuation use, forged results,
concurrency, requester delegation/substitution, interaction terminal ordering,
pending-interaction snapshot refusal, cancellation join, deterministic snapshot
round trips, and engine-owned terminals.

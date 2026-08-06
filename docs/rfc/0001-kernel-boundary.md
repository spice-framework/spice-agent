# RFC 0001: Deterministic Kernel Boundary

- **Status:** accepted for preview
- **Applies to:** `github.com/spice-framework/spice-agent`
- **Stability:** pre-1.0

## Problem

Agent frameworks tend to accumulate provider clients, tool registries, UI state,
persistence, permission policy, and worker scheduling in one mutable runtime.
That makes deterministic testing and safe extension difficult and would bypass
Spice's generated dependency graph.

## Decision

The kernel owns only provider-neutral immutable values and one deterministic
single-agent state machine. `Engine.Start` receives a validated definition and
input. Providers, the canonical tool dispatcher, identifiers, clock, interaction
broker, required observers, and best-effort observers are constructor-injected
typed beans. The engine never receives or exposes a service registry.

The kernel owns:

- immutable run, turn, message, model-operation, tool-call, and interaction IDs;
- bounded messages and model/tool contracts;
- turn/tool-loop state transitions;
- event sequencing and authoritative replay;
- cancellation and panic containment;
- exactly-once lifecycle terminal finalization;
- provider-neutral safe snapshots and import validation;
- immutable static and leased dynamic execution-plan identity per run.

Provider implementations, tool implementations, daemon transport, UI,
persistence, approval policy, sandboxing, Git/MCP/indexing/telemetry, and
multi-agent scheduling remain outside the kernel.

## Trust and failure semantics

Providers and compiled tools are trusted concurrent in-process beans. Context
cancellation is cooperative; the kernel cannot force a function that ignores
context to return. Panics are contained at provider, stream, dispatcher, and
engine boundaries and normalized into terminal failure.

All executable tools traverse one injected `ToolDispatcher`. The dispatcher
publishes an immutable capability/definition snapshot and validates active call
and progress correlation. Definitions explicitly classify external-state
effect and replay safety; capabilities form an unordered set returned in
canonical lexical order. Read-only definitions reject mutation-capable
filesystem, process, network, and environment capabilities. No tool may retain
a reporter after execution.

`tool.Tool.Execute` returns `(tool.Result, error)`. A valid `Result` whose
problem is set is model-visible terminal tool data and may continue the model
loop. An infrastructure failure returns a zero result and exactly one direct,
bounded, call-correlated `*tool.ExecutionError`; wrappers and joined siblings
are rejected. The error distinguishes a definitive
failure from an uncertain mutation outcome and supplies validated retry advice.
The dispatcher rejects untyped, uncorrelated, contradictory, or result-plus-error
outcomes and preserves `errors.Is` cancellation/deadline semantics.

Local event commit precedes required-observer acknowledgement. Once committed,
a sequence is never reused even if an observer reports failure. Bounded terminal
finalization uses a context independent of caller cancellation and surfaces a
typed durability error when completion cannot be established.

## Dependency rules

Kernel packages may depend on other provider-neutral core packages and the Go
standard library. They may not import OpenAI, coding tools, gRPC, Protobuf,
Bubble Tea, OS IPC, SQLite, Spice compiler/CLI entrypoints, or distribution
packages. Architecture tests enforce the direction.

## Rejected alternatives

- A runtime bean factory or service locator duplicates Spice and hides exact
  dependencies.
- A compiled `RuntimeGraph` permits wiring to differ from generated Go.
- Provider-specific message fields force all consumers to inherit provider churn.
- Kernel parent/child agent concepts prematurely constrain orchestration.
- Reflection dispatch weakens navigation, compile-time checks, and debugging.

## Consequences

Extension authors sometimes write small adapters or decorators instead of
registering arbitrary runtime objects. In exchange, embedded tests construct the
entire engine with ordinary Go, generated applications are inspectable, and
provider/tool/transport implementations evolve independently.

## Acceptance evidence

The boundary is proven only when a real provider, compiled tool, runtime tool,
required persistence observer, alternate client, and two-worker extension pass
without adding implementation concepts to the kernel. Dependency scans, race
tests, cancellation/panic tests, snapshot round trips, and generated DI source
are retained in the phase ledger.

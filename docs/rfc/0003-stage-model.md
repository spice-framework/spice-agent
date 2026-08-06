# RFC 0003: Spice-Native Stage and Tool Model

- **Status:** accepted for preview
- **Depends on:** ADR 0001 and Spice typed DI contracts

## Decision

Stages are generic exact transforms:
`Stage[Input, Output].Process(context.Context, Input) (Output, error)`. Each
instantiation is a distinct Go interface resolved at generated construction
time, not a universal middleware type. Executable tools are likewise narrow Go
interfaces. Replaceable defaults are fallback beans. Application or
starter implementations are normal candidates; ambiguity requires an
application-owned typed primary or qualifier. Decoration uses ordered typed
collections and never mutates an existing bean registry.

The default stage pipeline is a constructor-injected immutable slice. Ordering
uses Spice `@Order`, then canonical bean name and source position for deterministic
diagnostics. A stage receives only its typed input, output, and context; it cannot
look up other stages or tools by arbitrary runtime string.

Tools are exposed to models as immutable definitions but execute only through
one `ToolDispatcher`. The dispatcher owns a canonical map built from
`map[string]tool.Tool` injection, rejects name/definition mismatches, snapshots
definitions for each model request, enforces call/progress correlation, contains
panics, and preserves cancellation. Policy, telemetry, retry, and runtime-plugin
support are ordered typed dispatcher decorators.
`ApplyToolDispatchDecorators` applies the already Spice-ordered collection to
the merged dispatcher: the first decorator is outermost. Nil, panicking,
nil-returning, and definition-changing decorators fail construction. Every
wrapper is guarded by the same immutable definition snapshot, so it cannot
dispatch an undeclared tool.

Each definition has mandatory `Effect` (`read_only` or `mutating`) and
`ReplaySafety` (`safe`, `idempotent`, or `unsafe`) metadata. Capabilities are an
unordered set normalized to lexical order before storage, cloning,
fingerprinting, or exposure. Read-only tools must be replay-safe and cannot
declare `filesystem.write`, `process.execute`, `network.access`, or
`environment.write`; contradictory metadata fails dispatcher construction.
Replay-safe is reserved for read-only tools; mutating tools must declare either
idempotent or unsafe replay.

`Execute(context.Context, tool.Call, tool.Reporter)` returns
`(tool.Result, error)`. A problem result is a completed, model-visible tool
outcome. A Go error is infrastructure failure and must itself be exactly one
valid `*tool.ExecutionError` correlated to the active call. Error wrappers,
joins, and sibling failures are rejected so unbounded or unrelated data cannot
bypass validation. The dispatcher
rejects a simultaneous result and error, untyped errors, mismatched call IDs,
uncertain outcomes from read-only tools, and retry advice for replay-unsafe
tools. Uncertain outcomes always prohibit retry. Accepted typed errors are
returned without replacing their error chain, so cancellation remains
discoverable through `errors.Is`.
When progress reporting and execution both fail, the dispatcher returns one
generic-text `DispatchFailure`. Its normal unwrap path contains only the
validated execution failure; reporter durability is available explicitly via
`ReporterFailure` and cannot inject a cancellation sentinel into `errors.Is`.

## Annotation mapping

`@Stage`, `@Tool`, and `@ModelProvider` descriptors emit only existing generic
Spice provider and bean metadata contributions: name, aliases, qualifiers,
fallback/primary, and order. Exact interface-returning factories need no
interface-binding contribution. The ordinary Go signature establishes the
typed boundary and may use the standard optional cleanup/error provider forms;
handlers do not guess assignability from readable type strings. The generic
compiler remains authoritative for exact identity, aliases, cleanup, and error
forms. They do not emit an agent registry entry. Descriptor handlers are
typed Go functions in canonical files and the compiler consumes only generic
contributions.

## Dynamic tools

The compiled graph may contain one runtime-plugin-backed `ToolPlanSource`. A
source leases either its current immutable plan for a new run or one exact
`PlanID` for snapshot recovery. A lease owns a dispatcher, canonical defensive
copies of its definitions, and an idempotent observable release. An ID must
never be reused for changed definitions or behavior. The kernel validates
source results, releases a lease returned alongside an error or wrong ID, and
contains source/decorator/definition panics without exposing recovered text.
`StaticToolPlanSource` adapts an ordinary compiled dispatcher for embedded
applications. The source—not Go structural copying—guarantees behavior remains
stable while leased. Release callbacks only decrement references and must not
block; drain and process shutdown are source-owned asynchronous work. Dynamic
tools do not add compiled stages or mutate static DI.

## Rejected alternatives

- String-keyed runtime stage registries hide type errors and source navigation.
- Universal middleware functions erase lifecycle and capability contracts.
- Allowing tools to publish events directly bypasses canonical sequencing and
  future permission interception.
- Automatic interface scanning makes dependency selection dependent on ambient
  packages rather than source declarations.

## Acceptance

Generated fixtures prove fallback replacement, deterministic ambiguity, typed
primary/qualifier resolution, ordered decorators, named tool maps, test overrides,
capability snapshots, panic/cancellation, and absence of registry/reflection
source. A future permission stress prototype must intercept every executable
route without kernel changes.

The in-module `CompositionProof` target is the canonical compiled-composition
fixture. It returns exact aliased interfaces from the agent annotations, uses
the official typed `@Primary` and parameter `@Qualifier` metadata, and commits
the ordinary generated graph, ownership manifest, and source mappings. Its
application tests execute construction, selection, ordered transforms,
canonical map injection, typed override, rollback, and reverse cleanup. The
separate negative fixtures are compiled by the real CLI and retain stable
source-positioned diagnostics.

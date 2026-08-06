# ADR 0001: The Spice Graph Is the Only Compiled Extension Graph

- **Status:** accepted
- **Date:** 2026-08-06
- **Scope:** all Spice Agent repositories

## Context

An early design proposed an agent-specific compiled extension manifest and
`RuntimeGraph` that would select stages, providers, tools, and UI components
beside Spice. That would create two dependency systems, two diagnostic models,
and two answers to which implementation an application executes.

Spice already provides exact interface binding, explicit `@Implements`, bean
names and aliases, qualifiers, primary/fallback selection, ordered collections,
scopes/lifecycle, module boundaries, starter manifests, explicit
auto-configuration, generated source mappings, and test overrides.

## Decision

The generated Spice bean graph is the sole compiled composition graph. Agent
annotations may provide ergonomic domain spelling, but their handlers emit only
generic Spice contributions. Static application dependencies are visible as
ordinary direct-call generated Go and are immutable after construction.

Compiled extension modules activate defaults only through explicit
`/autoconfigure` blank imports. Defaults are fallback beans. Application code
resolves multiple candidates through the normal typed primary/qualifier rules.
Ordered typed collections implement decoration; canonical bean names identify
static tool maps.

Runtime plugin generations remain separate because their processes do not exist
at compile time. One statically injected host/decorator owns that dynamic graph.
It may contribute leased runtime tools for future runs but never mutate Spice
DI, add compiled stages, or load executable UI code.

## Prohibited designs

- `extension.Manifest`, compiled `RuntimeGraph`, stage/provider/tool selection
  maps, mutable bean registries, service locators, or reflection-based lookup;
- package scanning or automatic interface discovery from ambient dependencies;
- compiler switches on agent annotation names instead of descriptor handler
  contributions;
- runtime plugin APIs that register static beans or native UI implementations.

Architecture tests search source/imports for these concepts and generated
fixtures prove the actual selected graph.

## Consequences

Positive consequences are one source of diagnostics, native Go navigation,
inspectable/debuggable generated code, deterministic selection, reusable Spice
testing, and no runtime compiler/container dependency.

The cost is that third-party static extensions must publish annotations,
descriptors, explicit interface bindings, and auto-configuration instead of one
untyped registration function. Dynamic needs require a process protocol and
cannot silently alter in-flight runs.

## Reconsideration rule

This decision may be revisited only by a generic Spice RFC demonstrating a
cross-domain capability the current typed contribution model cannot express.
Agent-specific convenience or schedule pressure is not sufficient evidence.

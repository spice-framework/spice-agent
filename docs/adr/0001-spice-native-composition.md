# ADR 0001: Reject a Parallel Compiled Extension Graph

- **Status:** accepted
- **Date:** 2026-08-06

## Decision

The generated Spice bean graph is the sole compiled composition graph. Static
extensions use exact interfaces, explicit `@Implements`, names, qualifiers,
primary/fallback selection, ordered collections, lifecycle, modules, starter
manifests, and explicit auto-configuration.

## Rejected alternative

A second `extension.Manifest`, compiled `RuntimeGraph`, selection map, or mutable
registry would duplicate Spice, weaken native navigation and diagnostics, and
permit runtime wiring to diverge from generated Go. Such types and identifiers
are prohibited. Runtime-plugin generations remain separate because their
processes do not exist at compile time.


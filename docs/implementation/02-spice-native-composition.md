# Phase 2: Spice-Native Composition

**Objective:** prove that compiled extension composition is ordinary generated
Spice DI. `@Stage`, `@Tool`, and `@ModelProvider` emit existing stereotype,
interface-binding, metadata, and ordering contributions. Defaults use fallback;
applications resolve ambiguity with typed primary or qualified beans.

No service locator, compiled extension registry, package scanning, or
`RuntimeGraph` is permitted. External defaults require explicit
`/autoconfigure` blank imports. **Acceptance:** generated direct calls, ordered
collections, named maps, ambiguity diagnostics, test override, module graph, and
source mapping. **Status:** in progress.


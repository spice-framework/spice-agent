# Phase 1: Spice-Native Composition

## Objective and prerequisites

Prove that every compiled agent extension is an ordinary Spice bean selected by
the existing typed DI rules. This phase requires the exact Spice core/toolchain
preview revisions pinned in `go.mod`, the public annotation SDK, deterministic
per-source generation, and the repository foundation from phase 0.

## Public contracts

- `@Stage`, `@Tool`, and `@ModelProvider` are documented agent annotation
  descriptors with authorized Go handlers. They emit only generic provider and
  bean-name/alias/qualifier/fallback/primary/order metadata contributions; the
  Spice compiler contains no agent annotation-name switch. Exact
  interface-returning factories need no interface-binding contribution. The
  generic compiler owns `go/types` identity and alias resolution and supplies
  canonical per-result type facts to authorized handlers. Handlers fail closed
  when those facts are absent: Tool and ModelProvider require their exact named
  interfaces (including aliases), while Stage requires a named interface result.
  Display strings never participate in type validation.
- Every annotation has one canonical descriptor/handler Go file. Go to
  Definition opens the descriptor; Go to Implementation opens the typed handler.
- External defaults activate only through an explicit blank import of that
  module's `/autoconfigure` package.
- A default implementation is a fallback bean. A normal candidate replaces it.
  Multiple normal candidates are a deterministic error unless application code
  supplies a typed primary or qualified alias bean.
- Typed stages use distinct `stage.Stage[Input, Output]` instantiations or
  narrower application-owned interfaces. Tools inject as `map[string]tool.Tool`
  using required canonical bean names. Names are static identities, not runtime
  lookup keys supplied by untrusted input.
- Every executable is an `@Application` target and builds from committed,
  inspectable generated Go without the Spice compiler at runtime.

## Dependency and module boundaries

The repository is one Modulith root; its supported descendant API/SPI packages
are uniquely named interfaces. They are not competing module roots, so ordinary
kernel imports remain intra-module and no artificial allowed-dependency
exceptions are needed. Annotation/tooling packages may
depend on the public SDK and SPI facts but never on provider, coding-tool, TUI,
daemon implementation, or distribution packages. Auto-configuration packages
may wire their own module's beans and public SPIs only.

The compiled graph is immutable after generation. Runtime plugins may supply
dynamic tool implementations behind one injected host bean, but they never add
stages, providers, or UI code to Spice DI.

## Implementation slices

1. Define Modulith roots, exported named interfaces, and allowed dependencies.
2. Implement annotation descriptors and the authorized annotation tool.
3. Add auto-configuration contracts and starter manifests for core defaults.
4. Generate and compile a minimal embedded-engine application showing fallback
   selection, ordinary replacement, deterministic ambiguity, typed primary
   resolution, ordered decorators, and named tool maps.
5. Generate test-only application graphs proving application-owned overrides
   without a global mutable test container.
6. Add architecture guards that reject a compiled extension registry,
   `RuntimeGraph`, service locator, reflection lookup, or package scan.
7. Exercise `spice annotations doctor`, `spice beans --explain`, module diagrams,
   source mappings, ownership manifests, `--check`, and byte-identical rerender.

## Exclusions

The advanced aggregate-analysis seam is deferred. Agent metadata does not enter
Spice core unless a later generic RFC proves usefulness beyond agents. Dynamic
plugin discovery, daemon transport, persistence, and runtime user configuration
are not compiled-bean concerns.

## Verification

- Positive/negative annotation target, argument, import, authorization, and
  protocol tests use real generated-code compilation.
- DI tests cover zero/one/many candidates, fallback, primary, qualifier, aliases,
  order, map names, concrete vs explicit interface binding, pointer/value method
  sets, and generated test overrides.
- Two clean generations are byte-identical. Manual or stale generated-file edits
  are refused rather than overwritten.
- Architecture tests scan imports and source for forbidden registries, reflection
  dispatch, agent-name switches, hidden package enumeration, and global mutable
  containers.
- Offline vendor execution proves the generated application needs no compiler,
  network, or annotation tool at runtime.

## Performance and completion evidence

Warm generation/check for the embedded fixture targets five seconds; generated
application startup targets 100 ms. Exact source-to-generated-file mappings and
selected bean reasons are retained as artifacts.

Status is **complete for the preview**. The typed Stage SPI, three canonical
descriptors, authorized deterministic v1alpha2 tool, and committed
`CompositionProof` target cover the embedded generated graph. Provider,
coding-tool, and TUI defaults activate only through their explicit
`/autoconfigure` packages. Distribution commit `4cfd19a` compiles and executes
their generated provider → named tool → provider continuation through ordinary
Spice beans. The graph uses direct calls and contains no extension registry,
`RuntimeGraph`, reflection lookup, or second container. Evidence is recorded in
[`evidence/phase1-composition.md`](evidence/phase1-composition.md). Pre-1.0
contract evolution and clean-room public-authoring ergonomics remain later-phase work; no
repository may introduce a substitute static composition mechanism.

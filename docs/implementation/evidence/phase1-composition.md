# Phase 1 Generated Composition Evidence

This evidence is executable and repository-owned. The
`internal/compositionacceptance` tests run every read-only command below with
`GOWORK=off`, `GOPROXY=off`, `GOFLAGS=-mod=vendor`, and
`GOTOOLCHAIN=local`; they parse or assert the stable output rather than storing
machine-specific absolute paths.

The proof is pinned to Spice core
`v0.1.0-preview.1.0.20260806200749-524424a04df0` and toolchain
`v0.1.0-preview.1.0.20260806203056-d0b9ac086bd6`; the latter passed its own
full verification in 422.9 seconds at 85.1% coverage before this consumer was
vendored. On the final Windows/amd64 edit loop before repository verification,
`make fast` passed in 26.2 seconds and `make check` passed in 28.4 seconds.

| Contract | Exact command | Asserted evidence |
| --- | --- | --- |
| Authorized SDK tools | `go tool github.com/spice-framework/toolchain/cmd/spice annotations doctor ./internal/compositionfixture` | `9 descriptor(s), 2 tool(s)` and no stderr |
| Bean metadata | `go tool github.com/spice-framework/toolchain/cmd/spice beans --explain --format=json ./internal/compositionfixture` | normal `alpha`/`beta`, primary `beta`, fallback `fallback`, and canonical `read`/`write` beans |
| Modulith structure | `go tool github.com/spice-framework/toolchain/cmd/spice modules --format=json ./...` | the single `github.com/spice-framework/spice-agent` root, named interfaces, no cycles, and no unassigned packages |
| Source navigation | `go tool github.com/spice-framework/toolchain/cmd/spice generated --source <manifest-source> --line <manifest-line> --target compositionproof --format json` | source-to-generated `provider-construction` mapping |
| Reverse navigation | `go tool github.com/spice-framework/toolchain/cmd/spice generated --generated <manifest-output> --line <manifest-line> --target compositionproof --format json` | generated-to-source mapping to the exact factory |
| Focused module tests | `go tool github.com/spice-framework/toolchain/cmd/spice test --module=github.com/spice-framework/spice-agent --count=1 --run=GeneratedComposition ./...` | ordinary Go tests pass for the one-module graph |
| Freshness | `go tool github.com/spice-framework/toolchain/cmd/spice generate --check --target CompositionProof . ./internal/compositionfixture` | generation is current |
| Bounded diff | `go tool github.com/spice-framework/toolchain/cmd/spice generate --diff --target CompositionProof . ./internal/compositionfixture` | generation is current with no diff |

The ownership test copies only the required module, self-authorized annotation
tool, vendored toolchain, fixture source, generated target, and manifest to a
temporary directory. Two generations are byte-identical. After a generated
provider file is modified, generation reports that the owned file changed after
the manifest and preserves the edited bytes.

The generated application tests prove:

- the normal primary `beta` provider replaces the fallback and resolves two
  normal candidates without runtime lookup;
- exact aliases of `model.Provider`, `tool.Tool`, and
  `stage.Stage[string, string]` compile through the generated graph;
- stages execute in order `trim`, then `suffix`;
- a fallback-only stage is selected, while a normal stage replaces the
  competing fallback for a second narrow stage interface;
- aliases select the `read` tool while `map[string]tool.Tool` contains only
  canonical `read` and `write` entries;
- full cleanup/error factories register immediately and stop in reverse
  `write`, `read` order;
- a later factory failure rolls back the already-constructed `read` cleanup;
- a typed `bean.Replace` override changes the selected provider for one
  generated application without a global container; and
- generated source uses direct constructor calls and contains no reflection,
  service locator, extension registry, or compiled runtime graph.

The isolated negative fixtures run through `spice verify`. Ambiguous exact
interface providers report the same ordered diagnostic over three runs.
Standalone `@Tool`, `@ModelProvider`, and `@Stage` factories with basic results
are rejected by their official handlers using compiler-owned canonical result
facts. A separate ordinary `@Bean` with a concrete tool implementation is not
implicitly assignable to `tool.Tool`; the graph fails at the exact interface
injection boundary.

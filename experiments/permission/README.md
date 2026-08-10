# Permission guard experiment

This nested module stress-tests an optional permission policy without adding a
policy, registry, sandbox, or new extension type to Spice Agent core. It pins
the released `github.com/spice-framework/spice-agent v0.1.0-preview.5` module,
contains no `replace` directive, and commits its vendor tree. The API is
experimental and may be deleted or redesigned before promotion.

## Contract

`Policy.Decide` receives immutable `Facts` and returns `allow`, `deny`, or
`prompt`. `Guard` is an ordinary `stage.ToolDispatchGuard` bean. It never owns
events and never retries. Every invocation of the canonical continuation—an
initial compiled call, an application-owned retry, or an activated runtime-host
generation—crosses the guard again.

Prompt decisions call only `ToolDispatchScope.RequestInteraction`. The guard
cannot obtain or substitute `interaction.Broker` scope authority. Cancellation
always wins. The zero prompt-failure option denies; an application must choose
the conspicuous `DecisionAllow` default explicitly.

Policy facts exclude call arguments, definitions' descriptions and schemas,
raw run/call/plan identities, interaction values, paths, environment values,
and secrets. Correlation and tool identities are SHA-256 digests in durable
JSON. A policy may inspect the model-visible tool name transiently; effects,
replay safety, capabilities, and fingerprints are already part of the public
tool/plan contract. Policy errors and panics become fixed secret-safe failures.

The generated `PermissionProof` application proves direct static construction:

```text
NewProofPolicy -> NewProofGuard -> []stage.ToolDispatchGuard -> NewProof
```

There is no package scan, reflective lookup, mutable container, or parallel
compiled graph.

## Security and dependencies

- The guard author is trusted in-process Go code. It can deny canonical tool
  execution but is not a sandbox and cannot constrain arbitrary code outside
  the dispatcher.
- Trusted outer decorators may short-circuit without executing a tool. If they
  deliberately retry by invoking the inner dispatcher, each attempt is checked.
- The module performs no network access. Its only runtime dependency is the
  released Agent/Spice graph; the pinned toolchain is generation-only.
- `compatibility.json` records exact versions and public checksums. Vendor-only
  tests prove the checked-in dependency graph. No policy decision, prompt, or
  secret is written by the experiment.
- Cancellation is cooperative and is preserved through policy and prompt
  handling. Observability is intentionally limited to the existing canonical
  engine events; this experiment creates no second event stream.

## Verification

With Go 1.26.5:

```text
make fast
make check
make verify
make benchmark
```

All commands force `GOWORK=off`, `GOPROXY=off`, and `-mod=vendor`. See
`benchmarks/README.md` for provisional measurements. Promotion requires
independently authored policies and a contract/security review; passing these
tests does not stabilize the API. The parent quality gate enforces at least 85%
handwritten package coverage; the initial experiment measures 88.3%.

## Deletion

Delete `experiments/permission` and remove only its evidence/status links from
the root Phase 7 ledger and RFC notes. The parent module does not import this
nested module, and deleting it changes no Agent package, generated application,
protocol, release metadata, or public module graph.

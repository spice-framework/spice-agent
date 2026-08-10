# Phase 7 permission experiment evidence

## Boundary

The experiment is a nested module at `experiments/permission`. It depends on
the public `github.com/spice-framework/spice-agent v0.1.0-preview.5` release with
SumDB module hash `h1:rGND9DYx3pssliD1tZQOvPDOZ5GVfQLDc7VJQI3HLOM=` and
Go-module hash `h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=`. It has no
`replace`, no parent-module import, and no release/workflow change.

The public experimental surface is deliberately small:

- immutable `Facts` with hashed run/call/plan correlation and public
  definition/plan security metadata;
- concurrent `Policy`/`PolicyFunc` returning allow, deny, or prompt;
- `Guard`, an ordinary `stage.ToolDispatchGuard`, with zero-value fail-closed
  prompt fallback and an explicit application-selected allow fallback;
- prompt delegation solely through `ToolDispatchScope.RequestInteraction`.

Durable fact JSON contains no call arguments, tool description/schema, raw
run/call/plan/tool identity, paths, environment, secrets, prompt responses, or
recovered values. A policy may inspect the model-visible tool name transiently.
The experiment stores nothing and emits no parallel event stream.

## Composition and conformance

The committed `PermissionProof` generated application constructs:

```text
NewProofPolicy -> NewProofGuard -> []stage.ToolDispatchGuard -> NewProof
```

Tests prove allow/deny, affirmative and negative prompt responses, unavailable
and malformed prompt failure, explicit failure default, cancellation priority,
policy panic redaction, immutable secret-free JSON facts, each trusted-decorator
retry crossing the guard, 64-way concurrent dispatch, generated application
construction, and plugin-host dispatch through both the initial compiled
generation and a distinct atomically activated generation. The empty activation
intentionally launches no process; runtime-plugin transport/conformance remains
owned by Phase 5 while this proof isolates generation pipeline placement.

## Reproducible gates

All experiment commands set `GOWORK=off`, `GOPROXY=off`,
`GOTOOLCHAIN=local`, and `GOFLAGS=-mod=vendor`:

```text
make fast
make check
make verify
make benchmark
```

The parent quality gate independently reproduces the nested vendor tree, checks
generated Spice freshness, runs vet, shuffled tests, and race tests. Benchmark
samples are recorded in the experiment's `benchmarks/README.md`; they remain
provisional and have no noisy pass/fail threshold.

## Promotion and deletion

This is contract evidence, not a supported security product. Promotion needs
clean-room public policy implementations plus dependency and threat-model review. To
delete it, remove `experiments/permission` and these ledger/RFC evidence links.
No Agent package, protocol, generated production target, core module graph, or
release metadata changes.

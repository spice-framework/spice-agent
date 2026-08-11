# Phase 8 Released-Generation Compatibility Evidence

## Bounded outcome

This slice adds the candidate proof for the two immutable Agent generations
required by RFC 0010. It does not relabel two checked-out source profiles as
releases and does not claim a prebuilt executable asset that the module release
does not publish.

The canonical `compatibility/released-generation.json` manifest identifies:

- preview5 at tag commit `3e8fe6406171a7e7f1765311a4fa7fc3b878e425`,
  module sum `h1:rGND9DYx3pssliD1tZQOvPDOZ5GVfQLDc7VJQI3HLOM=`;
- preview6 at tag commit `f771caa3b150d87845417c4e26938e2a889441a6`,
  module sum `h1:XJKJge+xWP/FLNoL1/rXq8z8tdu/5iEkKfmu1dTgFms=`; and
- the common go.mod sum
  `h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=`.

Each generation is downloaded through `proxy.golang.org` with
`sum.golang.org`, `GOWORK=off`, empty private-module overrides, and a separate
fresh module/build cache. The runner rejects version, tag, commit, module sum,
or go.mod sum drift before compilation. It then tidies and vendors the isolated
peer, disables the network, proves tidy stability, builds the peer with
`-mod=vendor`, and builds the official released plugin fixture offline from the
verified module cache.

## Cross-generation process matrix

The engine matrix runs both directions at exact protocol 1.3:

1. preview5 client to preview6 server; and
2. preview6 client to preview5 server.

Each direction uses public client, daemon, gRPC adapter, and current-user local
IPC contracts. It proves wrong-token refusal, authenticated initialization,
model delta plus completed terminal, remote cancellation plus cancelled
terminal, zero active runs, clean server shutdown, and secret-free process
output.

The independent plugin matrix runs both directions at plugin protocol 1.0:

1. preview5 conformance client to the preview6 official Go fixture; and
2. preview6 conformance client to the preview5 official Go fixture.

The public `plugin/conformance` suite proves the authenticated transcript,
immutable manifest, echo, typed failure, malformed and oversized refusal,
cancellation, drain, shutdown, and clean fixture exit.

## Verification boundary

The initial repository-owned Windows execution passed:

```text
make verify-released-compatibility
```

Both engine directions negotiated `1.3.0`, both plugin directions completed
the full `1.0.0` conformance suite, and all four processes exited cleanly. The
dedicated `.github/workflows/released-compatibility.yml` workflow repeats the
same uncached command on `ubuntu-latest` and `windows-latest`.

At this candidate boundary the machine manifest remains `proven: false`, the
hosted evidence is empty, and both v1 protocol blockers remain. They may be
removed only by a follow-up commit after the exact runner and peer-source digest
are terminal green on both hosted platforms.

## Deletion and truthful limit

Deleting the dedicated workflow, manifest, runner, and peer fixture removes
only this evidence. It does not change the engine or plugin protocol, daemon,
kernel, public Go API, or release artifacts.

The proof is a released-*generation* matrix: processes are independently built
from immutable public module releases. The releases do not ship prebuilt daemon
or plugin binaries, so `released_binary_matrix.proven` remains false and its
claim remains `not-claimed`.

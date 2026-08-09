# Phase 6 Release History

## `v0.1.0-preview.1`: immutable pre-artifact failure

The annotated `v0.1.0-preview.1` tag is retained as failed release history. It
must never be moved, deleted, or treated as a published module release.

| Identity | Exact value |
| --- | --- |
| Tag object | `72c33c04f43b031eddd22d343f2a0dd58aff66c2` |
| Tagged commit | `0393b5ec16eac82a58c6d420ad6e72be8f2c560d` |
| Reusable workflow commit | `c4df74b2c60640c60fe0fa3fe641dadafbc4148a` |
| Failed workflow run | [31318421427](https://github.com/spice-framework/spice-agent/actions/runs/31318421427) |

The exact tag and main-ancestry checks passed. The uncredentialed candidate
gate then entered `make verify-release` without first bootstrapping the
candidate's pinned tools. Formatting attempted to resolve `goimports` while
`GOPROXY=off` and failed with the actionable bootstrap diagnostic.

The failure occurred before any release artifact or authority boundary:

- the run uploaded zero workflow artifacts;
- no GitHub Release was created;
- central rendering and independent artifact verification were skipped;
- keyless attestation and provenance authentication were skipped; and
- protected publication was skipped.

The first recovery advanced to `v0.1.0-preview.2` and pinned the organization
workflow that separates uncredentialed public-proxy tool bootstrap from
subsequent offline candidate verification. That corrected the preview.1
failure, but preview.2 exposed a later independent-verifier cleanup defect.

## `v0.1.0-preview.2`: immutable pre-attestation failure

The annotated `v0.1.0-preview.2` tag is also retained as failed release
history. It must never be moved, deleted, or treated as a published module
release.

| Identity | Exact value |
| --- | --- |
| Tag object | `f17af5ed24ab8e2243ac3f62e8b978d52dd3aa2d` |
| Tagged commit | `8d53ee064f9f0ae267c8f7b14ef21d192c85958a` |
| Reusable workflow commit | `07f898b85e7d1c409b91bf280e47d62921e786b6` |
| Failed workflow run | [31322858420](https://github.com/spice-framework/spice-agent/actions/runs/31322858420) |
| Rendered handoff | artifact `9040766739`, SHA-256 `ed82f5fd56c4b6f30def650ee222120d6d787879c80c4712d19a1b96c20840b0` |

Fresh candidate tool bootstrap and offline verification passed. The immutable
central renderer also passed and uploaded the exact four-artifact handoff. The
independent verifier validated its inputs and built offline, then failed while
removing Go's intentionally read-only module cache:

```text
unlinkat .../module-cache/golang.org/x/sync@v0.22.0/PATENTS: permission denied
```

The failure remained before every release-authority boundary:

- no independently verified handoff was uploaded;
- keyless attestation and provenance authentication were skipped;
- both protected environments remained unapproved; and
- no GitHub Release was created.

The `v0.1.0-preview.3` recovery uses development policy
`3937dca034f88907ea170967194e7f765777ac5a`, independent verifier
`2c0329bdf49a69c342007d95c49db7bda5cf7e19`, and organization workflow
`9b35ae8173d76f3baf9c63d74189863bc6f59e86`. The verifier now repairs owner
access through a root-scoped, symlink-safe private-workspace boundary and still
fails closed if cleanup cannot complete. Preview.1 and preview.2 metadata and
caller identities remain explicit negative quality-gate fixtures.

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

The `v0.1.0-preview.3` recovery used development policy
`3937dca034f88907ea170967194e7f765777ac5a`, independent verifier
`2c0329bdf49a69c342007d95c49db7bda5cf7e19`, and organization workflow
`9b35ae8173d76f3baf9c63d74189863bc6f59e86`. The verifier now repairs owner
access through a root-scoped, symlink-safe private-workspace boundary and still
fails closed if cleanup cannot complete. That fixed the preview.2 cleanup
failure, but the independent policy still authorized preview.2 while the
renderer authorized preview.3.

## `v0.1.0-preview.3`: immutable pre-attestation policy failure

The annotated `v0.1.0-preview.3` tag is retained as failed release history. It
must never be moved, deleted, or treated as a published module release.

| Identity | Exact value |
| --- | --- |
| Tag object | `094675bd3280203f18c87b9d7913ab2df8ef1315` |
| Tagged commit | `def246dd7aad59565da151ccd7632e8e72528efe` |
| Reusable workflow commit | `9b35ae8173d76f3baf9c63d74189863bc6f59e86` |
| Failed workflow run | [31325588169](https://github.com/spice-framework/spice-agent/actions/runs/31325588169) |
| Rendered handoff | artifact `9041514109`, SHA-256 `cd9400854662a9bd752a08c3ce1160723ff9d47927af8dd0991dd10c4cb2484f` |

Fresh candidate bootstrap and offline verification passed. The central
renderer also passed and uploaded its exact handoff. The independent verifier
built offline, then rejected the trusted inputs before artifact verification:

```text
spice-go-release-verify: trusted release inputs do not match independent module policy
```

Development authorized Agent preview.3, but the independently pinned Toolchain
policy still authorized preview.2. The failure remained before every release
authority boundary:

- no independently verified handoff was uploaded;
- keyless attestation and provenance authentication were skipped;
- both protected environments remained unapproved; and
- no GitHub Release was created.

## `v0.1.0-preview.4`: pre-tag policy authorization

Preview.4 uses separately reviewed policies and their organization-owned
workflow pin:

| Authority | Exact commit |
| --- | --- |
| Development renderer policy | `d0f88db000acb566b72499c736c9134909ee7912` |
| Toolchain independent policy | `4a97e78c3495c5f61bd4e25111722855184a786c` |
| Reusable organization workflow | `0fcd43dc8b41fad56c231d0e136ad8c762276ed5` |

Before changing release metadata or creating a tag, both artifact-free policy
checks ran from clean worktrees at those exact authority commits with Go
1.26.5, vendored dependencies, `GOWORK=off`, `GOPROXY=off`, `GOSUMDB=off`, and
`GOTOOLCHAIN=local`.

Development command:

```text
go run -mod=vendor ./cmd/spice-dev go-release policy-check --repo spice-agent --module github.com/spice-framework/spice-agent --version v0.1.0-preview.4 --profile go-module-v1
```

Development output:

```text
go-module-v1	spice-agent	github.com/spice-framework/spice-agent	v0.1.0-preview.4
```

Toolchain command:

```text
go run -mod=vendor ./cmd/spice-go-release-verify policy-check --repository=spice-agent --source=https://github.com/spice-framework/spice-agent --module=github.com/spice-framework/spice-agent --version=v0.1.0-preview.4 --profile=go-module-v1
```

Toolchain output:

```json
{"profile":"go-module-v1","repository":"spice-agent","module":"github.com/spice-framework/spice-agent","version":"v0.1.0-preview.4","source":"https://github.com/spice-framework/spice-agent"}
```

Normalizing the Toolchain JSON to Development's ordered
`profile`, `repository`, `module`, and `version` tuple produced identical bytes,
including one trailing newline. Their SHA-256 is
`5b40abb5ae77cc0699128616845ff517c98e44d2fd5a3b434acf32614e68ea86`.
Toolchain additionally binds the canonical source URL. At the time of this
comparison, preview.4 had no tag, GitHub Release, or release workflow run.

The release metadata and caller now advance to preview.4 and the exact
organization workflow above. Preview.1, preview.2, and preview.3 metadata and
caller identities remain explicit negative quality-gate fixtures. The pre-tag
comparison is policy agreement only; candidate verification, rendering,
independent artifact verification, attestation, provenance authentication, and
protected publication remain mandatory.

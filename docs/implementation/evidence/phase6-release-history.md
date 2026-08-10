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

## `v0.1.0-preview.4`: published architecture-proof module

Preview.4 uses separately reviewed policies and their organization-owned
workflow pin:

| Authority | Exact commit |
| --- | --- |
| Tag object | `c53f4bf9ffbb283320da66184c25088c8c5edf1e` |
| Tagged commit | `27dba90347520681eadb4fc6e86b69160bf8e00f` |
| Development renderer policy | `d0f88db000acb566b72499c736c9134909ee7912` |
| Toolchain independent policy | `4a97e78c3495c5f61bd4e25111722855184a786c` |
| Reusable organization workflow | `0fcd43dc8b41fad56c231d0e136ad8c762276ed5` |
| Successful release run | [31328938331](https://github.com/spice-framework/spice-agent/actions/runs/31328938331) |
| Published prerelease | [`v0.1.0-preview.4`](https://github.com/spice-framework/spice-agent/releases/tag/v0.1.0-preview.4) |

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

The release metadata and caller then advanced to preview.4 and the exact
organization workflow above. Candidate verification, rendering, independent
artifact verification, attestation, provenance authentication, and protected
publication all completed. The published prerelease contains the authenticated
source archive, SPDX SBOM, release metadata, checksums, and portable Sigstore
bundle. Preview.1, preview.2, and preview.3 metadata and caller identities
remain explicit negative quality-gate fixtures.

## `v0.1.0-preview.5`: published Phase 7 module

Preview.5 carries the generic Phase 7 terminal dispatch-guard seam, the
run-owned interaction requester, typed tool-start and tool-terminal occurrence
facts, the v1alpha3 snapshot boundary, and the first offline kernel runtime
baseline. These product commits remain individually reviewable beneath the
release preparation; no product, module, dependency, generated, vendor, or
tools byte is changed to authorize the candidate.

| Authority | Exact commit or run |
| --- | --- |
| Product base | `d30445b1704dbb89fcd8f11277f8188f0b19084c` |
| Development renderer policy | `a15d9406dcf33fddea29830491f5cdbcc1f4be47` |
| Toolchain independent policy | `7d9f7d1d1659e0ddbc5c604666527e68de2f184c` |
| Reusable organization workflow | `a8f9cc6ffd3a2744c5cae3b52c05e6e91cbc875e` |
| Product-base CI | [31342184213](https://github.com/spice-framework/spice-agent/actions/runs/31342184213) |
| Product-base documentation | [31342184208](https://github.com/spice-framework/spice-agent/actions/runs/31342184208) |
| Successful release run | [31343998056](https://github.com/spice-framework/spice-agent/actions/runs/31343998056) |
| Published prerelease | [`v0.1.0-preview.5`](https://github.com/spice-framework/spice-agent/releases/tag/v0.1.0-preview.5) |

Both artifact-free policy checks ran from clean hosted-green authority commits
with Go 1.26.5, vendored dependencies, `GOWORK=off`, `GOPROXY=off`,
`GOSUMDB=off`, and `GOTOOLCHAIN=local`.

Development command:

```text
go run -mod=vendor ./cmd/spice-dev go-release policy-check --repo spice-agent --module github.com/spice-framework/spice-agent --version v0.1.0-preview.5 --profile go-module-v1
```

Development output:

```text
go-module-v1	spice-agent	github.com/spice-framework/spice-agent	v0.1.0-preview.5
```

Toolchain command:

```text
go run -mod=vendor ./cmd/spice-go-release-verify policy-check --repository=spice-agent --source=https://github.com/spice-framework/spice-agent --module=github.com/spice-framework/spice-agent --version=v0.1.0-preview.5 --profile=go-module-v1
```

Toolchain output:

```json
{"profile":"go-module-v1","repository":"spice-agent","module":"github.com/spice-framework/spice-agent","version":"v0.1.0-preview.5","source":"https://github.com/spice-framework/spice-agent"}
```

Normalizing the Toolchain JSON to Development's ordered tuple produces the
same bytes, including one trailing newline. Their SHA-256 is
`a5df29b650781e6932661cb978370d16bbb4d8f25df5f7212180ccc3cb81f453`.
At authorization time there was no preview.5 tag, GitHub Release, or tag
workflow run. This commit advances only canonical release metadata, the audited
caller pin, their quality-gate constants and stale preview.4 negatives, and
this history. The policy match and green product base do not create, approve,
tag, attest, or publish a release.

The later annotated tag object
`f92c391ea0fa3bcd746d2d0ce6704ecd1d558a42` resolves to candidate commit
`3e8fe6406171a7e7f1765311a4fa7fc3b878e425`. The public Go proxy resolves the
module at preview.5. Release workflow
[31343998056](https://github.com/spice-framework/spice-agent/actions/runs/31343998056)
completed candidate validation, deterministic rendering, independent
verification, keyless attestation, provenance authentication, and protected
publication. The non-draft GitHub prerelease contains the exact source archive,
SPDX SBOM, release metadata, checksums, and portable Sigstore provenance
bundle. Fresh proxy and SumDB resolution yields module sum
`h1:rGND9DYx3pssliD1tZQOvPDOZ5GVfQLDc7VJQI3HLOM=` and go.mod sum
`h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=` at the tagged commit.

## `v0.1.0-preview.6`: published Phase 8 stabilization module

Preview.6 authorizes the current stabilization candidate after the immutable
preview.5 module. The candidate adds the public digest-bound
`process.VerifiedLauncher` and `process.ExecutableLease` boundary, source-built
engine protocol compatibility evidence, independent Go and Python runtime
plugin breadth, removable permission, recovery, delegation, compaction, Git,
telemetry, and planning experiments, and the enforced pre-v1 compatibility
history. Those experiments remain opt-in evidence; this candidate does not
claim a v1 API freeze, a released N/N-1 binary matrix, or external-author
completion.

| Authority | Exact commit or run |
| --- | --- |
| Product base | `913d65c6c1abb9a6b1532576f8938029a7290e33` |
| Development renderer policy | `c69219eb33ec50b6c5ab4a99515cb28d38975990` |
| Toolchain independent policy | `757076c72a71382548e7a1e38d9bbe4e56968a66` |
| Reusable organization workflow | `f29b7ce16f8d220e87bfae54469057d001944b7b` |
| Product-base CI | [31379105856](https://github.com/spice-framework/spice-agent/actions/runs/31379105856) |
| Product-base documentation | [31379105822](https://github.com/spice-framework/spice-agent/actions/runs/31379105822) |
| Tagged candidate | `f771caa3b150d87845417c4e26938e2a889441a6` |
| Annotated tag object | `ee8436262fb755c4bf4897254650cd6d84e2e9fc` |
| Successful release run | [31428824060](https://github.com/spice-framework/spice-agent/actions/runs/31428824060) |
| Published prerelease | [`v0.1.0-preview.6`](https://github.com/spice-framework/spice-agent/releases/tag/v0.1.0-preview.6) |

Both artifact-free policy authorities are clean and hosted green. Development
authorizes the exact tuple:

```text
go-module-v1\tspice-agent\tgithub.com/spice-framework/spice-agent\tv0.1.0-preview.6
```

Toolchain independently authorizes the matching closed JSON value:

```json
{"profile":"go-module-v1","repository":"spice-agent","module":"github.com/spice-framework/spice-agent","version":"v0.1.0-preview.6","source":"https://github.com/spice-framework/spice-agent"}
```

Normalizing the Toolchain value to Development's ordered tuple produces the
same bytes, including one trailing newline. Their SHA-256 is
`edd2a8e2454f5d5fc6f2aa47725b8af7c74eeeaeeb98ca6dcf094146b4b62501`.
The organization workflow verification and documentation runs
[31382031771](https://github.com/spice-framework/.github/actions/runs/31382031771)
and
[31382031890](https://github.com/spice-framework/.github/actions/runs/31382031890)
are green at the exact reusable-workflow commit.

At authorization time this preparation changed only canonical release
metadata, the reusable caller identity, their strict stale-preview.5
regressions, and this history. It did not create or move a tag, approve either
protected environment, attest or publish artifacts, or create a GitHub
Release.

The later annotated tag object
`ee8436262fb755c4bf4897254650cd6d84e2e9fc` resolves to the exact candidate
commit `f771caa3b150d87845417c4e26938e2a889441a6`. Release workflow
[31428824060](https://github.com/spice-framework/spice-agent/actions/runs/31428824060)
completed candidate validation, deterministic rendering, independent
verification, keyless attestation, provenance authentication, and protected
publication. The non-draft GitHub prerelease contains exactly
`checksums.txt`, `provenance.sigstore.json`, release metadata, an SPDX SBOM,
and the source archive. The checksum file closes over exactly the three
attested subjects. Fresh proxy and SumDB resolution yields module sum
`h1:XJKJge+xWP/FLNoL1/rXq8z8tdu/5iEkKfmu1dTgFms=` and go.mod sum
`h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=` at the tagged commit.

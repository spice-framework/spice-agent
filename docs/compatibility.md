# Compatibility policy

Spice Agent is pre-1.0. Its compatibility policy is enforced now so changes are
reviewable; it is not a stability promise.

## Canonical records

| Contract | Machine-readable source |
| --- | --- |
| policy and v1 blockers | `compatibility/policy.json` |
| public Go packages and reviewed breaks | `compatibility/go-api.json` |
| durable state identities and hard cuts | `compatibility/durable.json` |
| generated-source ownership and generator identity | `compatibility/generated-source.json` |
| clean-room public authoring | `compatibility/public-authoring.json` |
| released engine/plugin generations | `compatibility/released-generation.json` |
| security exceptions | `compatibility/security-exceptions.json` |
| engine protocol | `engine/v1/compatibility.json` |
| plugin protocol | `plugin/v1/compatibility.json` |

The manifests use strict canonical JSON. Engine and plugin histories are
independent. Durable versions are never reinterpreted, and security exceptions
cannot authorize downgrade to a withdrawn version.

## Reviewing a Go API change

Run:

```text
go run ./internal/qualitygate -mode=api-baseline
```

The command is standard-library-only, uses the committed vendor graph with the
network disabled, and evaluates `darwin/arm64`, `linux/amd64`, and
`windows/amd64` separately. It prints the exact package inventory and canonical
declaration digests; it does not edit the manifest.

An intentional source change must:

1. preserve or deliberately update all affected platform digests;
2. append an `SPICE-AGENT-GO-*` break record when existing source no longer
   compiles;
3. add a concrete migration guide;
4. keep the preview5 baseline unchanged; and
5. pass `make fast`, `make check`, and `make verify` on the exact commit.

The API inventory includes generated Protobuf declarations. It excludes
commands, internal packages, nested experimental modules, function bodies, and
documentation.

## Current truthful boundary

- Preview7 is the latest immutable Agent product release. Annotated tag object
  `251bd3b86c6c731cf2b8f20b57430130d31fde7e` resolves to commit
  `831fbf259ff3896067a7c6d74d4f402310214805`. Unique release workflow run
  `31519742953`, attempt 1, and protected attestation and publish deployments
  `5855895060` and `5855923346` produced immutable prerelease ID `368758289`
  with the exact five-asset module set. Fresh proxy and SumDB resolution yields
  module sum `h1:BQS23GwLBm5BLaRqMB9vYu+0dcEnuP6ooG6tzyjDSjY=` and go.mod sum
  `h1:WKNPxU7+jt+aPdL8v1aXovw9D32PwTYq3hE4xPug1YE=`.
- Preview5 remains the immutable API baseline. Preview6 annotated tag object
  `ee8436262fb755c4bf4897254650cd6d84e2e9fc` resolves to commit
  `f771caa3b150d87845417c4e26938e2a889441a6`.
- The public module proxy and SumDB resolve preview6 at that exact commit.
- Release workflow run `31428824060` completed validation, rendering,
  independent verification, keyless attestation, provenance authentication,
  and protected publication.
- The non-draft GitHub prerelease contains the exact five-asset module set:
  source archive, SPDX SBOM, release metadata, checksums, and portable Sigstore
  provenance bundle.
- Fresh proxy and SumDB resolution yields module sum
  `h1:XJKJge+xWP/FLNoL1/rXq8z8tdu/5iEkKfmu1dTgFms=` and go.mod sum
  `h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=`.
- Engine 1.2/1.3 and plugin 1.0 semantic evidence is complemented by the
  released-generation matrix at commit
  `609f74f0abc7e3eba9f8a9ceab3c68ac17208ca2`. Dedicated workflow run
  `31454312077` built preview5 and preview6 independently from the public proxy
  and SumDB and crossed their engine and plugin processes in both directions on
  Linux and Windows. The two released-generation blockers are closed; no
  prebuilt-binary asset matrix is claimed. Publishing preview7 does not change
  that preview5/preview6 evidence or prove a preview6/preview7
  cross-generation matrix.

## What remains before v1

The repository has proven all three clean-room public extensions, completed
the frozen generated-source exercise, and proven preview5/preview6 engine and
plugin compatibility on hosted Linux and Windows. Those released-generation
blockers remain closed. The policy records one separate v1 evidence blocker for
the independent Coding consumer of the public client conformance suite, and it remains explicitly
`pre-v1-enforced-not-stable`: a future v1 declaration is a separate reviewed
decision. Stable kernel benchmark ceilings and the stricter 20% time/10%
allocation investigation policy are machine-readable and enforced by
`make verify`. See [RFC 0010](rfc/0010-api-and-protocol-compatibility.md).

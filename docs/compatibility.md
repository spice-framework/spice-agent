# Compatibility policy

Spice Agent is pre-1.0. Its compatibility policy is enforced now so changes are
reviewable; it is not a stability promise.

## Canonical records

| Contract | Machine-readable source |
| --- | --- |
| policy and v1 blockers | `compatibility/policy.json` |
| public Go packages and reviewed breaks | `compatibility/go-api.json` |
| durable state identities and hard cuts | `compatibility/durable.json` |
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

- The preview5 annotated tag exists and resolves to commit
  `3e8fe6406171a7e7f1765311a4fa7fc3b878e425`.
- The public module proxy resolves preview5.
- Release workflow run `31343998056` completed validation, rendering,
  independent verification, keyless attestation, provenance authentication,
  and protected publication.
- The non-draft GitHub prerelease contains the exact five-asset module set:
  source archive, SPDX SBOM, release metadata, checksums, and portable Sigstore
  provenance bundle.
- Fresh proxy and SumDB resolution yields module sum
  `h1:rGND9DYx3pssliD1tZQOvPDOZ5GVfQLDc7VJQI3HLOM=` and go.mod sum
  `h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=`.
- Engine 1.2/1.3 and plugin 1.0 evidence is source-built. No released-binary
  N/N-1 matrix is claimed.

## What remains before v1

The repository must still prove external extension authorship, publish and test
two supported released generations of both protocols, freeze the generated
source ownership contract with an immutable generator. Stable kernel benchmark
ceilings and the stricter 20% time/10% allocation investigation policy are now
machine-readable and enforced by `make verify`. See
[RFC 0010](rfc/0010-api-and-protocol-compatibility.md).

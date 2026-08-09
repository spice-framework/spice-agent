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

The recovery release is `v0.1.0-preview.2`. It pins the organization workflow
that separates uncredentialed public-proxy tool bootstrap from subsequent
offline candidate verification. The old metadata version and caller workflow
commit are explicit negative quality-gate fixtures so they cannot silently
become current again.

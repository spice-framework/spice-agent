# Phase 8 immutable generated-source contract

The generator identity is no longer a development pseudo-version. Toolchain
annotated tag object `210b56ba89c127e5156ebb3b2d0ac363f9c8c2c4` resolves to commit
`bab8bcaf7d0c6311237b34812c681c3ee6a6593b`; release run `31403311626`
published prerelease `v0.1.0-preview.2`. The Agent contract records exact module
sum `h1:Hv/Ur+Uc3cG00jVCo/R5zINZ1w33jH0O6/ekeNOrFyk=` and go.mod sum
`h1:LZ04RGO793x7rSetV5T8xZnGvXjbI8u6WCyzdwN2wOI=`.

`compatibility/generated-source.json` freezes the reviewed Toolchain contract:

- ownership manifest schema 6 and Go formatting line 1.26;
- guarded input migration from schemas 1 through 6;
- manifest-only ownership and rejection of manual edits;
- stale-file removal only when the owned hash still matches;
- module-relative, forward-slash, case-fold-unique paths; and
- byte-identical output for identical input.

The root composition proof and seven nested experiment proofs select the exact
released module and sums in `go.mod`, `go.sum`, and `vendor/modules.txt`. Their
ownership manifests migrated from schema 5/`0.1.0-dev` to schema 6/preview2.
The generator reported zero generated Go writes and zero stale removals for all
eight targets: the migration changed ownership identity, not application
construction behavior.

The quality gate rejects generator, sum, schema, target, module-root, manifest,
vendor, path-policy, or proof-state drift. Ordinary generation checks continue
to prove current bytes and the existing acceptance suite proves deterministic
regeneration plus manual-edit preservation.

This closes the immutable-release and repository-migration portion of the v1
criterion. It does not claim the public-authoring boundary: the same released
generator must still be exercised by the three isolated clean-room extension
proofs before `clean-room-generated-source-exercise` can be removed.

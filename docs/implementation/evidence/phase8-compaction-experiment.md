# Phase 8 deterministic compaction experiment evidence

## Boundary

The `experiments/compaction` nested module consumes only
`spice-agent v0.1.0-preview.5` and `spice v0.1.0-preview.2`, contains no
`replace`, and leaves the root product graph and release metadata unchanged.
Its generated `CompactionProof` application explicitly constructs a concrete
base provider, semantic options, one `model.Provider` wrapper, and its consumer
through ordinary direct-call Spice injection.

Compaction is restricted to the transient immutable request copy received by
that wrapper. It selects only an old contiguous prefix of complete assistant
tool-call plus correlated tool-result rounds, retains the configured newest
rounds, and substitutes one bounded deterministic local extract. It never
performs another model call and has no tool, process, filesystem, network,
interaction, registry, scheduler, storage, or protocol authority.

## Proven invariants

- incomplete, mismatched, noncontiguous, and non-tool messages are preserved;
- operation ID, model selection, tool definitions, caller request values, and
  the one delegate call remain exact;
- summary identity, source digest, extraction order, truncation, collision
  handling, and byte accounting are deterministic and bounded;
- cancellation visible before delegation prevents the provider call;
- a real two-turn engine/tool loop gives only its second delegate request the
  compacted view while the terminal v1alpha3 snapshot retains the original user,
  assistant call, and tool result;
- authoritative model/tool/run event occurrences remain complete and ordered;
- `SemanticIdentity` changes when any compaction semantic option changes and
  fits the public snapshot compatibility bound;
- generated Go constructs and selects the wrapper with no registry or runtime
  discovery; and
- compatibility, offline vendor, deterministic fuzz, race, coverage, and
  provisional benchmark paths are repository-owned.

## Verification and deletion

The nested `make fast`, `make check`, `make verify`, and `make benchmark` are
also entered by the root quality gate. Completion evidence records the exact
commands and commit only after the final tree is green.

This experiment does not stabilize a compaction policy or claim provider token
quality. Delete the nested directory plus these ledger links to remove it; no
core package, schema, root module graph, release artifact, or generated product
target depends on it.

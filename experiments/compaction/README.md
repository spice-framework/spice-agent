# Deterministic model-context compaction experiment

This removable Phase 8 module proves that model-context compaction can remain
an ordinary, application-owned `model.Provider` wrapper. It does not add a
kernel compactor, mutable conversation store, hidden model request, runtime
registry, scheduler, or protocol concept.

The wrapper examines only the immutable request copy passed to `Stream`. Above
an explicit byte threshold, it may replace an old contiguous prefix of complete
assistant tool-call plus correlated tool-result rounds with one bounded local
extract. The newest configured rounds remain byte-for-byte intact. An
incomplete, mismatched, noncontiguous, or non-tool occurrence is never dropped.
The extract records a SHA-256 over every selected source field, includes only a
bounded transcript prefix, and uses a deterministic collision-free message ID.

The engine remains authoritative. It keeps the original history, durable
events, tool occurrence facts, and v1alpha3 snapshot. The integration test
proves that the delegate sees a compact request while the exported terminal
snapshot and event ledger retain the full tool round. `SemanticIdentity` binds
the exact algorithm and options into an application's explicit snapshot
compatibility identity; changing those semantics therefore rejects portable
resume when the application uses the helper as documented.

The generated `CompactionProof` application constructs a concrete base
provider, options, the exact `model.Provider` wrapper, and its consumer through
ordinary direct-call Spice injection. There is no implicit auto-configuration:
an application must deliberately contribute the wrapper it wants to use.

## Deliberate limits

- Extraction is deterministic and local; it does not claim abstractive summary
  quality or provider token-accounting accuracy.
- Only complete tool rounds are eligible. Large standalone prompts and partial
  operations remain untouched.
- The summary is still model input and may contain a bounded prefix of data that
  was already present in the provider request. It is never logged or emitted.
- The wrapper performs exactly one delegate call and no model, tool, process,
  network, filesystem, or interaction call of its own.
- This pre-1.0 experiment does not stabilize the option or report APIs.

## Verification and deletion

`make fast`, `make check`, `make verify`, and `make benchmark` are deterministic
and offline after explicit dependency bootstrap. Verification includes
generated-source freshness, shuffled and race tests, deterministic fuzz
executions, at least 85% handwritten package coverage, and fresh vendor proof.

Delete this directory and its root ledger/evidence links to remove the proof.
No root module dependency, release metadata, generated product target, protocol,
or kernel package depends on it.

# Phase 8 Agent logging promotion evidence

## Promotion boundary

The former `experiments/telemetry` nested module established the safety model
now implemented by the root production `logging` package and explicit
`logging/autoconfigure` adapter. The experiment directory, generated target,
vendor graph, local JSONL exporter, and quality-gate entry are retired.

Production projects Agent events through one bounded best-effort mailbox and
one consumer directly into the injected Spice-native structured logger. It is
diagnostic-only and does not claim OpenTelemetry or distributed trace parity.
There is no network, global provider, storage, retry, execution authority, or
durable observer.

## Proven invariants

- generic event payloads, model text, arguments, results, paths, errors,
  provider metadata, credentials, and raw tool names never enter logs;
- stable rich decoding is limited to the public versioned tool occurrences;
- process-local HMAC pseudonyms provide deterministic in-process correlation
  without publishing run or call identities;
- model deltas and, by default, tool progress are filtered before enqueue;
- filtered high-volume events and overflow drops are counted separately;
- malformed typed occurrences emit fixed warnings without raw decoder text;
- handler failure, panic, blocking, and cancellation remain diagnostic-only;
- accepted envelopes drain after producers stop when the handler cooperates;
  and
- the passive fixed-code health adapter is excluded from readiness by default.

## Deliberate exclusions

Metrics, spans, batching, exporter abstractions, local-file output, and the
experimental local JSONL schema were not promoted. A future OpenTelemetry
adapter still requires an explicit trace-context contract and separate
dependency, cancellation, security, and network review.

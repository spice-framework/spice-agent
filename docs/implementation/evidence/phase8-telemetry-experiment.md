# Phase 8 telemetry experiment evidence

## Boundary

The `experiments/telemetry` nested module pins released Agent preview5 and Spice
preview2 without `replace`. It leaves the root product graph, public API,
protocols, and release metadata unchanged.

The experiment projects Agent events through one bounded best-effort mailbox
and one consumer into immutable secret-safe metrics, complete run/tool spans,
and fixed lifecycle logs. It is intentionally exporter-neutral and does not
claim OpenTelemetry or distributed trace parity. There is no network, global
provider, storage, retry, execution authority, or durable observer.

## Proven invariants

- generic event payloads, model text, arguments, results, paths, errors,
  provider metadata, credentials, and raw tool names never enter telemetry;
- stable rich decoding is limited to the public versioned tool occurrences;
- process-local HMAC pseudonyms provide deterministic in-process correlation
  without exporting run or call identities;
- batches, bytes, mailbox, correlations, timeouts, and shutdown are bounded;
- a slow exporter creates exact accounted mailbox drops without run
  backpressure, and failed exports are never retried;
- malformed, mismatched, impossible-order, dropped-start, and orphan-terminal
  events never fabricate spans;
- generated Spice construction uses an actual Agent engine and proves engine
  shutdown precedes mailbox close, final accepted-event drain, and exporter
  shutdown; and
- the passive fixed-code health adapter is excluded from readiness by default.

## Promotion and deletion

Promotion requires stable safe identities beyond tools, an explicit trace
context contract, and a separately reviewed injected OpenTelemetry adapter.
Delete the nested directory plus its root ledger links to remove the experiment;
no root dependency, kernel package, schema, release artifact, or product target
depends on it.

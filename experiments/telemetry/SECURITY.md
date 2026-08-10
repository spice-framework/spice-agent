# Telemetry security boundary

Telemetry is a lossy derived diagnostic view. It is not a confidentiality
filter, permission system, sandbox, audit log, recovery source, or availability
dependency.

- Generic `Envelope.Data` is never emitted. Only the public bounded tool
  occurrence decoders are used, and raw names/call IDs are still omitted.
- Correlations are fixed-length HMAC pseudonyms. Random process-local keying is
  the default; an application-supplied key is copied, never exposed, and must be
  treated as secret configuration.
- Records accept closed enums and fixed scalar values, not arbitrary maps,
  attribute names, log bodies, error text, paths, or provider metadata.
- Exporter errors and panics become fixed internal counters. Their text never
  reaches records, health, or cleanup errors.
- The local JSONL exporter writes only to a caller-owned writer. It does not
  open paths, discover endpoints, use credentials, or create network clients.
- Batches, mailbox capacity, correlations, timeouts, record counts, and bytes
  are bounded. Export calls are serial and are never retried.
- A trusted exporter must honor context cancellation. If it does not, the
  application shutdown context is the honest containment limit.

Secret-canary, malformed occurrence, slow exporter, panic, cancellation, drop,
race, and deterministic output tests enforce these properties. Security
reports follow the repository root policy.

# Compaction security boundary

This experiment is a data transformation inside one already-authorized model
request. It is not a confidentiality filter, permission system, sandbox, or
durable erasure mechanism.

- It never sends an additional model request or invokes tools, processes,
  network clients, filesystems, or interaction brokers.
- It never logs, emits, persists, or returns source content through `Report` or
  errors. The report contains only counts, byte totals, and a digest.
- The bounded extract may contain source text and structured tool data because
  it replaces those values in the same provider request. Applications must not
  route the wrapper to a provider with broader authority than the delegate it
  replaced.
- Incomplete and uncertain tool rounds remain intact. Compaction cannot conceal
  an open operation from snapshot or recovery policy because authoritative
  engine state is never changed.
- Summary and trigger limits reject zero, partial, negative, and unbounded
  configuration. Cancellation visible before delegation returns without calling
  the provider.
- Semantic option changes must flow through `SemanticIdentity` before portable
  snapshot import is enabled.

Security reports follow the repository root security policy. Promotion requires
independent provider review and adversarial prompt/context evaluation.

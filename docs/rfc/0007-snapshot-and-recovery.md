# RFC 0007: Snapshot and Recovery

**Status:** accepted for the preview in-memory contract; durable SQLite recovery
remains a Phase 7 stress proof. A snapshot contains validated
provider-neutral run state and the last event sequence; it excludes clients,
secrets, functions, processes, and mutable registries. Import validates schema,
identity, monotonic sequence, and terminal state. Uncertain mutating operations
are recorded explicitly and never replayed automatically.

The `v1alpha1` snapshot is deterministic, immutable, and bounded. It contains
only run ID, definition identity/model/turn limit, completed turn count,
provider-neutral message history, sorted static plan identities, dynamic
generation identity, sorted previously used interaction IDs, last committed
sequence, and a safe lifecycle status. It
never serializes providers, tools, brokers, contexts, functions, credentials,
process handles, clients, logs, or mutable registries. Conversation content is
application data and persistence extensions must protect it accordingly.

Export succeeds only while suspended at a fully completed turn boundary or
after a run terminal. Duplicate message/call IDs, unmatched tool calls/results,
unknown fields, unsupported versions, active state, pending interactions,
unresolved tool mutations, invalid roles, and oversized content fail closed.

Only a suspended snapshot may resume. Static plan identities supplied by
generated applications include the bean's module, version, and source identity;
tool entries additionally fingerprint the exact name, description, schema bytes,
and capability set. The importer requires exact static plan
and dynamic generation identities, exclusive ownership after the original run
authority is unavailable, and a previously unseen run ID in that engine. Resume
does not emit a second `RunStarted`; it continues at `last_sequence+1`, starts
the next turn, and derives run-scoped model/message identities so a fresh process
cannot reuse earlier lifecycle IDs. Terminal snapshots are inspectable but never
resumable. Automatic replay of uncertain mutating work is deliberately excluded.
An imported event tail has no copy of pre-snapshot events, so subscriptions with
older cursors fail explicitly with the snapshot sequence as the recovery cursor.
The generation field records identity for host validation; it is not itself a
process lease or proof that a plugin generation remains alive.

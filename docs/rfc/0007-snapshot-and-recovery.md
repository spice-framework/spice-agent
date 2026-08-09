# RFC 0007: Snapshot and Recovery

**Status:** accepted for the preview in-memory contract; durable SQLite recovery
remains a Phase 7 stress proof. A snapshot contains validated
provider-neutral run state and the last event sequence; it excludes clients,
secrets, functions, processes, and mutable registries. Import validates schema,
identity, monotonic sequence, and terminal state. Uncertain mutating operations
are recorded explicitly and never replayed automatically.

The `v1alpha3` snapshot is deterministic, immutable, and bounded. It contains
only run ID, definition identity/model/turn limit, completed turn count,
provider-neutral message history, one combined plan identity, sorted previously
used interaction IDs, last committed sequence, and a safe lifecycle status. The
plan identity contains sorted compiled bean identities, the exact tool-plan ID,
an explicit generated snapshot-compatibility identity, a canonical workspace
SHA-256, and a canonical fingerprint over all of them plus the leased tool
definitions. It
never serializes providers, tools, brokers, contexts, functions, credentials,
process handles, clients, logs, or mutable registries. Conversation content is
application data and persistence extensions must protect it accordingly.

Export succeeds only while suspended at a fully completed turn boundary or
after a run terminal. Duplicate message/call IDs, unmatched tool calls/results,
unknown fields, unsupported versions, active state, pending interactions,
unresolved tool mutations, invalid roles, and oversized content fail closed.

Every process-boundary envelope is signed by a trusted snapshot authority. Its
public claim contains an exact 32-byte scope ID, a positive key generation, and
an exact 32-byte HMAC-SHA256. The canonical MAC input is a sequence of unsigned
64-bit big-endian length-prefixed components: domain
`spice.agent.run-authority/v1`, scope ID, generation encoded as eight-byte
big-endian, format, run ID, last sequence encoded as eight-byte big-endian,
lifecycle encoded as four-byte big-endian, and the already-validated payload
SHA-256. The authority HMAC and unknown Protobuf fields are not self-signed.
Construction cannot produce an unsigned envelope. Import requires a keyed
verifier; structure and the unkeyed payload digest never establish authority.

Only a suspended snapshot may resume. Compiled plan identities supplied by
generated applications include the bean's module, version, and source identity.
The leased definition fingerprints cover the exact tool contracts. Before event
log creation or engine mutation, the importer leases the recorded `ToolPlanID`
and recomputes the combined `PlanIdentity`; missing, substituted, or changed
plans fail closed and release the candidate lease. Resume additionally requires
exclusive ownership. In the same daemon, a known active, suspended, terminal,
or tombstoned run ID conflicts deterministically; import never replaces that
authority. Across processes, import is permitted only after the caller
establishes that the original run authority is dead, and the destination engine
must not have seen the embedded run ID. The destination additionally resolves
the scope and generation to a server-owned key and verifies the HMAC before any
lease, tombstone, event-log, or engine mutation. Unknown scopes, unavailable
generations, and incorrect HMACs share one secret-safe failure. Resume
does not emit a second `RunStarted`; it continues at `last_sequence+1`, starts
the next turn, and derives run-scoped model/message identities so a fresh process
cannot reuse earlier lifecycle IDs. Terminal snapshots are inspectable but never
resumable. Automatic replay of uncertain mutating work is deliberately excluded.
An imported event tail has no copy of pre-snapshot events, so subscriptions with
older cursors fail explicitly with the snapshot sequence as the recovery cursor.
The plan ID records immutable host identity; successful resume requires a real
live lease and never treats serialized identity as a process handle.

Durable recorders classify tool operation state from the versioned local
occurrences in RFC 0002. A `ToolStarted` without one correlated
`ToolCompleted`/`ToolFailed` occurrence is open or crash-interrupted; it is not
silently treated as failed. Terminal occurrence data is bounded and secret-free
and retains safe typed uncertain/retry facts when the dispatcher supplied them.
This makes crash-marker and uncertain-operation decisions possible without
persisting executable arguments, tool output, or free-form failure text.

Convenience engine constructors intentionally leave snapshot compatibility
empty. Their runs support local suspend/resume and deterministic export for
inspection, but another engine refuses import. Generated applications must set
one semantic compatibility identity and compiled identities for every
executable provider, stage, observer, broker, static tool, and decorator bean.
Static compatibility mismatches are rejected before `LeaseGeneration` can
launch or revive dynamic resources.
Portable compatibility also requires the engine workspace fingerprint to match
before any lease or identity mutation. `v1alpha2` lacks this authority fact and
is deliberately rejected rather than upgraded implicitly.

HMAC authority is integrity and provenance, not encryption. The OS-backed
authority stores a random scope ID and a distinct random key separately from
local IPC tokens. Its authority-key generation is the generation signed on the
wire; an independent per-run transition generation prevents replay without
pretending the key rotated. A domain-separated per-run HMAC subkey binds the
scope, run ID, and local transition generation, so even otherwise identical
snapshots from different suspend generations have different claims. Stable
per-run lock files are never unlinked. The authority binds and retains the
validated authority-directory identity: Unix operations are descriptor-relative
and Windows operations are handle-relative, so renaming or substituting a path
cannot redirect authority state. Every open also validates the complete
ancestry against OS ownership and delete/ACL-mutation rights and proves the
walked final object is the retained object, preventing rollback by a different
unprivileged principal after close/reopen. Windows trusts the volume namespace,
current user, SYSTEM, Administrators, and TrustedInstaller; Unix trusts root and
the current user, with sticky-directory semantics handled explicitly. Same-user
and elevated compromise are outside the per-user authority boundary. Authority
creation sets a newly created Unix leaf to `0700`; opening an existing leaf is
validation-only and requires it already be exactly `0700`.
Authority shutdown rejects new work and drains
outstanding run/import leases before releasing that identity, then clears the
canonical in-memory HMAC key. Proven cancellation at the final pre-write
context check remains retryable; a failure after filesystem-writer invocation
is uncertain even when the caller observes cancellation, because persistence
or its final durability barrier may already have completed.
`ACTIVE` cannot be imported, `SUSPENDED` records bind the exact digest and
authority HMAC, `IMPORTING` is a durable consume point, and terminal records are
non-resumable tombstones. Snapshot export remains logically read-only to kernel
state but is authority-gated: signing `SUSPENDED` retains exclusive ownership,
identical repeat export is idempotent, and a differing claim is rejected. Local
resume durably writes `ACTIVE` at the next generation and invalidates the old
claim while the kernel is still suspended; only then may the host resume the
kernel. Import prepares and verifies while holding the lock,
then the host consumes, commits its prepared kernel run, and activates. Failure
before consume is abortable; failure after consume is explicitly uncertain and
must not be retried automatically. In particular, an uncertain Activate after
kernel commit retains the import transaction lock: the host must stop and join
the committed kernel run before Abort/Close releases that lock, and execution
must never continue without a returned active authority lease. Same-scope
recovery is supported; copying a
snapshot to another user scope fails closed.

# Phase 4 run-authority evidence

## Implemented boundary

`daemon.RunAuthority` is an opaque, constructor-injectable singleton backed by
current-user-only local storage. The store creates a persistent random 32-byte
scope ID and a distinct 32-byte HMAC key, both independent of endpoint
authentication. Snapshot claims carry the persistent authority-key generation;
signed local records separately carry a per-run transition generation. A
domain-separated HMAC subkey derived from scope, run ID, and local transition
generation binds that counter without misrepresenting it as key rotation on the
wire.

The authority also validates the complete path ancestry on every open so an
older authority directory cannot be substituted between store lifetimes.
Unix accepts only root/current-user-owned ancestry without group/other write,
except a sticky directory whose next component is also root/current-user-owned.
Only a newly created authority leaf is set to `0700`; an existing leaf must
already be exactly `0700` and is rejected without permission mutation otherwise.
Windows inspects the volume root and every component: owners must be the current
user, SYSTEM, Administrators, or TrustedInstaller, and no untrusted applicable
ACE may grant delete, delete-child, DACL/owner mutation, or generic-all rights.
Unknown object/callback ACE forms fail closed. The final component opened by
the ancestry walk must have the same volume/file identity as the retained
authority handle. The Windows volume namespace and root/SYSTEM/Admin/
TrustedInstaller principals, Unix root, and the current OS user are explicit
trust anchors. A same-user or elevated attacker is outside this local
current-user boundary because it can already read or replace key material.

Every run uses a filename derived from `SHA-256(run_id)`. The original run ID is
inside the signed record. Its lock file has stable identity and is never
unlinked. The store retains the validated authority-directory identity for its
whole lifetime. Windows uses `LockFileEx`, protected current-user DACLs,
local-volume checks, reparse-component rejection, post-open canonical-path
binding, handle-based owner/DACL checks, single-link validation, handle-relative
`NtCreateFile`/`NtSetInformationFile`, and `FlushFileBuffers`. Linux and macOS
use a retained directory descriptor, component-by-component no-follow opens,
`openat`, owner/mode/link validation, `flock`, file sync, atomic `renameat`, and
directory sync. Renaming or substituting the original path cannot redirect
record reads, lock acquisition, or atomic replacement to another directory.

## Transaction contract

1. Start holds the stable lock and persists `ACTIVE`.
2. Signing a suspended envelope persists the exact signed `SUSPENDED` digest
   while retaining the run lock and active lease. Repeating the identical
   export is idempotent; a differing export at the same suspension is rejected.
   A maximum-generation run cannot issue a resumable snapshot. Signing a
   terminal envelope persists a tombstone and releases ownership.
   `ActiveRun.IssueSnapshotEnvelope` is the typed daemon boundary: it validates
   and deterministically encodes an `agent.Snapshot`, derives identity,
   sequence, and lifecycle from that value, and returns only a fully validated
   signed envelope. `ActiveRun` does not expose raw signing or implement the
   public signer interface, so callers cannot provide parallel wire metadata.
3. Local Resume first persists `ACTIVE` at the next run generation while the
   kernel remains suspended, invalidating the prior snapshot and retaining the
   lock. Only after that succeeds may the host resume kernel execution. If the
   authority write is uncertain, the kernel stays suspended and only Close may
   release the uncertain owner. That first Close reports uncertainty while
   releasing the lock/lease; repeated Close is idempotent.
4. After the suspended owner closes or dies, PrepareImport acquires the stable
   lock, verifies the signed record and keyed
   envelope, and changes no durable state.
5. The host prepares its kernel resume, then Consume persists `IMPORTING` and
   increments only the local run generation.
6. The host commits the prepared kernel run, then Activate persists `ACTIVE`
   and transfers the held lock to the returned active lease.

Because kernel commit precedes Activate, an uncertain Activate failure may
leave a committed kernel run without a proven durable `ACTIVE` transition. The
transaction deliberately retains its stable lock. The host must cancel, stop,
and join that kernel run before calling Abort/Close to release the transaction;
it must never let execution continue without an `ActiveRun` lease.

Abort is safe before Consume. Abort or failure after Consume is uncertain and
the snapshot must not be retried. A platform write error after the Consume
attempt is also uncertain because replacement may have committed before a late
durability error; the transaction enters a terminally uncertain state, every
later verify/consume/activate returns the uncertainty error, and only
Abort/Close releases its lock. The store also fences same-process retries. An `ACTIVE`,
`IMPORTING`, terminal, tampered,
wrong-scope, wrong-digest, replayed, or concurrently locked run fails closed.
`NewRunAuthority` establishes an owned OS resource. `RunAuthority.Close` begins
shutdown, rejects new work, and reports a busy
authority while a run/import lease remains. The final lease release closes the
retained directory identity; later closes are idempotent. This is the cleanup
contract required for generated Spice singleton lifecycle wiring; callers must
not rely on garbage collection or finalizers. Final close clears the canonical
in-memory HMAC key after all leases drain.

The persistence boundary distinguishes cancellation proven before the
filesystem writer is invoked from ambiguous write failure. A final context
preflight occurs immediately before invocation. Proven pre-write cancellation
does not change owner state and Resume/Terminal may be retried; once invocation
begins, any returned failure is uncertain because replacement or its durability
barrier may already have committed.

Snapshot issuance uses commit-wins cancellation. Proven pre-write cancellation
of a valid active run remains retryable and returns the canonical context
sentinel. Cancellation cannot mask a wrong-run or closed-lease state, an
ambiguous write, or a post-tombstone cleanup failure. Once a signer returns a
valid claim, cancellation observed afterward cannot discard that durable
result. Signer failures are secret-safe: only canonical context cancellation
returned by the signer is propagated; every other signer detail is replaced by
the generic signing sentinel. The daemon then maps the authority's race-safe
classification to `ErrRunAuthorityState`, `ErrRunAuthorityUncertain`, or
`ErrRunAuthorityUnavailable`. Uncertainty has precedence over cleanup failure
and is never retried automatically.

## Verification scope

Tests cover persistence, distinct secret material, secret-safe failures,
suspend/import/activate order, independent generations, replay, wrong scope,
tampering, cancellation, concurrent ownership, consumed uncertainty, terminal
tombstones, stable lock identity, hard links, intermediate reparse/symlink
components, concurrent first-open initialization, directory path substitution,
close/reopen rollback through unsafe ancestry, close/drain concurrency,
non-mutating rejection of an existing Unix leaf with unsafe permissions,
idempotent/differing suspension, local-resume invalidation and generation,
pre-write cancellation recovery, uncertain first-close reporting, in-memory
key clearing, typed kernel-snapshot envelope issuance, byte-identical concurrent
suspended retries, terminal lifecycle mapping, cancellation/state races,
uncertain and unavailable issuance classification, post-tombstone cleanup
failure, secret containment, public API rejection of raw signing, injected
randomness and atomic-write failures, and helper-process crashes while
suspended, before activation, and after activation. Exact gate
commands and timings are recorded with the commit that accepts this slice; this
document does not claim unexecuted macOS runtime evidence.

# Recovery threat model

The database is untrusted local input. The experiment authenticates no user and
does not encrypt storage. Applications must place the file in user-private local
state and protect it using operating-system permissions. A process with write
access can delete data and can cause recovery refusal; digests detect accidental
or partial corruption, not a malicious writer that can recompute hashes.

Recovery fails closed. It will not execute stored code, tool arguments, provider
metadata, or UI values. Events are bounded by the released Agent contracts;
tool operation rows store only typed non-secret occurrence facts. Unknown
schema versions, missing routes, identity drift, interactions, mutations, open
calls, and uncertain retries are refused. `trusted_schema=OFF`, prepared SQL,
strict tables, application/user version tags, foreign keys, exact sequence
checks, and a 100,000-event validation bound constrain hostile input.

WAL/FULL durability narrows crash loss but cannot prove a remote filesystem,
broken disk, or hostile kernel. Ambiguous commit acknowledgment requires an
exact read-after proof. A crash marker does not authorize replay. Branches do
not inherit executable authority; applications must construct a new run with
the recorded new identity.

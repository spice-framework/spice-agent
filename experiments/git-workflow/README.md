# Guarded Git workflow experiment

This removable Phase 8 module proves that a narrow Git workflow can remain an
ordinary application extension. It contributes exactly two tools through
generated Spice injection:

- `git.inspect` runs fixed porcelain-v2 status and staged-index inspection;
- `git.commit_staged` commits only the index that an exact run-owned
  interaction approved.

The commit guard binds a deterministic authority token to run, turn, leased
tool plan, combined plan fingerprint, workspace fingerprint, tool definition,
call arguments, and staged-index digest. Its in-memory grant exists only around
the terminal continuation and is consumed once by the commit tool. The runner
serializes commits and rechecks the staged digest immediately before launching
the fixed command.

There is no `add`, `reset`, `checkout`, `merge`, `rebase`, `push`, `fetch`,
network operation, arbitrary Git argument, shell, hook, signer, pager, editor,
credential helper, or hidden configuration discovery. `git.commit_staged`
always uses `--no-verify`, `--no-gpg-sign`, `--file=-`, an empty private hooks
directory, and an empty private global configuration. All application-supplied
`GIT_*` environment values are rejected. Repository-local configuration remains
part of the explicitly selected repository, but hooks, fsmonitor commands,
signing, credential helpers, pagers, editors, and automatic maintenance are
overridden by the fixed invocation.

## Deliberate preview5 limitation

The module pins the immutable public Agent `v0.1.0-preview.5`, which has
`process.Launcher` but no atomic digest-pinned `VerifiedLauncher`. An
application must configure one trusted canonical absolute Git executable and
its SHA-256. The runner holds file/directory identity handles and verifies the
executable digest and executable/repository/config identities before and after
every command. This detects substitution but cannot eliminate the pathname
race between verification and operating-system child creation. Consequently
the experiment is not promotable until a later immutable Agent release
contains the generic verified-child seam and this runner migrates to it.

The experiment-owned launcher gives every child a containment boundary: a
process group on Linux/macOS and a kill-on-close Job Object on Windows. A
post-launch failure for the mutating operation is conservatively
`ExecutionUncertain`/`RetryNever`; no automatic replay occurs.

## Verification and deletion

`make fast`, `make check`, `make verify`, and `make benchmark` run offline from
committed vendor contents. Verification covers real staged-only Git commits,
hook/signing suppression, authority denial/cancellation/panic, index changes,
executable/config substitution, output bounds, concurrent grants, process-tree
containment, deterministic fuzz smoke, race tests, generated Spice injection,
and at least 85% handwritten package coverage.

Delete this directory and its root ledger/evidence links to remove the proof.
No root dependency, protocol, release artifact, or product target imports it.

# Phase 8 guarded Git workflow experiment evidence

## Boundary

The removable `experiments/git-workflow` nested module pins released Agent
preview5 and Spice preview2 without `replace`. It adds no root dependency,
protocol, kernel concept, release artifact, or product target. Generated
`GitWorkflowProof` composition injects exactly the `git.inspect` and
`git.commit_staged` tool beans plus one terminal `ToolDispatchGuard` through
ordinary public Spice contracts.

Inspection uses only fixed porcelain-v2 status and staged-index commands.
Commit accepts one bounded message on stdin and can commit only the exact staged
index digest approved through a run-owned interaction. Its deterministic token
binds run, turn, exact leased tool plan, combined plan/workspace fingerprints,
definition, call, arguments, and staged digest. Grants are single-use and exist
only around the guarded continuation.

## Proven invariants

- no model-controlled executable, option, ref, revision, pathspec, environment,
  hook, signer, editor, pager, credential helper, shell, or arbitrary argument;
- no add/reset/checkout/merge/rebase/push/fetch or network operation;
- application-supplied `GIT_*` values fail closed, while system/global config,
  hooks, prompts, credentials, signing, and optional read locks are disabled;
- repository/executable/private-config identities are held and checked before
  and after use, executable SHA-256 is exact, output is bounded, and hook
  directories remain empty;
- a changed staged index cannot launch commit, denial/panic/cancellation cannot
  leave a grant, and concurrent call grants remain isolated;
- post-launch mutating failures are uncertain/retry-never and never replayed;
- experiment-owned Windows Job Object and Unix process-group adapters contain,
  stop, and join a real descendant process tree; and
- generated freshness, offline vendor, compatibility, deterministic fuzz,
  race, coverage, and provisional benchmark paths are root-owned gates.

## Historical promotion boundary

Agent preview5 exposes `process.Launcher` but not the later public
`process.VerifiedLauncher`. Held identity plus strict pre/post digest checks
detect substitution but cannot atomically bind the verified file handle to
child creation. This is explicit experimental evidence, not a claim of
TOCTOU-resistant execution. At the time of this proof, promotion was blocked
until the module could pin an immutable Agent release containing that generic
seam and migrate without duplicating it or weakening behavior.

Agent `v0.1.0-preview.6` later published that immutable generic seam. The
release-availability prerequisite is therefore closed, while this experiment's
preview5 pin and historical proof remain unchanged. Promotion would still
require an explicit preview6-or-later repin, migration to the public lease and
launcher, and a complete rerun of the experiment's security and compatibility
evidence; it is not implied by the later release alone.

## Verification and deletion

The nested `make fast`, `make check`, `make verify`, and `make benchmark` are
also entered by the root quality gate. Completion evidence records the exact
commit and command results only after the final tree is green on Windows and
hosted Linux/macOS compile and behavior paths.

Delete the nested module and these ledger links to remove it. Nothing in core
or distribution imports the experiment.

# Phase 4 provider-neutral process-launch contract

## Implemented boundary

Package `process` defines the mandatory constructor-injected seam shared by
managed daemon startup and compiled coding tools. `Spec` validates an exact
lexically canonical absolute executable and working directory, copied discrete
arguments, a canonical copied environment, explicit standard streams, and a
sorted capability set containing `process.execute`. It performs no filesystem
or network access. Values are bounded, immutable through public APIs, and
redacted from formatting and JSON.

Natural names and relative workspace executables remain supported through the
separate `ExecutableResolver` seam. Its immutable redacted `Lookup` carries the
unmodified request, canonical absolute workdir, and exact copied environment.
The resolver uses no ambient state or network access and returns the canonical
absolute path that is subsequently validated by `Spec`. Core contains no path
resolver or process launcher.

`Launcher.Start` is bounded only by its launch context. A returned `Process`
owns the independent lifetime even when Start also reports an error. `Done` and
typed `Outcome` describe only the root process. `Wait` separately proves that
all resources owned by an implementation are safe to release. Stop, kill, and
join failures do not silently surrender ownership. Retryable joins can be
re-proved; an explicitly non-retryable managed-candidate join is retained for
manual recovery without replaying cleanup calls.

Portable outcomes distinguish ordinary exited, signaled, and unknown roots and
provide an optional unsigned-32-bit-compatible exit code without exposing
`*exec.ExitError`. Typed failures preserve `errors.Is`/`errors.As` and
cancellation identity while all ordinary formatting remains secret-safe.

Digest-pinned child processes use the independent `VerifiedLauncher` contract.
`VerifyExecutable` validates a canonical path and nonzero `SHA256`, opens the
leaf without following a final symlink/reparse point, proves a regular
executable file, hashes that open object with cancellation, and holds its
platform identity. `ExecutableLease.ValidateSpec` prevents path substitution in
the immutable process intent; `DuplicateForLaunch` supplies a caller-owned
exact descriptor to trusted Linux launchers. `MaterializeForLaunch` supports
Darwin without reopening the configured pathname: it creates a random
mode-0700 directory, copies only from the verified duplicate, syncs and closes
the writer, verifies the expected digest again, and returns a second lease that
owns exact nonrecursive cleanup. `Recheck` supports the Windows
suspended-create/pre-resume boundary and post-launch defense. There is no
adapter from ordinary `Launcher`, so security-sensitive consumers fail at
construction instead of falling back.

## Verification scope

Tests cover immutable lookup/spec copies, name/relative/absolute requests,
environment/capability ordering, redaction, canonical digest parsing,
verification cancellation, duplicate/materialization/close boundaries,
partial-failure and cancellation cleanup, symlink/reparse rejection,
mutation/identity drift, and real Windows, Linux, and hosted Darwin
substitution attempts in which only the leased image executes,
required streams and capabilities, malformed and oversized values, exit-code
boundaries, launch-context independence, root/join separation, retryable and
terminal containment failures, and dependency direction. Fuzz targets exercise
lookup, specification, and outcome validation. On Windows/amd64 with Go 1.26.5,
focused tests, race tests, and vet passed for `./process ./client/managed`; all
three process fuzz targets passed two-second smoke runs; `make fast` passed in 68.4 seconds;
and the final `make check` passed in 61.3 seconds with repository architecture,
formatting, module/vendor, Protobuf, vet, and test gates green.

Commit `5d2fd63` passed the exact repository `make verify` gate in 152.5
seconds at 85.2% handwritten-product coverage. The gate included all three
process fuzz targets, shuffled and race-enabled tests, zero lint findings, no
reachable vulnerabilities, and vendor-offline execution under Go 1.26.5 on
Windows/amd64.

## Explicit exclusions

Core contains no `os/exec` launcher, process registry, reflective construction,
platform signal mapping, process-tree walker, cgroup, Job Object, or sandbox.
The contract does not promise universal descendant containment. Concrete
Windows and Unix launch/containment implementations and their adversarial
process-tree tests remain distribution responsibilities. Executable identity
does not pin loaders, DLLs/shared libraries, interpreters, argument-referenced
files, or same-user in-place mutation on Unix.

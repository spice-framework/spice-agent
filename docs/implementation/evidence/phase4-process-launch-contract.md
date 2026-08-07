# Phase 4 provider-neutral process-launch contract

## Implemented boundary

Package `process` defines the mandatory constructor-injected seam shared by
managed daemon startup and compiled coding tools. `Spec` validates an exact
lexically canonical absolute executable and working directory, copied discrete
arguments, a canonical copied environment, explicit standard streams, and a
sorted capability set containing `process.execute`. It performs no filesystem
or network access. Values are bounded, immutable through public APIs, and
redacted from formatting and JSON.

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

## Verification scope

Tests cover immutable copies, environment/capability ordering, redaction,
required streams and capabilities, malformed and oversized values, exit-code
boundaries, launch-context independence, root/join separation, retryable and
terminal containment failures, and dependency direction. Fuzz targets exercise
specification and outcome validation. On Windows/amd64 with Go 1.26.5, focused
tests, race tests, and vet passed for `./process ./client/managed`; both process
fuzz targets passed two-second smoke runs; `make fast` passed in 68.4 seconds;
and the final `make check` passed in 61.3 seconds with repository architecture,
formatting, module/vendor, Protobuf, vet, and test gates green.

## Explicit exclusions

Core contains no `os/exec` launcher, process registry, reflective construction,
platform signal mapping, process-tree walker, cgroup, Job Object, or sandbox.
The contract does not promise universal descendant containment. Concrete
Windows and Unix implementations and their adversarial process-tree tests
remain distribution responsibilities.

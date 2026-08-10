# Phase 5 Runtime-Plugin Host Security Foundation

## Implemented boundary

Package `plugin/host` now owns immutable runtime-plugin executable intent and
the filesystem verification lease used immediately around launch. A configured
plugin has one canonical identifier, exact expected manifest identity,
canonical absolute executable and working-directory paths, an exact SHA-256
pin, an explicit environment, approved capabilities, negotiated limits, and
bounded operation timeouts. Constructor input is defensively copied and every
ordinary formatter and JSON representation redacts paths, environment, and
digest material.

Executable arguments are deliberately absent. Pinning an interpreter while a
script supplied as an argument remains independently mutable would claim an
integrity property the host does not provide. Script-based plugins must be
packaged as a pinned executable artifact before production activation.

Immediately before launch, the host opens the leaf executable without
following a Unix symbolic link or Windows reparse point, proves it is a regular
executable file, hashes it with cancellation, and retains the file identity and
open descriptor/handle. Windows denies ordinary write and delete sharing while
the lease is live. Unix requires at least one execute bit. The generic values
now live in public package `process`; `pluginhost.SHA256` remains a compatible
alias. Host requires `process.VerifiedLauncher`, passes the exact candidate-owned
lease, and offers no ordinary `Launcher` fallback. Immediately after ownership
transfer it still reopens the path, compares platform identity, and hashes the
complete file before a candidate can become ready, authenticate, or activate.

The public `plugin/v1` bootstrap contract now provides one bounded,
newline-terminated deterministic JSON record carrying the local address and a
256-bit launch secret through private stdin. Decode rejects unknown, duplicate,
missing, malformed, oversized, or trailing input and returns caller-owned
secret bytes that must be cleared. The only permitted stdout record is the
exact readiness record. The fixture process consumes this same public contract,
so conformance and production framing cannot drift.

The host readiness sink recognizes the record across arbitrary write chunks,
never retains child stdout, keeps draining after failure, and treats every byte
after readiness as fatal contamination. The stderr sink always drains while
retaining at most a fixed 64 KiB internal prefix plus saturating byte-count and
truncation metadata; formatting and serialization expose metadata only.

## Verified-launch boundary

The public lease supplies a duplicated exact descriptor for Unix native launch.
Windows' held non-sharing handle prevents ordinary mutation/replacement, and a
native implementation must create suspended, recheck, then resume. Real test
executables prove that replacing the Unix path still runs only the leased image
and that Windows replacement is denied before the verified image runs. macOS
uses `/dev/fd/3`; the repository requires its hosted real-process job to pass
before claiming that platform complete.

This is executable-image integrity, not a sandbox or an execution-closure pin.
Dynamic loaders, DLLs/shared libraries, shebang interpreters, argument-named
files, and malicious same-user in-place Unix mutation remain outside the claim.

## Verification scope

Tests cover deterministic bootstrap bytes, strict decoding, independently
owned secret material, exact readiness and flushing, malformed and trailing
input, arbitrary readiness chunks, every mismatch position, same-write and
later stdout contamination, close and cancellation races, huge concurrent
output, bounded stderr, saturating accounting, immutable configuration,
environment and capability normalization, timeout and limit boundaries,
format redaction, digest mismatch, cancellation, directory rejection, Unix
symbolic-link/permission/mutation/replacement and descriptor-execution cases,
and Windows reparse-point/write-sharing and substitution behavior. The
repository architecture gate also forbids the host core from importing daemon,
client, engine, internal fixture, provider-facing kernel packages, or
platform-specific verifier dependencies; it locks the exact
`VerifiedLauncher` Host/autoconfiguration boundary.

Exact commit, gate timing, coverage, and platform evidence are recorded after
the repository-owned full verification gate passes on the committed tree.

## Explicit exclusions

This slice does not launch a process, allocate or dial local IPC, construct an
Initialize request, validate a candidate manifest, expose a remote `tool.Tool`,
activate a generation, lease a plan, restart a failed process, or drain and
join an owned process. Capability declarations remain metadata rather than a
sandbox. Those behaviors are subsequent bounded Phase 5 slices.

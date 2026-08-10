# RFC 0008: Security Responsibility and Capability Model

- **Status:** accepted for architecture-proof preview
- **Review trigger:** any remote listener, default policy, or sandbox claim

## Threat boundary

Spice annotations/tools, compiled extension modules, providers, in-process tools,
and runtime plugin executables are trusted code selected by Go module or explicit
digest configuration. Process separation, local authentication, capability
metadata, and digest pinning are not sandboxes.

The architecture-proof distribution deliberately gives coding tools the current
user process's privileges. It must warn on first run and in help/health output
that no permission or sandbox extension is active. Marketing and documentation
may not imply containment that the implementation does not enforce.

## Canonical interception

Every compiled or runtime tool call traverses one typed dispatcher and the
terminal guard seam in RFC 0009. Definitions
declare exact filesystem read/write, process, network, secret, and environment
capabilities. A permission guard can inspect definition, call, exact leased
plan, workspace, run, turn, and interaction facts and deny before execution.
When policy needs user input, it can invoke only the private run-bound requester
on `ToolDispatchScope`; it cannot provide or forge broker scope authority. The
request uses the normal interaction lifecycle and blocks tool completion until
one interaction terminal is committed. Tools cannot publish an alternate
executable route or event directly.

Each run uses one immutable leased definition snapshot. The guard rejects tool
names outside that snapshot even if a mutable or malicious delegate later
advertises or accepts them. Decorators cannot change the merged definition set,
and source/decorator panics are converted to bounded diagnostics without
including recovered values. Plan IDs are integrity identities, not trust or
sandbox claims, and must never be reused for different executable behavior.

Capability data is immutable, bounded, deterministic, and covered by generated
composition tests. It is useful for UI disclosure and policy input but provides
no enforcement until an explicit policy bean is installed.

Effect and replay metadata are also mandatory policy inputs, not inferred
security guarantees. Definitions marked read-only fail closed if they request
filesystem write, process execution, network access, or environment write.
Capabilities are a set with canonical order, so source declaration order cannot
change fingerprints, snapshots, or policy decisions. Replay-safe is reserved
for read-only work; mutating tools must explicitly choose idempotent or unsafe.

## Secrets and sensitive data

Credentials originate in secret-redacted typed configuration and are passed only
to the owning constructor/client. They are prohibited from:

- generated source and ownership manifests;
- compatibility/starter manifests, SBOMs, signatures, and provenance;
- errors, ordinary logs, events, snapshots, replay, plugin manifests, and TUI
  diagnostics;
- command argv or environment unless an explicit tool contract and policy allow
  it.

Provider metadata uses explicit namespace allowlisting and a documented safe
field set. Unknown headers/provider payloads are dropped, not forwarded.
Interaction events carry only identity, kind, and lifecycle status. Prompt text,
schemas, approvals, secrets, and user-entered response JSON never enter the
event log or replay stream; the validated response is returned directly to the
requesting stage. Interaction-requester panics become one fixed diagnostic and
never format the recovered value.

## Local daemon and plugins

Daemon endpoints and tokens are current-user only. This reduces accidental
cross-user access but does not protect against malicious code already executing
as the same user. Remote listen is absent. Plugin paths are absolute and digest
pinned with random per-launch secrets. The Host requires a verified-executable
lease launcher, never a pathname-only fallback, and retains the lease until
containment before activation.

## Supply chain

All modules pin exact direct/tool dependencies, commit reproducible vendor data,
verify with `GOWORK=off`, scan reachable vulnerabilities, and audit GitHub
manifest alerts. Releases are reproducible, signed, checksummed, SBOM-backed,
and covered by protected immutable tags and private vulnerability reporting.

## Failure and uncertainty

Cancellation does not prove a mutating operation had no effect. File replacement
reports commit separately from durability. Lost process/plugin acknowledgement
reports uncertain termination/outcome. Automatic replay is forbidden unless the
operation contract proves it did not begin or is independently idempotent. A
typed uncertain execution error can only describe a mutating tool and always
declares retry forbidden. A definitive failure may declare retry eligible only
when the tool's replay contract is not unsafe; no core dispatcher performs that
retry by itself.

## Rejected claims

- "Runs in another process" is not isolation.
- "Inside a configured worktree" is not filesystem containment for child code.
- A capability declaration is not permission enforcement.
- Redaction after logging is insufficient; sensitive values must not enter the
  event/log value in the first place.

## Acceptance

Security tests scan generated/release artifacts for canary secrets, prove all
tool routes hit a terminal guard, verify endpoint permissions and digest
changes, exercise process/plugin uncertain outcomes, and audit standalone module
graphs. The Phase 7 permission prototype now uses `ToolDispatchGuard` and the
run-owned `RequestInteraction` capability to intercept canonical executable
routes without another kernel change. Its durable policy facts hash correlation
identities and exclude call arguments, schemas, descriptions, paths,
environment, secrets, interaction responses, and recovered panic values. This
is policy interception, not a process sandbox or a claim that arbitrary trusted
Go code is contained.

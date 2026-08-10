# Spice Agent Implementation Ledger

This directory is the canonical, append-only source of phase status and evidence.
Repository roadmaps link here and must not duplicate status.

| Phase | State | Exit evidence |
| --- | --- | --- |
| 0 — product and repositories | In progress | repository governance, exact toolchain, offline build, quality gate |
| 1 — Spice-native composition | Complete for preview | generated static DI, auto-configuration, cross-repository continuation |
| 2 — deterministic kernel | Complete for preview | `841edd3`; deterministic lifecycle, interaction, snapshot, race/fuzz proof |
| 3 — provider and coding tools | In progress | generated cross-repository continuation and opt-in live acceptance |
| 4 — daemon and TUI | In progress | generated daemon/TUI distribution targets and managed local composition proven; installed Windows/Linux terminal interaction pending |
| 5 — runtime plugins | In progress | frozen plugin/v1, Go/Python conformance, authenticated host, atomic generations, exact leases, graceful lifecycle, bounded recovery/health, generated distribution activation, real-process cancellation, and real `spice dev` last-known-good proof complete; simultaneous installed daemon/TUI fault-reconnect proof pending |
| 6 — architecture proof | In progress | dependency-ordered keyless module releases, then independently attested preview distribution; [Agent preview.1/preview.2/preview.3 failure history](evidence/phase6-release-history.md) |
| 7 — stress prototypes | Complete | permission, SQLite recovery, alternate semantic shell, and two-worker extension proven without stabilizing their APIs |
| 8 — stabilization | In progress | source-built engine compatibility matrix plus removable deterministic compaction, guarded Git, and safe telemetry projection extensions proven; external authors and frozen compatibility policy remain |

Exact commits and command output are recorded only after the corresponding gate
has run. A phase is not complete because code exists; every exit criterion in its
document must be green on Windows and Linux where required.

## Current Phase 8 boundary

The first compatibility slice makes engine protocol 1.0–1.3 semantics
machine-readable and proves source-built previous (1.2) and current (1.3)
profiles over authenticated real local processes on Linux and Windows. The
acceptance-only 1.2 server cap is private; production continues to advertise
the complete range. This is not a released-binary N/N-1 claim, and plugin
generation compatibility remains a separate next slice. Exact scope and tests
are recorded in
[`phase8-engine-protocol-compatibility.md`](evidence/phase8-engine-protocol-compatibility.md).

The first optional-extension proof is the isolated `experiments/compaction`
module. It uses only the released preview5 `model.Provider` request boundary,
compacts a transient defensive copy through deterministic complete-round local
extraction, and proves the engine's durable history/events/snapshots remain
unchanged. Exact generated-DI and verification evidence is in
[`phase8-compaction-experiment.md`](evidence/phase8-compaction-experiment.md).

The removable `experiments/git-workflow` module proves a deliberately narrow
Git extension through ordinary tools, a terminal guard, run-owned interaction,
and generated Spice map/collection injection. It exposes only inspection and
staged-index commit, proves deterministic authority and process containment,
and records the preview5 pathname-race limitation rather than duplicating the
unreleased verified-child seam. Exact scope is in
[`phase8-git-workflow-experiment.md`](evidence/phase8-git-workflow-experiment.md).

The isolated `experiments/telemetry` module proves a single-mailbox,
single-consumer, best-effort secret-safe projection with exact drop accounting,
HMAC pseudonyms, typed tool occurrence decoding, deterministic local JSONL,
and generated engine-before-mailbox cleanup. It deliberately does not claim
OpenTelemetry or distributed trace continuity. Exact scope is in
[`phase8-telemetry-experiment.md`](evidence/phase8-telemetry-experiment.md).

## Current Phase 7 boundary

RFC 0009 defines and implements the generic permission seam without installing
a permission policy. Generated Spice composition supplies ordered
`ToolDispatchGuard` collections. The pipeline installs them exactly once inside
trusted decorators and immediately above the merged compiled/runtime base.
Each dispatch carries immutable run, turn, exact `PlanID`, combined plan,
workspace, and interaction authority facts. Workspace identity is now part of
`PlanIdentity` and the deliberately incompatible v1alpha3 snapshot contract,
so cross-workspace resume fails before leasing dynamic resources. The optional
external permission policy is now the isolated, deletable
`experiments/permission` module pinned to the released preview5 core with no
`replace`. It proves generated Policy-to-guard collection injection,
fail-closed prompt/default behavior, secret-free durable facts, retry and
cancellation behavior, concurrency, and compiled plus activated runtime-host
generation routes without another core API. Exact commands and boundaries are
recorded in
[`phase7-permission-experiment.md`](evidence/phase7-permission-experiment.md).
Core provides its required run-owned interaction lifecycle seam: guards can request
UI-neutral input through an unforgeable requester bound to `Run.Interact`, while
the engine retains exactly-once interaction events, snapshot safety,
cancellation joining, and tool/run terminal ordering.

The isolated `experiments/sqlite-recovery` module now proves the v1alpha3
snapshot plus v1alpha1 typed start/terminal occurrences against a real embedded
STRICT SQLite WAL store. Required-observer acknowledgment, ambiguous commit
proof, checkpoint reservation ordering, crash markers, fail-closed recovery,
immutable branch lineage, generated Spice construction, process-kill tests,
coverage, and offline vendor are owned entirely by the deletable experiment.
It does not claim transparent daemon restart. Exact evidence is in
[`phase7-sqlite-recovery-experiment.md`](evidence/phase7-sqlite-recovery-experiment.md).

The alternate-client proof is complete in the TUI repository at
`0bacac3d5a2541abfde41fd9686b763f622f84c0`: its removable semantic shell
consumes only released UI-neutral Session values and emits deterministic JSONL
without Bubble Tea, transport ownership, or executable plugin UI. The isolated
`experiments/two-worker` module completes the distributed-extension proof using
one ordinary `worker.delegate` tool, an injected public `client.Session`, and a
second ordinary `daemon.RunHost` over authenticated current-user local IPC.
There is no kernel hierarchy, scheduler, compiled registry, or protocol change.
Exact core evidence is in
[`phase7-two-worker-experiment.md`](evidence/phase7-two-worker-experiment.md).

The kernel now durably records each `ToolStarted` occurrence with typed,
bounded, secret-free definition and exact plan/workspace facts. Corrupt,
unknown, duplicate, missing, or oversized occurrence payloads fail closed, and
an unknown model tool cannot reach a guard or executable. The current daemon
wire remains intentionally compatible by projecting only `call_id` and `name`.
Each `ToolCompleted` or `ToolFailed` now closes that start with a strict typed,
bounded, secret-free terminal occurrence. Its event kind and correlation are
validated, optional execution state/retry facts remain typed, and exactly-one
ledger tests prove no started call is left ambiguously closed. The daemon keeps
the legacy terminal shape using a fixed safe failure problem.

Core runtime comparison is now repository-owned through `make benchmark`:
engine construction, a text run, one compiled tool round, and cooperative
cancellation run offline for five bounded samples with allocation reporting.
The initial Windows numbers and material-regression policy are recorded in
[`phase7-kernel-runtime-baseline.md`](evidence/phase7-kernel-runtime-baseline.md);
installed daemon/client/plugin/TUI budgets remain separate Phase 6 evidence.

## Current Phase 5 boundary

The initial tool-only protocol and independent Go/Python conformance processes
are complete. The production host now has its public private-stdin bootstrap,
exact stdout readiness contract, immutable pinned executable configuration,
held platform file-identity/digest lease, fail-closed public
`process.VerifiedLauncher` contract with no pathname fallback, immediate
defense-in-depth recheck, bounded
non-reflecting stdout/stderr monitors, current-user local endpoint allocation,
authenticated candidate launch, and fail-closed remote tool translation. A
complete immutable desired plugin `Set` feeds an atomic host that publishes one
complete merged generation, leases exact current or retained generations,
fails closed after an active crash, and performs bounded reverse graceful
lifecycle and containment without blocking lease release. The optional leaf
auto-configuration exposes that same Host through ordinary named fallback Spice
beans with generated cleanup. A validated immutable `RestartPolicy` now drives
one host-owned whole-set recovery controller: current failure closes new lease
admission immediately, attempt one is immediate, later attempts use bounded
backoff/deadlines, and only a complete still-current desired set may publish a
distinct generation. Explicit activation always takes precedence. Public
`Health` is a passive immutable snapshot containing only fixed states/issues,
plan identity, and bounded ownership counters. The zero policy remains disabled
in core and leaf auto-configuration; the reference distribution must explicitly
contribute the default policy. Distribution commit `997ab02` injects the exact
Host as the daemon's generated `ToolPlanSource`, and `e58ab17` moves both public
commands to package-main Spice targets with independent `spice dev`
supervisors. Distribution commit `6df45fa` adds explicit typed single-plugin
configuration, required/optional activation policy, generated pre-publication
lifecycle, fixed-code health, and bounded generated cleanup. Commit `73f1c4f`
proves the real digest-pinned process, cancellation with subsequent generation
reuse, generated daemon composition, authenticated local IPC, in-flight
reconnect, production-limit replay, compiled/runtime tool continuation, secret
redaction, and one real `spice dev` last-known-good/debounce workflow. The
simultaneous installed daemon plus Bubble Tea fault/reconnect workflow remains
pending. Detailed executable-integrity scope and evidence are recorded in
[`evidence/phase5-host-security-foundation.md`](evidence/phase5-host-security-foundation.md)
and
[`evidence/phase5-candidate-and-remote-tools.md`](evidence/phase5-candidate-and-remote-tools.md),
with generation/lifecycle evidence in
[`evidence/phase5-generations-and-lifecycle.md`](evidence/phase5-generations-and-lifecycle.md),
recovery/health evidence in
[`evidence/phase5-recovery-and-health.md`](evidence/phase5-recovery-and-health.md),
and auto-configuration evidence in
[`evidence/phase5-runtime-autoconfiguration.md`](evidence/phase5-runtime-autoconfiguration.md).
Distribution activation and developer-loop evidence is in
[`evidence/phase5-distribution-activation-and-devloop.md`](evidence/phase5-distribution-activation-and-devloop.md).

## Phase 2 preview boundary

The immutable model/tool contracts, typed provider failures, dispatcher
capability snapshot, call/progress correlation, bounded independent event
replay, terminal durability, and engine shutdown lifecycle are implemented.
They remain pre-1.0 contracts and will be exercised by the independent OpenAI
provider and coding-tool repositories before stabilization.

Interaction completion and snapshot import/export are bounded preview contracts
with deterministic round-trip, resume, cancellation, panic, observer-failure,
race, replay-gap, identifier-reuse, and tool-plan-fingerprint tests. Commit
`841edd3` passed `make verify` in 29.9s at 86.2% repository coverage. Durable
SQLite recovery remains the isolated Phase 7 stress proof.

## Phase 1 preview completion

Canonical `@Stage`, `@Tool`, and `@ModelProvider` descriptors and their
authorized v1alpha2 process are implemented using only generic Spice provider
and bean-metadata contributions. The in-module `CompositionProof` application
now commits and executes the real generated graph, selection matrix, ordered
collections, canonical tool map, cleanup/rollback, typed override, ownership,
diagnostic, module, and source-mapping proof. Provider, coding-tool, and TUI
repositories now contribute through explicit auto-configuration, and
distribution commit `4cfd19a` proves the generated cross-repository compiled
continuation without a registry or `RuntimeGraph`. Exact reproducible commands
and asserted core output are recorded in
[`evidence/phase1-composition.md`](evidence/phase1-composition.md). This closes
Phase 1 for the preview; pre-1.0 contract changes remain governed by the later
stress and external-author phases.

## Current Phase 3 boundary

The provider and coding-tool repositories, including the opt-in live test path,
are implemented and independently repinned. The generated cross-repository architecture proof is green at
`spice-agent-coding` commit `16244f5`: Windows `make fast` was 15.2 seconds,
`make check` was 18.2 seconds, and `make verify` was 65 seconds at 86.0%
coverage. A clean WSL2 Linux/amd64 clone at that commit ran Go 1.26.5,
`make tools-bootstrap` in 1.35 seconds, and full `make verify` in approximately
75 seconds with zero lint findings, no reachable vulnerabilities, race tests,
and vendor-offline acceptance; the worktree remained clean. Phase 3 is not
complete because opt-in live OpenAI acceptance has not been run with supplied
credentials. Distribution commit `4cfd19a` (including follow-up `1dbef3d` and
exact provider `4beed383` / coding-tools `17cbef3b`) proves real provider HTTP-request
cancellation, exact model/turn/run terminal events, and secret scans across
events, generated source, and manifests; `make verify` passed in 73.8 seconds
at 86.4% coverage with zero lint findings, no reachable vulnerabilities, race,
and vendor-offline checks. Offline scripted acceptance remains the mandatory
default.

## Current Phase 4 boundary

`common/v1` and `engine/v1` now define the provisional repository-owned Protobuf boundary
and handwritten fail-closed validation for negotiation, health, run creation,
ordered bounded replay, cancellation, stale interactions, and safe snapshot
transfer. The repaired boundary advertises server-owned definitions, encodes
and validates stable-owner reconnect CAS, keeps auth in transport metadata, separates pending
interaction snapshots/deltas from run events, pages replay atomically, and
preserves snapshot run identity through suspend/resume/import. Buf lint,
FILE-level breaking comparison against
`schema-baseline`, exact local Go-tool generation, and byte-identical freshness
are normal offline gates. Unknown fields, old/new peers, capabilities, overload,
replay gaps, stale clients, snapshot version skew, generated service shape, and
protocol fuzzing are covered. No daemon, listener, transport authentication,
managed startup, or TUI implementation is claimed by this slice. Reproducible
commands are recorded in
[`evidence/phase4-protocol.md`](evidence/phase4-protocol.md).
The wire boundary advertises protocol 1.0-1.3 and makes snapshot transfer a
minor-1 `snapshot-authority-v1` capability. Envelopes require a canonical
HMAC-SHA256 authority claim; construction requires a trusted signer and import
requires keyed verification. This is a wire and cryptographic seam, not an OS
key store or daemon-host implementation.
The first host-foundation slice now implements the transport-independent
`daemon` named interface: exact immutable definition catalogs, root-owned
stable-client reconnect CAS, bounded per-client idempotency with panic-safe
uncertain outcomes, and a stable-client-partitioned `PendingHub`. Explicit run
bindings preserve accepted prompts through run-terminal races; per-client
complete-first revisions, reconnect fencing, explicit global/per-client
budgets, and joined observer shutdown keep discovery isolated and bounded.
Its deterministic concurrency, shutdown, overflow, aliasing, capacity, and
secret-containment evidence is recorded in
[`evidence/phase4-host-foundation.md`](evidence/phase4-host-foundation.md).
No RPC server or local transport is claimed by this foundation.
Session ownership now adds a bounded, reconnect-prioritized commit/stream gate.
It fences mutation commits, joins old stream senders before a successful epoch
advance, bounds every waiting claimant and active stream set, and drains those
resources during shutdown without holding the store lock across waits or
cancellation callbacks. Evidence is in
[`evidence/phase4-session-gates.md`](evidence/phase4-session-gates.md).
The local run-authority slice now supplies the OS-backed ownership boundary:
private persistent scope/key material unrelated to endpoint tokens, signed
durable run records, stable never-unlinked run locks, keyed suspended-snapshot
matching, non-retryable consumed imports, and terminal tombstones. Import host
ordering remains explicit: prepare authority and kernel resources, persist
`IMPORTING`, commit the prepared kernel run, then persist `ACTIVE`. Evidence is
strengthened by retained directory identity, handle-relative filesystem
operations, full-ancestry rollback defense, concurrent first-open
serialization, exclusive suspended ownership and local-resume invalidation,
explicit close/drain lifecycle, and process-crash tests at suspension and both
import durability boundaries. It is
recorded in
[`evidence/phase4-run-authority.md`](evidence/phase4-run-authority.md).
The kernel's inert local-resume reservation now closes the ordering gap between
that authority and suspended execution: reserve the exact kernel boundary,
persist authority `ACTIVE` at the next local generation, then commit the
reservation. A live abort restores the byte-identical snapshot; cancellation
and shutdown remain latched until the decision. Evidence is in
[`evidence/phase4-kernel-local-resume.md`](evidence/phase4-kernel-local-resume.md).
The public transport-neutral `client` package now defines immutable negotiated
connections and sessions, explicit event/interaction control frames, bounded
secret-safe structured values, snapshot/health contracts, and lossless typed
recovery using only the Go standard library. It deliberately does not claim a
gRPC adapter, authentication, discovery, or OS endpoint. Evidence is in
[`evidence/phase4-client-contract.md`](evidence/phase4-client-contract.md).
Transport-independent protocol prerequisites now split initialization into pure
preflight and ownership completion, validate every unary success/error shape
against negotiated byte and collection limits, and define the exact maximum
opaque snapshot-envelope size. Evidence is in
[`evidence/phase4-protocol-prerequisites.md`](evidence/phase4-protocol-prerequisites.md).
Prepared starts and imported resumes can now be registered behind an inert
kernel activation gate. Only a successful durable daemon authority transition
may release execution; abort remains event- and extension-free. Evidence is in
[`evidence/phase4-kernel-activation-gate.md`](evidence/phase4-kernel-activation-gate.md).
The daemon now also exposes one typed kernel-snapshot issuer. It derives all
wire identity and lifecycle data from a validated `agent.Snapshot`, provides
byte-identical suspended retries, persists terminal tombstones, and keeps
cancellation, uncertainty, and post-commit cleanup failures distinct without
exposing signer internals. Evidence is in
[`evidence/phase4-run-authority.md`](evidence/phase4-run-authority.md).
The transport-independent `daemon.RunHost` now composes definitions, kernel,
authority, sessions, idempotency, and pending interactions behind typed Start,
Import, Suspend, Resume, Cancel, Respond, Export, Health, and Shutdown methods.
It registers starts and imports inertly, commits durable authority while holding
the stable client's mutation gate, and only then releases kernel execution.
Pre-boundary failures abandon their operation-ledger entry; committed and
uncertain results remain deterministic. Per-run transition waits honor caller
and session cancellation, and setup observes request, session, and host
lifetimes before a commit boundary. Active capacity and terminal envelopes are
bounded, terminal eviction follows completion order, owner lookup is
non-disclosing, and fixed safe degradation reasons are reported by Health.
Typed stale-owner and host/session-gate capacity facts survive durable outcome
replay and pre-boundary abandonment without exposing dependency error text;
malformed or legacy detail fields degrade to stable public sentinels.
Owned event replay/tailing and client-scoped interaction snapshot/tail views now
form the transport-neutral read boundary. Snapshot-only interaction reads do
not allocate observers. Opaque observations enforce configured stream capacity,
merge request, session, and host lifetimes, and retain their reconnect fence
until the eventual transport joins every sender and calls `Close`.
The kernel's exact run-identity ledger is now bounded by count and charged
bytes. Preparation reserves identity, terminal completion creates a tombstone
before `Wait` returns, and only an opaque exact-generation capability can retire
it. RunHost exercises that capability solely after durable terminal authority
and cleanup succeed; uncertain paths retain identity and degrade. Focused
evidence is in
[`evidence/phase4-run-identity-ledger.md`](evidence/phase4-run-identity-ledger.md).
`RunHost.Describe` now supplies the initialization boundary with one immutable,
validated, sessionless snapshot of the generated definition catalog and daemon
readiness. Session-bound Health reuses that exact snapshot implementation after
ownership validation. A generic constructor-injected `HealthSource` seam now
adds bounded passive readiness without holding the host lock or accepting
arbitrary dependency text; immutable contributions use a closed fixed-code
vocabulary with deterministic clone/sort/dedup behavior and stopping
precedence. Evidence is in
[`evidence/phase4-run-host-description.md`](evidence/phase4-run-host-description.md).
The first independent gRPC security prerequisite is now implemented in
`daemon/grpcserver`: canonical random 256-bit endpoint credentials, exhaustive
format redaction, exact single-value Bearer metadata authentication, and
matching unary/stream fail-closed middleware proven over `bufconn`. The
middleware is not exported independently, preventing consumers from treating a
partially authenticated server as supported. `grpcserver.NewServer` now owns
mandatory middleware installation, global gRPC message bounds, a bounded
private negotiated-session registry, and authenticated `Initialize` and
`Health` RPC translation. Fresh allocation occurs only after pure negotiation;
reconnect uses the daemon's epoch CAS, old owners receive typed stale facts, and
unknown identities reveal no invented epoch. The same authenticated service now
implements all seven lifecycle unary RPCs through typed client values and
`RunHost`, with exact negotiated-limit revalidation, fixed safe error statuses,
single-text Start input, snapshot capability gates, and structural-only import
translation. Authenticated event replay/tailing and complete-first interaction
streaming now preserve exact typed recovery facts and reconnect fencing through
sender exit. No OS endpoint, metadata-file permission handling, or public
client adapter is claimed.
Evidence is in
[`evidence/phase4-grpc-authentication.md`](evidence/phase4-grpc-authentication.md)
and
[`evidence/phase4-initialize-health.md`](evidence/phase4-initialize-health.md).
Lifecycle unary evidence is in
[`evidence/phase4-lifecycle-unary.md`](evidence/phase4-lifecycle-unary.md).
Authenticated streaming evidence is in
[`evidence/phase4-streaming-rpc.md`](evidence/phase4-streaming-rpc.md).
The authenticated local-client slice now adds strict current-user endpoint
metadata, bounded daemon publication and discovery, Unix-socket/Windows-pipe
transport, the complete public gRPC adapter, attach-or-start policy, and their
local bridge. Startup is authorized only by an exact proven-absence result;
malformed or untrusted state fails hard. Legacy protocol initialization loss is
explicitly non-retryable because versions 1.0 through 1.2 have no attempt
identity. Evidence and the remaining process-launch/TUI exclusions are in
[`evidence/phase4-local-client.md`](evidence/phase4-local-client.md).
Implementation commit `ec3ef1c` passed the complete local gate in 155.8 seconds
at 85.2% whole-repository handwritten-product coverage with zero reachable
vulnerabilities and full race, fuzz, and offline-vendor proof.
The current-user lifecycle slice adds the normative current-user scope, protected
explicit-attach policy, and ownership-safe managed-candidate lifecycle. The
scope selects one inseparable private directory/local transport/address tuple;
explicit attach requires an exact address match in active protected metadata
and can never authorize startup; managed shutdown stops only the exact candidate
this connector launched. Failed, canceled, or early-exiting launches are
boundedly shut down and joined, while an incomplete join retains ownership.
Retryable joins may be re-proved; explicitly non-retryable joins are retained
for manual recovery without repeating cleanup. Windows focused, repeated,
race, and vet evidence plus the
reported WSL endpoint race evidence are recorded in
[`evidence/phase4-managed-local-lifecycle.md`](evidence/phase4-managed-local-lifecycle.md).
The public provider-neutral process-launch contract now supplies immutable,
bounded, capability-declared launch intent plus typed root outcomes and a
strictly separate containment/resource join. It is an injection/decorator seam,
not an OS implementation or containment claim. Evidence is in
[`evidence/phase4-process-launch-contract.md`](evidence/phase4-process-launch-contract.md).
Protocol minor 3 now closes the local initialization acknowledgement gap. One
caller-owned 128-bit identity covers both fresh allocation and reconnect CAS;
the server coalesces exact duplicates, publishes replay state atomically with
ownership, retains creation/latest-reconnect results within fixed bounds, and
rejects conflicting reuse. The client performs one exact retry only for a
transient unavailable transport and preserves legacy 1.0-1.2 uncertainty.
Evidence and remaining process-launch/TUI exclusions are in
[`evidence/phase4-initialization-replay.md`](evidence/phase4-initialization-replay.md).
The snapshot-import contract now also exposes a complete unkeyed structural
validator for the transport boundary. It accounts for compatible unknown
Protobuf fields in the negotiated size and deliberately accepts an untrusted
but correctly shaped HMAC. Unsigned root-envelope fields and complete opaque
envelopes beyond the client transfer bound fail before translation; only `RunHost.Import` performs keyed authority
verification and admits state. This and the durable recovery facts are recorded
in
[`evidence/phase4-protocol-prerequisites.md`](evidence/phase4-protocol-prerequisites.md).
The independent TUI repository now exposes an immutable UI-neutral session
port and a public terminal shell while keeping Bubble Tea and presentation
messages internal. Commit `82adb45` generates its renderer, theme, ordered key
bindings, terminal I/O, accessibility settings, and shell through Spice. Its
external acceptance constructs the generated application, starts it, runs the
actual injected shell, and stops it on normal exit. Full verification passed in
158.4 seconds at 90.1% handwritten-product coverage, including generation
freshness, shuffled/race tests, and vendor-offline execution. This proves the
compiled presentation boundary; it does not yet claim distribution-managed
process startup, reconnect, or a real Windows/Linux terminal workflow.
The reference distribution now commits a second generated Spice application,
`spice-agentd`, at `spice-agent-coding` commit `4c13deb`. The target constructs
the authenticated local gRPC service, endpoint publication, run host, provider,
compiled tools, lifecycle hooks, and current-user root registry through the
generated bean graph. Its managed launcher attaches Windows children to a Job
Object before resume and uses a gated process-root protocol plus explicit
descendant registration on Unix; root outcome and containment/resource joining
remain separate ownership facts. `make verify` passed in 99.9 seconds at 89.5%
handwritten-product coverage with generated freshness, shuffled and race tests,
zero lint findings, no reachable vulnerabilities, and vendor-offline proof.
Focused Windows process tests and WSL2 Linux process/race tests passed, and the
platform sources compile for Darwin amd64/arm64. The Unix implementation
deliberately does not claim universal containment of arbitrary daemonizing
binaries.
Transactional `PrepareStart` and `PrepareResumeSnapshot` handles now provide the
kernel seam required for authority-before-publish hosting. They acquire and
validate the exact execution resources without engine registration, events, or
execution; one commit supplies the run-root context, while abort releases once.
See
[`evidence/phase4-kernel-preparation.md`](evidence/phase4-kernel-preparation.md).
The schema is not the final Phase 4 freeze. The host, authenticated local IPC,
managed-candidate libraries, and generated explicit-serve daemon now prove
those repaired contracts. The distribution must still route every compiled
coding-tool process through its injected resolver/launcher, generate the
managed-client/TUI application, exercise one-command and explicit attach
through installed Windows/Linux processes and terminals.

## Phase 5.0A execution prerequisite

The shared tool contract now requires effect and replay-safety metadata,
canonicalizes capabilities as an unordered set, and fails closed on read-only
definitions that request mutation-capable operations. `Tool.Execute` separates
model-visible problem results from bounded correlated infrastructure errors.
The dispatcher rejects ambiguous, untyped, mismatched, and replay-unsafe error
combinations while preserving typed errors and normal cancellation checks.
This is a prerequisite shared by compiled and runtime tools; it does not add a
plugin protocol, dynamic plan source, generation lease, or retry engine. Exact
acceptance commands are listed in
[`evidence/phase5-tool-execution.md`](evidence/phase5-tool-execution.md).

## Phase 5.0B immutable tool-plan prerequisite

The kernel now leases one immutable dispatcher generation per run through the
generic `stage.ToolPlanSource` boundary. Acquisition precedes mutation, snapshot
resume leases the exact recorded `ToolPlanID`, `PlanIdentity` combines compiled
bean identities with tool definitions, and release occurs exactly once before
terminal selection. Static embedded applications retain their existing
constructors through `StaticToolPlanSource`. Ordered decorators fail closed and
cannot change or bypass the snapshotted definition set. This is a kernel seam,
not a plugin protocol or host implementation. Exact acceptance commands are in
[`evidence/phase5-tool-plan-leases.md`](evidence/phase5-tool-plan-leases.md).

## Phase 5.1 runtime-tool protocol

The initial additive `plugin/v1` contract is frozen behind Buf FILE-level
breaking checks and deterministic local generation. It authenticates the full
initialization transcript without transmitting the per-launch secret, freezes
one sorted immutable tool catalog using the kernel's existing effect, replay,
and capability types, and validates contiguous Execute progress followed by
exactly one correlated terminal result or typed failure. Session-bound
Drain/Shutdown complete the wire lifecycle. Unknown fields round-trip and count
toward limits; duplicates, unknown enums/capabilities, mismatches, oversized
payloads, missing terminals, and post-terminal traffic fail closed. This slice
does not claim executable/digest ownership, process launch, activation, leases,
fixtures, or capability enforcement. Exact acceptance is in
[`evidence/phase5-runtime-plugin-protocol.md`](evidence/phase5-runtime-plugin-protocol.md).

## Phase 5.2 cross-language conformance

`plugin/conformance` now provides a reusable black-box suite over only the
generated public client. An independent fixture executable accepts a bounded
address/secret bootstrap through stdin, emits exactly one readiness record on
stdout, and serves over current-user Unix-socket or Windows-pipe gRPC. The suite
proves authenticated initialization (including unknown fields), canonical
manifest metadata, contiguous success and typed-failure streams, malformed and
oversized rejection, real RPC cancellation, drain admission fencing, and clean
shutdown. Normally completed streams require exactly one terminal frame;
client transport cancellation is the explicit preempting case. An independent
Python 3.12+ implementation uses pinned `grpcio` and Protobuf dependencies and
passes the same public Go harness. On Windows it uses a private absolute AF_UNIX
socket because Python gRPC does not serve named pipes; the production Go daemon
transport remains unchanged. This remains conformance infrastructure, not the
digest-owning process host or activation manager. Evidence is appended to
[`evidence/phase5-runtime-plugin-protocol.md`](evidence/phase5-runtime-plugin-protocol.md).

## Current infrastructure blocker

A Windows clean-clone audit of all five repositories passed Go 1.26.5,
`make fast`, offline vendor tests with `GOWORK=off GOPROXY=off
GOFLAGS=-mod=vendor`, and `govulncheck`. This is not Linux or macOS evidence and
does not close Phase 0. A separate WSL2 Linux 6.18.33.1 audit with Go 1.26.5
linux/amd64 used fresh public clones: all five repositories passed `make fast`
and explicit `GOWORK=off GOPROXY=off GOFLAGS=-mod=vendor go test ./...`.
Fresh-clone full verification still needs a source-preserving dependency
bootstrap. After cache preparation, `spice-agent` passed in 32.2s and
`spice-agent-coding` passed in 6.7s; the other repositories exposed bootstrap
or `go.sum` preservation gaps being corrected separately. This is not macOS
evidence and does not close Phase 0. GitHub Actions jobs remain queued without
starting; diagnosing organization billing/policy requires unavailable
`admin:org` authority. Core/tools Dependabot gRPC alerts remain open and must
not be dismissed, although their tools modules already pin v1.82.1 and local
`govulncheck` is clean.

Development catalog commit `a8990e3f` adds topological concurrent
five-repository fast verification, an exact compatibility snapshot, and
vendor-only macOS amd64/arm64 compile proof; its `make verify` passed at 85.6%
coverage. Follow-up `379a375` adds strict generic Go-module and distribution
release profiles without changing the existing starter release contract; its
`make verify` passed in 107.9 seconds at 85.2% coverage. Organization-profile
commit `3ee0039d` records reusable workflow and Actions-queue governance. macOS
remains compile-only: real race, UI, process, and runtime acceptance still
requires a macOS runner.

## Completed evidence

- Organization governance/profile: `.github` `11e9470`.
- Development catalog/workspace: `36a3bf5`; `make verify` 98s, 85.5% coverage.
- Phase 0 catalog/governance follow-up: development `a8990e3f` (85.6% coverage,
  concurrent five-repository fast and macOS vendor compile), organization
  profile `3ee0039d`.
- Strict agent module/distribution release profiles: development `379a375`;
  `make verify` 107.9s, 85.2% coverage.
- Core foundation: `spice-agent` `218ffbb`.
- Professional quality baseline: `829dd0a`; `make verify` 27.6s, 87.7% coverage.
- Hardened deterministic kernel: `eaf1918`; `make verify` 23.9s, 88.4% coverage.
- Completed preview kernel: `841edd3`; `make verify` 29.9s, 86.2% coverage.
- Spice-native composition proof: `1f07284`; `make verify` 137.3s, 86.2% coverage.
- OpenAI provider final repin `88c3044`; `make verify` 29.25s, 89.3% coverage.
- Coding-tools final repin `653b405`; `make verify` 33.19s, 87.7% coverage.
- TUI foundation through `spice-agent-tui` `28a89dc`.
- Reference distribution through `spice-agent-coding` `2809a1a`.
- Generated Phase 3 architecture proof: `spice-agent-coding` `16244f5`;
  Windows and clean-clone WSL2 Linux full verification green at 86.0% coverage.
- Current Phase 3 distribution proof: `spice-agent-coding` `4cfd19a` (provider
  `4beed383`, coding tools `17cbef3b`);
  `make verify` 73.8s, 86.4% coverage.
- Process lookup/launch ownership contracts: `spice-agent` `5d2fd63`;
  `make verify` 152.5s, 85.2% coverage.
- Generated explicit-serve daemon and managed process boundary:
  `spice-agent-coding` `4c13deb`; `make verify` 99.9s, 89.5% coverage.
- Generated runtime-plugin Host cutover: `spice-agent-coding` `997ab02`;
  `make verify` 128.4s, 87.0% coverage.
- Package-main daemon/terminal development supervisors: `spice-agent-coding`
  `e58ab17`; `make verify` 129s, 87.0% coverage.
- Explicit generated runtime-plugin activation: `spice-agent-coding`
  `6df45fa`; `make verify` 128.2s, 87.1% coverage.
- Generated architecture, runtime cancellation, and real `spice dev`
  last-known-good acceptance: `spice-agent-coding` `73f1c4f`; `make fast`
  20.4s, `make check` 69.4s, and `make verify` 183.1s at 87.1% coverage.
- Public-facade Bubble Tea interaction proof: `spice-agent-tui` `a9c2bc3`;
  `make verify` 90.1s, 90.2% coverage.

Active follow-up slices are not recorded as completed evidence until their
repositories publish green commits.

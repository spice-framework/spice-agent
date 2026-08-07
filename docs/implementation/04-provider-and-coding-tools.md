# Phase 3: OpenAI Provider and Coding Tools

## Objective and prerequisites

Prove the kernel's public model and tool seams with independent production
modules: OpenAI Responses streaming plus bounded read, replace/write, and
process execution. This phase requires immutable kernel contracts, typed
provider errors, capability-aware dispatch, and phase 1 auto-configuration.

## Provider contracts

- Pin `github.com/openai/openai-go/v3 v3.50.0` exactly.
- Translate provider-neutral requests into Responses API input without exposing
  the raw SDK client as public API. The request owns the selected model.
- Support text deltas, finalized function calls, usage, cancellation, refusal or
  typed failure, and bounded safe metadata. A completed response is the
  authoritative source of finalized tool call IDs and arguments.
- Provider startup errors and stream receive errors carry a bounded typed
  `model.Problem`. The kernel—not the adapter—computes whether any stream item
  was observed, so a failure after partial output is never retried as pristine.
- Retry only errors known to occur before streaming begins. Idempotency keys are
  stable derivatives of host operation IDs and reveal no prompt or tool data.
- Configuration validates timeouts, base URL, organization/project IDs, and
  credentials. Secret values are redacted from formatting, errors, events,
  manifests, generated source, and test artifacts.
- The starter is a fallback `model.Provider`; an application-owned normal or
  primary bean follows normal Spice DI replacement rules.

## Coding-tool contracts

- `read` accepts a validated relative path plus paging bounds, uses an anchored
  root, reports UTF-8 or base64, and returns a hash when a complete file is read.
- `replace` creates exclusively or performs a stale-protected atomic file
  replacement using an expected SHA-256. It serializes commits within the bean,
  rechecks immediately before rename, and explicitly does **not** promise a
  cross-process atomic compare-and-swap primitive.
- A replacement reports `committed` separately from `durable`; a post-commit
  file/directory sync failure is an uncertain-durability result, not a claim
  that no mutation occurred.
- `shell` executes discrete argv without a shell, from a validated worktree-
  selected starting directory. It is not filesystem containment: the child has
  the user's full process, network, filesystem, and allowlisted-environment
  privileges.
- Process construction uses the public provider-neutral `process.Launcher`
  bean. Immutable specifications preserve exact argv/environment/streams and
  declared capabilities for later permission decorators; no coding tool may
  expose or construct `*exec.Cmd` as its public boundary.
- Shell output is count/byte bounded, binary-safe, cancellable, and time bounded.
  Unix process groups and Windows kill-on-close Job Objects are used; inability
  to confirm tree termination is surfaced.
- Every tool publishes exact capabilities and uses the canonical dispatcher.
  First-run/help text warns that no permission or sandbox extension is active.

## Implementation slices

1. Implement fake-source provider translation and exhaustive offline tests.
2. Add redacted typed configuration, starter manifest, and explicit
   `/autoconfigure` package.
3. Add an opt-in live test requiring an explicit environment switch and secret;
   ordinary verification must neither run it nor contact the network.
4. Implement anchored read, atomic replacement, and process tools with immutable
   definitions and fallback beans.
5. Add dependency, license, maintenance, retry, observability, and security
   reviews in each repository.
6. Pin immutable core pseudo/tag versions, remove local `replace`, regenerate
   vendor data, and prove isolated offline verification.
7. Add a generated application acceptance flow: streamed text, one compiled
   tool call, continuation, final text, cancellation, and secret-redaction scan.

## Exclusions

The provider module does not select retry policy for partially observed streams,
persist conversations, or own global clients. Coding tools do not implement
approval, sandbox, Git policy, remote execution, or hidden path/network
restrictions. Those are later dispatcher decorators or alternate tool beans.

## Verification

- Provider tests cover malformed/unknown events, partial streams, duplicate or
  undeclared calls, usage, metadata bounds, startup/receive cancellation,
  ambiguous retry, redaction, and stream-close failures.
- Tool tests cover traversal, links, device/special files, invalid UTF-8, paging,
  stale hashes, create races, cancellation before commit, uncertain durability,
  output truncation, process start/exit/timeout, descendants, and environment
  allowlisting on Windows and Unix.
- Shuffled race tests prove singleton bean concurrency. Fuzz smoke targets JSON
  argument decoding and provider event translation.
- `make verify` includes isolated tools-module security, exact vendor contents,
  and at least 85% handwritten product coverage.

## Performance and completion evidence

Local adapter translation targets sub-millisecond per event outside SDK/network
latency. Cooperative process cancellation targets p95 below 50 ms before the
configured grace interval; forced tree cleanup has an explicit bounded deadline.

Status is **in progress**. The hardened core is pinned for active provider and
coding-tool implementations. This phase closes only after both repositories
push green standalone commits and the generated cross-repository vertical flow
passes.

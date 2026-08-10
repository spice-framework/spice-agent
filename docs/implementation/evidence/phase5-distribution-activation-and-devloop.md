# Phase 5 Distribution Activation and Developer-Loop Evidence

## Scope

This evidence closes explicit reference-distribution activation and one real
last-known-good development-supervisor workflow. It does not claim the remaining
simultaneous installed daemon plus Bubble Tea fault/reconnect workflow.

## Generated production activation

`spice-agent-coding` commit `6df45fa` makes runtime-plugin activation an ordinary
generated Spice lifecycle:

- one explicit `agent.runtime-plugin` typed configuration names an absolute
  executable, SHA-256 digest, manifest identity, required/optional policy,
  capability bounds, and lifecycle deadlines;
- the application-owned exact Host bean replaces the fallback and owns bounded
  cancellation-independent cleanup;
- activation completes before daemon publication, and a fixed-code passive
  health source feeds the generated RunHost;
- required failure blocks publication, optional failure degrades health, and a
  cancellation between activation and publication contains the process;
- no discovery, environment fallback, package scan, runtime container, or
  reflective lookup is introduced.

The exact commit passed Windows `make verify` in 128.2 seconds with zero lint or
NilAway findings, no reachable vulnerabilities, race and offline-vendor proof,
and 87.1% handwritten-product coverage.

## Real process, architecture, and cancellation proof

`spice-agent-coding` commit `73f1c4f` adds three independent acceptance layers:

1. A digest-pinned Go runtime-plugin fixture negotiates a single call slot. A
   blocking RPC is canceled after observed admission, yields one correlated
   definitive non-retryable terminal failure, releases both client and server
   admission, permits an immediate echo call, and exits under bounded Host
   shutdown on Windows and Unix.
2. The generated daemon component graph activates that process, serves its
   generated authenticated gRPC boundary on a private local endpoint, and is
   driven through the real local client and a local-TLS OpenAI Responses
   fixture. One run executes compiled `read`, runtime `fixture.echo`, and the
   final model continuation. Ownership reconnect occurs while the final
   provider response is held. Replay is contiguous, paged, and exercised at
   the generated 4,096-event/8-MiB limits; the run has exactly one terminal.
   Endpoint/provider credentials are absent from events, provider bodies, and
   formatted connector state. Generated cleanup orders RunHost before plugin
   Host before root process containment.
3. A copied valid-Go package-main fixture runs under the real vendored `spice
   dev` command. An invalid annotation leaves the exact PID/process identity
   and HTTP health endpoint unchanged. A repaired revision replaces it, two
   observed writes debounce to only their final bytes, restoration replaces it
   again, and shutdown removes the endpoint. Handwritten and generated hashes
   return byte-identically to their starting state. Each candidate binds port
   zero and atomically publishes a random process identity, avoiding PID/port
   reuse false positives; fallback cleanup verifies identity before targeting
   the child process group.

The daemon's advertised replay limits and event-log subscriber limits now come
from the same generated immutable `client.Limits` bean. Checked platform-width
and retained-history guards reject impossible budgets before engine creation.

## Exact verification

The committed `73f1c4f` tree passed on Windows/amd64 with Go 1.26.5:

```text
make fast    20.4s
make check   69.4s
make verify  183.1s
```

The full verifier reported zero lint/NilAway findings, no reachable
vulnerabilities, shuffled and race-enabled process acceptance, 87.1%
handwritten-product coverage, deterministic generated freshness, and complete
offline-vendor tests/builds. The process-heavy real-CLI acceptance remains in
`make check` and `make verify`; `make fast` excludes only that package to stay
below its 30-second feedback budget.

## Remaining boundary

This component-level architecture proof intentionally drives the generated
daemon components on a private endpoint. It does not call the installed
`spice-agentd` command adapter or run the real Bubble Tea process. Phase 5 still
requires both production `spice dev` supervisors to run simultaneously, an
invalid daemon-only edit to preserve the attached TUI, a valid daemon-only
replacement, and observed Bubble Tea reconnection without restarting the TUI.

Subsequent distribution commit `7f36894a16768210242967431477eda3cc02c566`
does call both installed command adapters and drives the real Bubble Tea event
loop through its accessible renderer. The cross-platform stabilized preview 2
tree proves transport-fault replay and daemon replacement while the same
terminal process remains alive. That closes the installed-process gap in this
paragraph, but not the specifically separate dual-`spice dev` supervisor edit
workflow above or native PTY/ConPTY presentation. The exact boundary is recorded
in [`phase4-installed-distribution.md`](phase4-installed-distribution.md).

# Phase 4 current-user attach and managed-candidate evidence

## Implemented boundary

This slice completes the library-level ownership boundary between a protected
current-user endpoint, explicit attachment, and a daemon candidate launched by
one managed connector. It does not yet provide a distribution process launcher
or command wiring.

`daemon/endpoint.CurrentUserScope` returns one immutable tuple of private
runtime directory, local transport, and canonical address. Callers cannot
replace an individual field. On Linux and macOS it accepts a trusted private
`XDG_RUNTIME_DIR` or creates a current-user directory beneath a trusted sticky
temporary directory and selects a Unix socket within that directory. On
Windows it selects a private LocalAppData runtime directory and a canonical
local named pipe derived from the current user's SID. Opening and revalidating
the scope use the retained, ownership-checking user-storage boundary. No scope
can select TCP, a remote pipe, an untrusted directory, or a caller-controlled
transport/address combination.

`client/localclient.ExplicitDiscovery` accepts one caller-selected local
address but resolves it only through protected active endpoint metadata. The
requested address must exactly match the metadata address before the existing
lazy authenticated local connector is returned. Absence, stale cleanup,
malformed metadata, cancellation, and mismatch are hard attach failures. In
particular, explicit attachment never returns the exact managed-start absence
sentinel, so it cannot accidentally authorize a new process. Errors and all
formatting paths omit endpoint addresses and credential material.

## Candidate ownership and shutdown

`client/managed.Starter` returns a `Candidate` handle with `Done`, `Result`,
`BeginShutdown`, and context-bounded `Wait` operations. `Result` is only the
child process outcome. `Wait` returns nil only when every process and
containment resource owned by the candidate is safe to release. The candidate
lifetime is independent of the launch context. A non-nil candidate returned
alongside a launch error is still owned and must be cleaned up.

One managed connector serializes initialization locally and uses the supplied
cross-process startup lease for the discovery recheck and launch decision. It
starts only after discovery returns the exact, unwrapped
`managed.ErrEndpointNotFound` value. Malformed, incompatible, untrusted, or
otherwise wrapped discovery failures never authorize launch. Concurrent
initialization cannot replace the owned candidate.

Ownership is exact:

- a daemon found by discovery is external and `Shutdown` never stops it;
- only the candidate returned by this connector's `Starter` is retained;
- early child exit fails startup promptly and exposes only typed/safe causes;
- failed or canceled startup begins shutdown and joins the candidate;
- failure to release the startup lease after launch closes the returned session
  and also shuts down and joins that launched candidate; and
- connector shutdown closes admission, cancels an in-flight initialization,
  waits for it to leave the serialized boundary, then boundedly stops and joins
  only the owned candidate.

If the candidate cannot join before the shutdown budget, or its containment
join reports a failure after process exit, ownership is retained and a later
`Shutdown` may retry. Dependency errors remain available through
`errors.Is`/`errors.As`, while their arbitrary text is not copied into the
public error string. This is lifecycle ownership, not a sandbox: the future
distribution starter will still launch a daemon with the user's process
privileges.

## Verification evidence

The exact focused evidence captured while implementing this slice is:

- Windows: `go test -shuffle=on -race ./daemon/endpoint` passed;
- WSL Linux: `go test -shuffle=on -race ./daemon/endpoint` passed;
- Windows: `go vet ./daemon/endpoint` passed;
- Windows integrated focused run:
  `go test ./client/managed ./client/localclient ./daemon/endpoint` passed in
  3.4 seconds;
- Windows integrated race run:
  `go test -race ./client/managed ./client/localclient ./daemon/endpoint`
  passed in 7.1 seconds;
- Windows: `go vet ./client/managed ./client/localclient ./daemon/endpoint`
  passed; and
- Windows repeated shuffled run:
  `go test ./client/managed ./client/localclient ./daemon/endpoint -shuffle=on -count=10`
  passed in 12.9 seconds.

The managed package additionally passed its focused test, race test, vet, scoped
golangci-lint with zero findings, and `git diff --check` during development.
The final repository gate remains a delivery requirement rather than a claim
made by this focused-evidence record.

Coverage includes current-user scope selection and rejection, explicit exact
match and protected-store behavior, stale/malformed/canceled attach failures,
format redaction, exact startup authorization, early exit, failed and canceled
startup cleanup, release-failure cleanup, external-versus-owned shutdown,
concurrent initialization, bounded shutdown timeout, retry after an incomplete
or failed containment join, dependency-error redaction, and suppression of a
late session when shutdown wins the initialization-finalization race.

## Remaining exclusions

This slice does not implement `os/exec` process creation, process-tree signal or
termination policy, daemon executable discovery, distribution configuration,
`spice-agent` managed startup, `spice-agentd serve`, `spice-agent attach`, or
the daemon-to-TUI bridge. It does not prove a real process publication race,
installed one-command startup, terminal reconnect, Ctrl-C shutdown, or the
complete Windows/Linux distribution workflow. Those remain required before
Phase 4 freezes.

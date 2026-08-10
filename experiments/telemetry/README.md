# Secret-safe best-effort telemetry projection

This removable Phase 8 module proves a deliberately small telemetry boundary
using only released Agent preview5 APIs. It is an exporter-neutral projection,
not an OpenTelemetry implementation and not durable event history.

One generated `event.BestEffortObserver` is the only mailbox. One consumer
translates accepted envelopes into immutable bounded metric, complete-span, and
fixed-log values, then synchronously calls one application-owned `Exporter`.
There is no second queue, retry, global provider, environment discovery,
network client, storage, reflection, or registry.

The generic envelope payload is never exported. Rich decoding is restricted to
the versioned `ToolStartedOccurrence` and `ToolTerminalOccurrence` contracts.
Run and call correlation values are process-local HMAC-SHA-256 pseudonyms.
Tool names, arguments, output, model text, provider metadata, paths, endpoints,
errors, credentials, and workspace identities are omitted.

`localjsonl.New(writer)` provides deterministic local evidence through a
caller-owned writer. It neither opens nor closes a file. The optional daemon
health adapter is passive, uses fixed reason codes, and has no readiness impact
unless the application explicitly enables it.

## Delivery and drain

Mailbox publication is nonblocking. Overflow increments the Agent-owned exact
drop counter; telemetry can never delay or fail a run. Each accepted envelope
is processed exactly once by the consumer, while failed exporter calls have no
retry because their side effects may be partial.

The generated `TelemetryProof` constructs exporter → mailbox → processor → real
Agent engine. Reverse Spice cleanup invokes `Engine.Shutdown`, waits for run
terminal finalization, then closes and drains the mailbox, performs a final
export attempt, and shuts down the exporter. Thus every accepted envelope is
drained when the exporter honors cancellation. A trusted exporter that ignores
its context can consume the caller's shutdown deadline; no in-process Go API
can forcibly contain such code, and this experiment makes no stronger claim.

## Deliberate limits

- Drops can omit starts or terminals, so telemetry is never replay/recovery
  truth. Open correlations at shutdown are counted as incomplete and cleared.
- Preview5 exposes stable rich occurrence identities only for tools. Turn,
  model, and interaction spans are not fabricated from private payload shapes.
- Events contain no inbound distributed trace context. Derived spans are new
  local roots and cannot claim provider/RPC trace continuity.
- This module does not install OpenTelemetry providers. A future isolated OTel
  adapter requires a separate dependency/security review and explicit injected
  providers with network disabled by default.

## Verification and deletion

`make fast`, `make check`, `make verify`, and `make benchmark` are deterministic
and offline after explicit dependency bootstrap. Delete this directory and its
root ledger/evidence links to remove all code and runtime cost; the root module
and release graph do not depend on it.

# Phase 4 Authenticated Streaming RPC Evidence

## Outcome

`daemon/grpcserver` now implements both repository-owned server streams over
the authenticated, negotiated engine service:

- `StreamEvents` delivers one atomic bounded replay page, its explicit control,
  and an optional gap-free live tail.
- `StreamInteractions` delivers one complete pending-interaction snapshot, its
  captured control, and an optional revision-contiguous live tail.

Both adapters consume transport-neutral observations owned by `RunHost`. An
observation retains its stable-client stream lease until the handler's sole
sender exits and calls `Close`; reconnect therefore fences and joins the old
sender before advancing ownership.

## Boundary invariants

Authentication and hard server validation precede session lookup. After exact
registry and `SessionStore` ownership checks, each request is validated again
against its immutable negotiated protocol and limits. Interaction streaming is
available only on protocol 1.2 and later; an older negotiated peer receives a
typed version mismatch. The removed provisional interaction replay field is
rejected even when received as unknown field number 3.

The event adapter exhaustively maps all 19 kernel event kinds. It validates the
entire replay page and every response envelope before the first event is sent.
Successful controls preserve captured bounds, explicit page cursor, paging,
and tail state. Cursor gaps carry exact replay bounds. Count/byte exhaustion
carries the exact safe resource, limit, observed value, and last-delivered
cursor; arithmetic remains exact at platform bounds.

The interaction adapter validates the complete snapshot/control pair before
the first frame is sent. Live deltas pass through the stateful
`InteractionTailValidator`, so only the exact next revision may open a missing
entry or close its matching pending value. Observer exhaustion carries exact
queue facts. Reconnect recovery always begins with a new complete snapshot and
never depends on delta history.

Application failures are bounded control frames. Authentication, RPC-context
cancellation/deadline, and transport send failures remain gRPC results. A
daemon shutdown or reconnect fence that cancels only the owned observation
ends the superseded stream quietly. An error control that cannot fit the
negotiated frame bound fails explicitly rather than emitting malformed data.
Adapter-root cancellation is merged into observation acquisition. The public
bounded `Server.Shutdown(ctx)` first stops admission and idle observations,
then force-stops gRPC if the caller's deadline expires, so client flow control
cannot strand process shutdown. The legacy `GracefulStop` remains an explicitly
unbounded compatibility wrapper.

## Executable proof

Tests cover hard and negotiated validation, stale and wrong owners without run
disclosure, protocol-1.2 refusal, cursor gaps, exact overload facts, complete
wrapper bounds, page prevalidation before disclosure, all event kinds, finite
pages, live event and interaction tails, invalid/noncontiguous deltas, send
failure, cancellation, slow-observer exhaustion, and reconnect fencing through
sender exit. Shutdown tests prove idle-root cancellation and deadline-triggered
forced transport stop. Authenticated service cases use the real gRPC boundary over
`bufconn`; transport-neutral success paths use deterministic stream and
observation fixtures, while `RunHost` tests independently prove the concrete
lease/join behavior.

The exact combined tree passes repository `make verify` before commit. Local
Unix sockets, Windows named pipes, endpoint discovery, a public client adapter,
managed daemon startup, and process-level Windows/Linux acceptance remain
outside this slice.

# Phase 4: Daemon and TUI

**Objective:** expose the same engine through authenticated user-local IPC and a
separate Spice-generated Bubble Tea application. The protocol supports version
negotiation, health, start, sequenced replay, cancellation, interactions, and
snapshots. Remote listeners are excluded.

Managed startup attaches to a compatible daemon or starts one; explicit serve
and attach remain available. **Exit:** Windows named-pipe and Unix-socket tests,
bounded reconnect, resize, version rejection, and clean shutdown. **Status:**
planned.

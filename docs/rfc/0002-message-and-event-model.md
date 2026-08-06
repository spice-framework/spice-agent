# RFC 0002: Message and Event Model

**Status:** accepted for preview. Messages use validated roles and bounded typed
parts. Provider-specific data is a size-bounded namespaced JSON envelope.
Events carry one immutable run identity, strictly increasing sequence, stable
kind, timestamp supplied by the engine clock, and bounded payload. Every started
operation receives exactly one terminal event; replay never renumbers history.

The authoritative per-run log is bounded by both retained count and encoded
bytes. Subscribers own independent count-and-byte-bounded cursors. A subscription
atomically captures retained replay and joins the live tail, so there is no
replay/live gap. Behind cursors recover from `earliest-1`; ahead cursors recover
from `latest`. Slow consumers terminate with their last delivered sequence.

Local commit precedes required-observer acknowledgement. A required observer
returning nil acknowledges durable acceptance; an error may represent a partial
external side effect, but the already committed local sequence is never reused.
Best-effort observers see an event only after every required observer
acknowledges it. Lifecycle terminal events receive reserved log capacity.

# RFC 0002: Message and Event Model

**Status:** accepted for preview. Messages use validated roles and bounded typed
parts. Provider-specific data is a size-bounded namespaced JSON envelope.
Events carry one immutable run identity, strictly increasing sequence, stable
kind, timestamp supplied by the engine clock, and bounded payload. Every started
operation receives exactly one terminal event; replay never renumbers history.


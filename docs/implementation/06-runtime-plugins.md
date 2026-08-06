# Phase 5: Runtime Plugins

**Objective:** support genuinely dynamic tools without mutating compiled DI. One
process and connection serve each digest-pinned plugin. A random launch secret,
bounded gRPC protocol, candidate validation, atomic activation, and generation
leases protect existing runs.

Failed candidates never affect the active generation. Go and Python fixtures use
the same conformance suite. **Exit:** mismatch, crash, cancellation, timeout,
activation, drain, and shutdown proof. **Status:** planned.

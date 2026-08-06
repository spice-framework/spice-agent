# Phase 0: Product Boundaries

**Objective:** establish a small SDK-first agent platform, not a feature-complete
coding product. **Prerequisite:** Spice core/toolchain `v0.1.0-preview.1`.

The kernel owns one deterministic run loop, provider-neutral values, typed model
and tool seams, events, cancellation, containment, and snapshots. Provider
implementations, coding tools, transport, UI, persistence, permissions, MCP,
Git, indexing, telemetry, subagents, and distribution policy are excluded.

**Slices:** governance; contracts; deterministic laboratory; architecture tests.
**Tests:** clean clone, exact Go, offline vendor, dependency direction.
**Budget:** warm `make fast` under 30 seconds. **Status:** in progress.


# RFC 0001: Kernel Boundary

**Status:** accepted for preview. The kernel owns values, one-agent execution,
sequencing, cancellation, containment, terminal finalization, and snapshots. It
accepts constructor-injected typed dependencies and never locates services.
Provider implementations, tools, UI, transport, storage, permissions, and
multi-agent scheduling remain extensions. Exit proof is a real provider, a
compiled tool, a runtime tool, and a persistence observer without kernel growth.

Providers and tools are trusted in-process beans. Context cancellation is
cooperative and the kernel cannot stop code that ignores it. The dispatcher is
the only canonical executable route, snapshots tool capabilities before runs,
and validates call correlation around execution. The engine never exposes a
service registry or dynamically mutable compiled graph.

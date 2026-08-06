# RFC 0006: UI and Interaction Model

**Status:** accepted for preview. The kernel emits UI-independent interaction requests and
portable semantic views. A compiled Spice TUI injects one shell and ordered
renderers, commands, bindings, workspaces, status, and theme. Unknown views have
a bounded textual fallback. Runtime plugins may send namespaced data, never UI
code. Every opened interaction receives one answered, cancelled, or failed event.

`interaction.Broker` is a constructor-injected, UI-neutral port. Requests and
responses are immutable, typed, JSON-bounded values; response IDs must match the
active request. Events contain only interaction identity, kind, and lifecycle
status. Prompt text, schemas, and user-entered response JSON are returned through
the direct broker call and never enter event/replay payloads. Prompt rendering,
terminal state, shortcuts, and client routing remain outside the kernel.

An interaction can begin only while its run is active. A locally committed
`InteractionStarted` is followed exactly once by completed, failed, or cancelled,
including broker panic, caller/run cancellation, malformed responses, and
post-commit required-observer failure. Run finalization rejects new interactions,
cancels cooperative active brokers, waits for their terminals, and then commits
the run terminal. Brokers are trusted in-process beans and must honor context.

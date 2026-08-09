# RFC 0006: UI and Interaction Model

**Status:** accepted for preview. The kernel emits UI-independent interaction requests and
portable semantic views. A compiled Spice TUI injects one shell and ordered
renderers, commands, bindings, workspaces, status, and theme. Unknown views have
a bounded textual fallback. Runtime plugins may send namespaced data, never UI
code. Every opened interaction receives one answered, cancelled, or failed event.

`interaction.Broker` is a constructor-injected, UI-neutral port. Every call
receives a validated immutable scope containing the owning run ID. Requests and
responses are immutable, typed, JSON-bounded values; the response interaction
ID must match the active request. Protocol mutations use the client operation ID
for idempotency and do not invent a second response ID. Events contain only
interaction identity, kind, and lifecycle status. Prompt text, schemas, and
user-entered response JSON are returned through
the direct broker call and never enter event/replay payloads. Prompt rendering,
terminal state, shortcuts, and client routing remain outside the kernel.

Terminal tool guards do not receive the broker or construct its scope. Their
`ToolDispatchScope` holds a private run-bound `interaction.Requester` capability
whose `Request` method accepts only context and request. The engine adapts it to
`Run.Interact`, preserving the same validation, event ownership, cancellation,
snapshot-safety, and finalization join as any other in-run interaction. A copied
or reconstructed public scope cannot reproduce this pointer capability.

The process protocol carries pending prompts on a separate stream. Its first
frame is always one atomic complete snapshot of pending state, even after
reconnect; revision-contiguous opened/closed deltas follow. A client never has
to reconstruct unresolved prompts from potentially evicted event or delta
history.

An interaction can begin only while its run is active. A locally committed
`InteractionStarted` is followed exactly once by completed, failed, or cancelled,
including broker panic, caller/run cancellation, malformed responses, and
post-commit required-observer failure. Run finalization rejects new interactions,
cancels cooperative active brokers, waits for their terminals, and then commits
the run terminal. Brokers are trusted in-process beans and must honor context.

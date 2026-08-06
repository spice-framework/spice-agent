# RFC 0006: UI and Interaction Model

**Status:** draft. The kernel emits UI-independent interaction requests and
portable semantic views. A compiled Spice TUI injects one shell and ordered
renderers, commands, bindings, workspaces, status, and theme. Unknown views have
a bounded textual fallback. Runtime plugins may send namespaced data, never UI
code. Every opened interaction receives one answered, cancelled, or failed event.


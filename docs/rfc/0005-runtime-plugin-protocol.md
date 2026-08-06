# RFC 0005: Runtime Plugin Protocol

**Status:** draft. Each digest-pinned executable receives a random per-launch
secret and serves one bounded local gRPC connection. Handshake proves version,
manifest, and digest before candidate activation. Preview v1 exposes tools only.
Activation is atomic for future runs; leases retain prior generations until all
owners release them. No plugin mutates Spice DI or emits executable UI code.


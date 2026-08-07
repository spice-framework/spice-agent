# Python runtime-plugin conformance fixture

This subtree is an independent Python 3.12+ implementation of the frozen
Spice Agent `plugin/v1` runtime-tool profile. It exists to prove the protocol
is language-neutral; it is not production plugin-host code.

The process reads one bounded JSON bootstrap object from stdin:

```json
{"address":"<current-user local IPC address>","secret":"<base64url, no padding>"}
```

It writes exactly `{"ready":true}` plus a newline to stdout after the local
gRPC server is ready. Diagnostics go only to stderr. The launch secret is used
to authenticate the versioned canonical initialize transcript and is never
placed on the wire or in diagnostics.

The address is an absolute Unix-domain socket path on Linux, macOS, and
Windows. Python's `grpcio` C-core supports Windows AF_UNIX sockets but does not
serve Windows named pipes; a cross-language conformance launcher must therefore
use AF_UNIX for this fixture on Windows. The production Go daemon's named-pipe
contract is unchanged.

Reproducible setup and tests:

```text
uv sync --frozen
uv run --frozen python -m unittest discover -s tests -v
```

Bindings are committed so runtime setup needs no compiler. To regenerate them,
the canonical schemas must still match `protocol-lock.json`:

```text
uv run --frozen python generate_protocol.py
```

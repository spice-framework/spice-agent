# Dependency Review

The kernel remains standard-library-first. Dependencies below are pinned in the
owning module and vendored; normal builds and checks use `GOPROXY=off`.

| Dependency | Pin/license | Owned use and review |
| --- | --- | --- |
| `github.com/spice-framework/spice` | `v0.1.0-preview.4`, Apache-2.0 | Annotation SDK, portable starter identity, lifecycle cleanup, and the standard-library `slog.Handler`-backed structured logger. Agent logging receives the exact logger by generated injection and adds no global logger, runtime container, file, or network path. Handler cancellation and I/O remain caller-owned; failure and panic are diagnostic-only. Replacement cost is limited to descriptor, manifest, lifecycle, and logging APIs. |
| `github.com/Microsoft/go-winio` | `v0.6.2`, MIT | Windows-only named-pipe listener and context-aware dial implementation in `daemon/localipc`. Spice supplies an explicit protected current-user DACL and accepts only canonical local pipe names; go-winio sets `FILE_PIPE_REJECT_REMOTE_CLIENTS` on every server instance. The dependency is maintained by Microsoft, is broadly used in Go's Windows ecosystem, uses the already-required `golang.org/x/sys/windows`, and is isolated behind the two-function local IPC boundary. Listener cleanup, cancellation, timeout, duplicate binding, ACL construction, and remote-name rejection are tested. It opens no TCP path and performs no discovery or background network access. |
| `google.golang.org/protobuf` | `v1.36.11`, BSD-3-Clause | Generated messages plus deterministic marshal/clone support only in `common/v1`, `engine/v1`, and `plugin/v1`. Unknown fields are retained; lifecycle and execution enums are validated fail-closed. |
| `google.golang.org/grpc` | `v1.83.0`, Apache-2.0 | Generated client/server interfaces, mandatory endpoint-authentication interceptors, and the local client adapter. The public server wrapper installs unary and streaming authentication together and applies global message bounds. Production dialing is restricted to the explicit Unix-socket/Windows-pipe dialer with proxies and configured retries disabled; there is no TCP fallback. Real in-memory and local-IPC tests prove authentication, initialization, reconnect fencing, lifecycle operations, streaming, stale-owner recovery, and cleanup. |

The isolated tools module pins Buf `v1.72.0` (Apache-2.0),
`protoc-gen-go` through Protobuf `v1.36.11` (BSD-3-Clause), and
`protoc-gen-go-grpc v1.6.2` (Apache-2.0). They run only through local Go `tool`
directives. Buf has no remote plugin or registry dependency in generation,
lint, or breaking checks. Generated Go is committed and compared byte-for-byte.

Google maintains gRPC and Protobuf and Buf maintains Buf. All three have active
security processes and broad Go adoption. Their substantial transitive graph is
accepted only at the process boundary; repository architecture checks prevent
it from entering the kernel. `govulncheck`, license review, vendor
reproducibility, cancellation tests, and upgrade/breaking review remain release
requirements. No dependency in this review authorizes hidden network access.

## Update process

Spice Framework maintainers own dependency review. Dependabot security updates
and private vulnerability reporting remain enabled as repository controls.
Critical reports are reviewed within one day; the complete dependency graph is
reviewed at least every 30 days.

Every change must preserve exact module selection, run `go mod tidy -diff`,
prove vendor reproducibility, recheck license and attribution, and pass gosec,
govulncheck, offline tests, and the full verifier. A breaking or behaviorally
material update also requires protocol/API review, cancellation and failure
evidence, and migration guidance. A security fix is released or the affected
version is withdrawn; dependency changes never authorize hidden network access.
The closed machine-readable process is
`compatibility/security-process.json`.

# Dependency Review

The kernel remains standard-library-first. Dependencies below are pinned in the
owning module and vendored; normal builds and checks use `GOPROXY=off`.

| Dependency | Pin/license | Owned use and review |
| --- | --- | --- |
| `github.com/spice-framework/spice` | `v0.1.0-preview.1.0.20260806200749-524424a04df0`, Apache-2.0 | Annotation SDK and portable starter identity. It adds no runtime container or network path. Replacement cost is limited to descriptor and manifest APIs. |
| `google.golang.org/protobuf` | `v1.36.11`, BSD-3-Clause | Generated messages plus deterministic marshal/clone support only in `common/v1` and `engine/v1`. Unknown fields are retained and lifecycle enums are validated fail-closed. |
| `google.golang.org/grpc` | `v1.83.0`, Apache-2.0 | Generated client/server interfaces and the local boundary's private endpoint-authentication interceptors. Real `bufconn` tests prove unary and streaming rejection before application handling. This slice opens no OS listener and creates no production connection. Cancellation, deadlines, message bounds, and mandatory interceptor installation belong to the server slice. |

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

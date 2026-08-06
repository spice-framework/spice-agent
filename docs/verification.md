# Verification

All ordinary repository commands are offline and select exactly the Go 1.26.5
toolchain that launched the quality gate. `make fast` is the affected feedback
loop, `make check` adds formatting, module/vendor, Protobuf, architecture, vet,
and shuffled tests, and `make verify` adds lint/NilAway, security, race, fuzz,
coverage, and vendor-only build proof.

A fresh machine runs `make tools-bootstrap` once. This is the sole
network-authorized quality-gate mode. It copies each `go.mod`/`go.sum` pair to a
temporary modfile, downloads the exact graph from the public Go proxy with
credentials and private-module environment removed, and verifies that no
repository byte changed. It does not tidy, vendor, generate, or install into the
repository.

`make proto` regenerates committed `common/v1` and `engine/v1` Go from local
tool directives. `make check` runs Buf lint, compares schemas with the committed
FILE-level `schema-baseline`, regenerates into a temporary directory using the
exact selected Go binary, and rejects any byte difference. There are no remote
Buf plugins or normal network fallbacks.

Generated `*.pb.go` and canonical Spice output remain compilation, test, race,
security, and offline-build inputs. They are excluded only from handwritten
formatting and coverage-denominator checks because their source schemas and
deterministic freshness are separately enforced.

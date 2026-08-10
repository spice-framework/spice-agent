# Verification

All ordinary repository commands are offline and select exactly the Go 1.26.5
toolchain that launched the quality gate. `make fast` is the affected feedback
loop, `make check` adds formatting, module/vendor, Protobuf, architecture, vet,
and shuffled tests, and `make verify` adds lint/NilAway, security, race, fuzz,
coverage, stable kernel runtime budgets, and vendor-only build proof.
Formatting enumerates the same complete sorted Go-file set in deterministic
bounded batches so Windows command-line limits cannot silently reduce coverage.

`make benchmark` is the bounded offline runtime comparison command. It executes
the four `BenchmarkKernel*` paths for 500 iterations and five samples on one
logical CPU with allocation reporting. The gate fails when a benchmark or
sample is missing, computes the median of each metric independently, and rejects
the stable time, byte, or allocation ceilings in `benchmarks/budgets.json`.
Those same budgets run in `make verify`. Comparable machine-specific results
remain subject to the stricter material-regression policy in
[`phase7-kernel-runtime-baseline.md`](implementation/evidence/phase7-kernel-runtime-baseline.md),
and any ceiling change requires measured evidence plus reviewed rationale.

Hosted CI keeps the cross-platform proof and the expensive repository-quality
mirror distinct. The reusable verifier remains pinned and owns Linux, macOS,
Windows, and vendor/offline jobs. One Ubuntu quality job bootstraps the pinned
tools and runs the full repository verifier; it is not duplicated on Windows.
The quality job has a bounded 40-minute cold-runner budget, while `verify`
receives a 30-minute internal deadline. Every other quality-gate mode retains
the 15-minute bound used by the local feedback loop. Repository identity fails
closed if the reusable pin, platform proof, single-mirror topology, ordering,
timeouts, or required aggregation drifts.

Fuzz smoke is deterministic across runner speeds: every registered target uses
Go's exact `-fuzztime=100x` execution budget instead of a wall-clock cutoff.
The quality gate locks the complete target list and rejects duration-based fuzz
arguments, preventing corpus discovery or minimization near a time boundary
from turning a successful smoke run into a deadline failure.

Every ordinary gate also enters the isolated permission, SQLite recovery,
two-worker, compaction, Git workflow, telemetry, and planning modules with `GOWORK=off`, `GOPROXY=off`, and
`-mod=vendor`. Check and verify modes reproduce each nested vendor tree, verify
its generated Spice target, run vet and shuffled tests, and verify additionally
runs race plus an 85% handwritten package coverage floor. The compaction proof
also runs its exact `FuzzCompact` target, Git runs `FuzzDecodeCommitArguments`,
telemetry runs `FuzzTranslateEnvelope`, and planning runs `FuzzParsePlan`, each
for 100 deterministic executions.

A fresh machine runs `make tools-bootstrap` once. This is the sole
network-authorized quality-gate mode. It copies each `go.mod`/`go.sum` pair to a
temporary modfile, downloads the exact graph from the public Go proxy with
credentials and private-module environment removed, and verifies that no
repository byte changed. It does not tidy, vendor, generate, or install into the
repository.

`make proto` regenerates committed `common/v1`, `engine/v1`, and `plugin/v1` Go from local
tool directives. `make check` runs Buf lint, compares schemas with the committed
FILE-level `schema-baseline`, regenerates into a temporary directory using the
exact selected Go binary, and rejects any byte difference. There are no remote
Buf plugins or normal network fallbacks.

Generated `*.pb.go` and canonical Spice output remain compilation, test, race,
security, and offline-build inputs. They are excluded only from handwritten
formatting and coverage-denominator checks because their source schemas and
deterministic freshness are separately enforced.

Every quality-gate mode validates all strict canonical compatibility manifests,
their cross-references, append-only histories, and the exact 26-package public
Go surface. The API gate evaluates `darwin/arm64`, `linux/amd64`, and
`windows/amd64` separately with offline `go list`; generated protocol exports
are included and mutually exclusive platform files are never unioned. The
read-only `go run ./internal/qualitygate -mode=api-baseline` command prints the
current package/declaration digests for review.

Ordinary tests launch source-built
previous-semantics and current-semantics servers over the public local IPC,
gRPC client, and server boundaries. The decisive process matrix runs on Linux
and Windows, checks retry/ambiguity/cancellation/cleanup behavior, and is
explicitly not evidence of released-binary N/N-1 compatibility.

The independent `plugin/v1/compatibility.json` manifest locks the plugin/v1
breadth claim. Ordinary hosted
tests launch the source-built Go plugin fixture, validate its public protocol
traffic, and route its immutable leased tool plan through exact engine 1.2 and
1.3 clients. `make verify-python` repeats that matrix with the independently
implemented, frozen, offline Python fixture after checking its lock, generated
bindings, unit suite, and bytecode compilation. Both paths prove result
equivalence, cancellation terminals, process-loss failure, and lease cleanup;
neither couples plugin/v1 versioning to the engine protocol or claims native
Python `pluginhost.NewHost` launch containment.

Repository identity also validates `.github/workflows/release.yml` as a
single-job, secret-free caller of the organization keyless Go-module release
workflow at its exact audited commit. The caller must deny permissions at the
workflow level and may grant only `contents`, `id-token`, `attestations`, and
`artifact-metadata` writes to the reusable release job. Extra permissions,
local steps, additional jobs, legacy workflows, module drift, and either named
or inherited secrets fail every verification mode before product tests run.

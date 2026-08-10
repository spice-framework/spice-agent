# Verification

All ordinary repository commands are offline and select exactly the Go 1.26.5
toolchain that launched the quality gate. `make fast` is the affected feedback
loop, `make check` adds formatting, module/vendor, Protobuf, architecture, vet,
and shuffled tests, and `make verify` adds lint/NilAway, security, race, fuzz,
coverage, and vendor-only build proof.
Formatting enumerates the same complete sorted Go-file set in deterministic
bounded batches so Windows command-line limits cannot silently reduce coverage.

`make benchmark` is the bounded offline runtime comparison command. It executes
the four `BenchmarkKernel*` paths for 500 iterations and five samples on one
logical CPU with allocation reporting. The gate fails when a benchmark or
sample is missing; machine-specific time and allocation results are compared
under the regression policy in
[`phase7-kernel-runtime-baseline.md`](implementation/evidence/phase7-kernel-runtime-baseline.md),
not enforced as noisy absolute thresholds on unrelated hosts.

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

Repository identity also validates `.github/workflows/release.yml` as a
single-job, secret-free caller of the organization keyless Go-module release
workflow at its exact audited commit. The caller must deny permissions at the
workflow level and may grant only `contents`, `id-token`, `attestations`, and
`artifact-metadata` writes to the reusable release job. Extra permissions,
local steps, additional jobs, legacy workflows, module drift, and either named
or inherited secrets fail every verification mode before product tests run.

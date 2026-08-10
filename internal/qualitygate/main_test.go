package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNetworkIsReservedForExplicitBootstrap(t *testing.T) {
	t.Parallel()
	if !networkAllowed("tools-bootstrap") {
		t.Fatal("tools-bootstrap does not allow dependency download")
	}
	for _, mode := range []string{"fast", "check", "coverage", "benchmark", "verify"} {
		if networkAllowed(mode) {
			t.Fatalf("ordinary gate %q allows network access", mode)
		}
	}
}

func TestVerificationTimeoutIsModeAware(t *testing.T) {
	t.Parallel()
	if timeout := gateTimeout("verify"); timeout != 30*time.Minute {
		t.Fatalf("verify timeout = %s", timeout)
	}
	for _, mode := range []string{"tools-bootstrap", "proto", "fast", "check", "coverage", "benchmark", "unknown"} {
		if timeout := gateTimeout(mode); timeout != 15*time.Minute {
			t.Fatalf("%s timeout = %s", mode, timeout)
		}
	}
}

func TestValidatePermissionCoverage(t *testing.T) {
	t.Parallel()
	if err := validatePermissionCoverage("ok\tpermission\t0.1s\tcoverage: 88.2% of statements\n"); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		"ok\tpermission\t0.1s\tcoverage: 84.9% of statements\n",
		"ok\tpermission\t0.1s\n",
		"ok\tpermission\tcoverage: bad% of statements\n",
	} {
		if err := validatePermissionCoverage(output); err == nil {
			t.Fatalf("validatePermissionCoverage(%q) succeeded", output)
		}
	}
}

func TestValidateSQLiteRecoveryCoverage(t *testing.T) {
	t.Parallel()
	if err := validateSQLiteRecoveryCoverage("ok\tsqliterecovery\tcoverage: 85.4% of statements\n"); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		"ok\tsqliterecovery\tcoverage: 84.9% of statements\n",
		"ok\tsqliterecovery\n",
		"ok\tsqliterecovery\tcoverage: invalid% of statements\n",
	} {
		if err := validateSQLiteRecoveryCoverage(output); err == nil {
			t.Fatalf("validateSQLiteRecoveryCoverage(%q) succeeded", output)
		}
	}
}

func TestValidateTwoWorkerCoverage(t *testing.T) {
	t.Parallel()
	if err := validateTwoWorkerCoverage("ok\ttwoworker\tcoverage: 85.7% of statements\n"); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		"ok\ttwoworker\tcoverage: 84.9% of statements\n",
		"ok\ttwoworker\n",
		"ok\ttwoworker\tcoverage: invalid% of statements\n",
	} {
		if err := validateTwoWorkerCoverage(output); err == nil {
			t.Fatalf("validateTwoWorkerCoverage(%q) succeeded", output)
		}
	}
}

func TestValidateCompactionCoverageAndDeterministicFuzzBudget(t *testing.T) {
	t.Parallel()
	if err := validateCompactionCoverage("ok\tcompaction\tcoverage: 85.1% of statements\n"); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		"ok\tcompaction\tcoverage: 84.9% of statements\n",
		"ok\tcompaction\n",
		"ok\tcompaction\tcoverage: invalid% of statements\n",
	} {
		if err := validateCompactionCoverage(output); err == nil {
			t.Fatalf("validateCompactionCoverage(%q) succeeded", output)
		}
	}
	want := []string{"test", "-run=^$", "-fuzz=^FuzzCompact$", "-fuzztime=100x", "."}
	if got := compactionFuzzArguments(); !slices.Equal(got, want) {
		t.Fatalf("compaction fuzz arguments = %q", got)
	}
	if strings.Contains(strings.Join(compactionFuzzArguments(), " "), "1s") {
		t.Fatal("compaction fuzz smoke uses a wall-clock duration")
	}
}

func TestValidateGitWorkflowCoverageAndDeterministicFuzzBudget(t *testing.T) {
	t.Parallel()
	if err := validateGitWorkflowCoverage("ok\tgitworkflow\tcoverage: 85.6% of statements\n"); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		"ok\tgitworkflow\tcoverage: 84.9% of statements\n",
		"ok\tgitworkflow\n",
		"ok\tgitworkflow\tcoverage: invalid% of statements\n",
	} {
		if err := validateGitWorkflowCoverage(output); err == nil {
			t.Fatalf("validateGitWorkflowCoverage(%q) succeeded", output)
		}
	}
	want := []string{"test", "-run=^$", "-fuzz=^FuzzDecodeCommitArguments$", "-fuzztime=100x", "."}
	if got := gitWorkflowFuzzArguments(); !slices.Equal(got, want) {
		t.Fatalf("Git workflow fuzz arguments = %q", got)
	}
	if strings.Contains(strings.Join(gitWorkflowFuzzArguments(), " "), "1s") {
		t.Fatal("Git workflow fuzz smoke uses a wall-clock duration")
	}
}

func TestValidateTelemetryCoverageAndDeterministicFuzzBudget(t *testing.T) {
	t.Parallel()
	if err := validateTelemetryCoverage("ok\ttelemetry\tcoverage: 86.2% of statements\n"); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		"ok\ttelemetry\tcoverage: 84.9% of statements\n",
		"ok\ttelemetry\n",
		"ok\ttelemetry\tcoverage: invalid% of statements\n",
	} {
		if err := validateTelemetryCoverage(output); err == nil {
			t.Fatalf("validateTelemetryCoverage(%q) succeeded", output)
		}
	}
	want := []string{"test", "-run=^$", "-fuzz=^FuzzTranslateEnvelope$", "-fuzztime=100x", "."}
	if got := telemetryFuzzArguments(); !slices.Equal(got, want) {
		t.Fatalf("telemetry fuzz arguments = %q", got)
	}
	if strings.Contains(strings.Join(telemetryFuzzArguments(), " "), "1s") {
		t.Fatal("telemetry fuzz smoke uses a wall-clock duration")
	}
}

func TestKernelRuntimeBenchmarkContractIsBoundedAndFailsClosed(t *testing.T) {
	t.Parallel()
	wantArguments := []string{
		"test", "-run=^$", "-bench=^BenchmarkKernel", "-benchmem",
		"-benchtime=500x", "-count=5", "-cpu=1", "./agent",
	}
	if actual := kernelRuntimeBenchmarkArguments(); !slices.Equal(actual, wantArguments) {
		t.Fatalf("kernel benchmark arguments = %q", actual)
	}
	valid := strings.Repeat(`BenchmarkKernelEngineConstruction 500 1 ns/op 1 B/op 1 allocs/op
BenchmarkKernelTextRun 500 1 ns/op 1 B/op 1 allocs/op
BenchmarkKernelToolRound 500 1 ns/op 1 B/op 1 allocs/op
BenchmarkKernelCancellation 500 1 ns/op 1 B/op 1 allocs/op
`, 5)
	if err := validateKernelRuntimeBenchmarkOutput(valid); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{
		"BenchmarkKernelEngineConstruction", "BenchmarkKernelTextRun",
		"BenchmarkKernelToolRound", "BenchmarkKernelCancellation",
	} {
		invalid := strings.Replace(valid, missing, "BenchmarkOmitted", 1)
		if err := validateKernelRuntimeBenchmarkOutput(invalid); err == nil || !strings.Contains(err.Error(), "require 5") {
			t.Fatalf("missing benchmark sample %q = %v", missing, err)
		}
	}
}

func TestRepositoryPortabilityRequiresLFAndExplicitToolBootstrap(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGateFile(t, root, ".gitattributes", "* text=auto eol=lf\n*.pb -text\n*.png -text\n")
	writeGateFile(t, root, ".github/workflows/ci.yml", validCIWorkflow())
	if err := checkRepositoryPortability(root); err != nil {
		t.Fatal(err)
	}

	writeGateFile(
		t,
		root,
		".github/workflows/ci.yml",
		strings.Replace(validCIWorkflow(), "      - run: go run ./internal/qualitygate -mode=tools-bootstrap\n", "", 1),
	)
	if err := checkRepositoryPortability(root); err == nil || !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("missing bootstrap error = %v", err)
	}
}

func TestCIWorkflowPreservesUniqueGatesAndSingleQualityMirror(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		workflow string
		wantErr  string
	}{
		{name: "valid", workflow: validCIWorkflow()},
		{
			name:     "wrong reusable pin",
			workflow: strings.Replace(validCIWorkflow(), verifyWorkflowCommit, strings.Repeat("0", 40), 1),
			wantErr:  "go-verify.yml@",
		},
		{
			name:     "duplicate windows quality",
			workflow: strings.Replace(validCIWorkflow(), "    runs-on: ubuntu-latest\n", "    strategy: {matrix: {os: [ubuntu-latest, windows-latest]}}\n    runs-on: ${{ matrix.os }}\n", 1),
			wantErr:  "runs-on: ubuntu-latest",
		},
		{
			name:     "short cold runner budget",
			workflow: strings.Replace(validCIWorkflow(), "    timeout-minutes: 40\n", "    timeout-minutes: 20\n", 1),
			wantErr:  "timeout-minutes: 40",
		},
		{
			name:     "missing reusable verification",
			workflow: strings.Replace(validCIWorkflow(), "  verify:\n", "  omitted:\n", 1),
			wantErr:  "jobs must be",
		},
		{
			name:     "missing required aggregation",
			workflow: strings.Replace(validCIWorkflow(), "    needs: [verify, quality]\n", "    needs: [quality]\n", 1),
			wantErr:  "needs: [verify, quality]",
		},
		{
			name:     "duplicate full verifier",
			workflow: strings.Replace(validCIWorkflow(), "      - run: go run ./internal/qualitygate -mode=verify\n", "      - run: go run ./internal/qualitygate -mode=verify\n      - run: go run ./internal/qualitygate -mode=verify\n", 1),
			wantErr:  "exactly once",
		},
		{
			name:     "extra job",
			workflow: validCIWorkflow() + "  extra:\n    runs-on: ubuntu-latest\n",
			wantErr:  "jobs must be",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := checkCIWorkflow(test.workflow)
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("checkCIWorkflow() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func validCIWorkflow() string {
	return `name: CI
on:
  push: {branches: [main]}
  pull_request: {branches: [main]}
permissions: {contents: read}
jobs:
  verify:
    uses: spice-framework/.github/.github/workflows/go-verify.yml@` + verifyWorkflowCommit + `
    with: {go-version: 1.26.5}
  quality:
    runs-on: ubuntu-latest
    timeout-minutes: 40
    steps:
      - run: go run ./internal/qualitygate -mode=tools-bootstrap
      - run: go run ./internal/qualitygate -mode=verify
  required:
    if: always()
    needs: [verify, quality]
    runs-on: ubuntu-latest
    steps:
      - run: test "${{ needs.verify.result }}" = success && test "${{ needs.quality.result }}" = success
`
}

func TestReleaseWorkflowRequiresExactKeylessBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		workflow string
		wantErr  string
		omit     bool
	}{
		{name: "valid", workflow: validReleaseWorkflow()},
		{name: "missing", wantErr: "read release workflow", omit: true},
		{
			name:     "wrong reusable pin",
			workflow: strings.Replace(validReleaseWorkflow(), releaseWorkflowCommit, strings.Repeat("0", 40), 1),
			wantErr:  "uses:",
		},
		{
			name: "superseded preview.1 caller",
			workflow: strings.ReplaceAll(
				validReleaseWorkflow(),
				releaseWorkflowCommit,
				"c4df74b2c60640c60fe0fa3fe641dadafbc4148a",
			),
			wantErr: "uses:",
		},
		{
			name: "superseded preview.2 caller",
			workflow: strings.ReplaceAll(
				validReleaseWorkflow(),
				releaseWorkflowCommit,
				"07f898b85e7d1c409b91bf280e47d62921e786b6",
			),
			wantErr: "uses:",
		},
		{
			name: "superseded preview.3 caller",
			workflow: strings.ReplaceAll(
				validReleaseWorkflow(),
				releaseWorkflowCommit,
				"9b35ae8173d76f3baf9c63d74189863bc6f59e86",
			),
			wantErr: "uses:",
		},
		{
			name: "superseded preview.4 caller",
			workflow: strings.ReplaceAll(
				validReleaseWorkflow(),
				releaseWorkflowCommit,
				"0fcd43dc8b41fad56c231d0e136ad8c762276ed5",
			),
			wantErr: "uses:",
		},
		{
			name: "wrong attested workflow pin",
			workflow: strings.Replace(
				validReleaseWorkflow(),
				"workflow_commit: "+releaseWorkflowCommit,
				"workflow_commit: "+strings.Repeat("0", 40),
				1,
			),
			wantErr: "workflow_commit:",
		},
		{
			name:     "wrong module",
			workflow: strings.Replace(validReleaseWorkflow(), modulePath, "example.com/wrong", 1),
			wantErr:  "module:",
		},
		{
			name:     "legacy workflow",
			workflow: strings.Replace(validReleaseWorkflow(), "go-module-release.yml", "library-release.yml", 1),
			wantErr:  "go-module-release.yml",
		},
		{
			name:     "inherited secrets",
			workflow: validReleaseWorkflow() + "    secrets: inherit\n",
			wantErr:  "secrets:",
		},
		{
			name:     "named signing secret",
			workflow: validReleaseWorkflow() + "    secrets:\n      SPICE_LIBRARY_RELEASE_SIGNING_KEY: value\n",
			wantErr:  "secrets:",
		},
		{
			name: "extra permission",
			workflow: strings.Replace(
				validReleaseWorkflow(),
				"      contents: write\n",
				"      contents: write\n      packages: write\n",
				1,
			),
			wantErr: "permission ceiling",
		},
		{
			name:     "missing permission",
			workflow: strings.Replace(validReleaseWorkflow(), "      attestations: write\n", "", 1),
			wantErr:  "attestations: write",
		},
		{
			name:     "extra permission block",
			workflow: validReleaseWorkflow() + "permissions: read-all\n",
			wantErr:  "permission blocks",
		},
		{
			name:     "extra job",
			workflow: validReleaseWorkflow() + "  publish-again:\n    uses: example.invalid/workflow.yml@deadbeef\n",
			wantErr:  "only the release job",
		},
		{
			name:     "local steps",
			workflow: validReleaseWorkflow() + "    steps:\n      - run: echo unsafe\n",
			wantErr:  "steps:",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if !test.omit {
				writeGateFile(t, root, ".github/workflows/release.yml", test.workflow)
			}
			err := checkReleaseWorkflow(root)
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("checkReleaseWorkflow() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func validReleaseWorkflow() string {
	return `name: Release

on:
  push:
    tags:
      - "v[0-9]*.[0-9]*.[0-9]*"

permissions: {}

jobs:
  release:
    name: Keylessly attest and publish
    permissions:
      contents: write
      id-token: write
      attestations: write
      artifact-metadata: write
    uses: spice-framework/.github/.github/workflows/go-module-release.yml@` + releaseWorkflowCommit + `
    with:
      module: ` + modulePath + `
      workflow_commit: ` + releaseWorkflowCommit + `
`
}

func TestBootstrapUsesCopiedModuleGraphAndPreservesSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGateFile(t, root, "go.mod", "module example.com/product\n\ngo 1.26.0\n")
	writeGateFile(t, root, "go.sum", "example.com/dependency v1.0.0 h1:test\n")
	writeGateFile(t, root, "tools/go.mod", "module example.com/product/tools\n\ngo 1.26.0\n")
	writeGateFile(t, root, "tools/go.sum", "example.com/tool v1.0.0 h1:test\n")
	writeGateFile(t, root, "experiments/permission/go.mod", "module example.com/product/experiments/permission\n\ngo 1.26.0\n")
	writeGateFile(t, root, "experiments/permission/go.sum", "example.com/policy v1.0.0 h1:test\n")
	writeGateFile(t, root, "experiments/sqlite-recovery/go.mod", "module example.com/product/experiments/sqlite-recovery\n\ngo 1.26.0\n")
	writeGateFile(t, root, "experiments/sqlite-recovery/go.sum", "example.com/sqlite v1.0.0 h1:test\n")
	writeGateFile(t, root, "experiments/two-worker/go.mod", "module example.com/product/experiments/two-worker\n\ngo 1.26.0\n")
	writeGateFile(t, root, "experiments/two-worker/go.sum", "example.com/session v1.0.0 h1:test\n")
	writeGateFile(t, root, "experiments/compaction/go.mod", "module example.com/product/experiments/compaction\n\ngo 1.26.0\n")
	writeGateFile(t, root, "experiments/compaction/go.sum", "example.com/model v1.0.0 h1:test\n")
	writeGateFile(t, root, "experiments/git-workflow/go.mod", "module example.com/product/experiments/git-workflow\n\ngo 1.26.0\n")
	writeGateFile(t, root, "experiments/git-workflow/go.sum", "example.com/process v1.0.0 h1:test\n")
	writeGateFile(t, root, "experiments/telemetry/go.mod", "module example.com/product/experiments/telemetry\n\ngo 1.26.0\n")
	writeGateFile(t, root, "experiments/telemetry/go.sum", "example.com/telemetry v1.0.0 h1:test\n")

	var directories []string
	err := bootstrapDependencies(
		context.Background(),
		root,
		func(_ context.Context, directory string, arguments ...string) error {
			directories = append(directories, directory)
			if len(arguments) != 4 || arguments[0] != "mod" ||
				arguments[1] != "download" || arguments[3] != "all" ||
				!strings.HasPrefix(arguments[2], "-modfile=") {
				t.Fatalf("bootstrap arguments = %q", arguments)
			}
			if filepath.Dir(strings.TrimPrefix(arguments[2], "-modfile=")) == directory {
				t.Fatal("bootstrap used the repository-owned go.mod")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(directories, []string{
		root,
		filepath.Join(root, "tools"),
		filepath.Join(root, "experiments", "permission"),
		filepath.Join(root, "experiments", "sqlite-recovery"),
		filepath.Join(root, "experiments", "two-worker"),
		filepath.Join(root, "experiments", "compaction"),
		filepath.Join(root, "experiments", "git-workflow"),
		filepath.Join(root, "experiments", "telemetry"),
	}) {
		t.Fatalf("bootstrap directories = %q", directories)
	}
}

func TestBootstrapDetectsSourceMutationAndCancellation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		runner bootstrapRunner
		want   error
	}{
		{
			name: "mutation",
			runner: func(_ context.Context, directory string, _ ...string) error {
				return os.WriteFile(filepath.Join(directory, "unexpected.txt"), []byte("changed"), 0o600)
			},
			want: errors.New("dependency bootstrap modified the repository"),
		},
		{
			name: "cancellation",
			runner: func(ctx context.Context, _ string, _ ...string) error {
				return ctx.Err()
			},
			want: context.Canceled,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeGateFile(t, root, "go.mod", "module example.com/product\n\ngo 1.26.0\n")
			ctx := context.Background()
			if test.name == "cancellation" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := bootstrapDependencies(ctx, root, test.runner)
			if err == nil || !strings.Contains(err.Error(), test.want.Error()) {
				t.Fatalf("bootstrap error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCommandEnvironmentIsOfflineAndScrubsSecrets(t *testing.T) {
	t.Setenv("SPICE_TEST_TOKEN", "secret")
	t.Setenv("SPICE_TEST_PASSWORD", "secret")
	offline := strings.Join(commandEnvironment(false, nil), "\n")
	for _, expected := range []string{
		"GOPROXY=off", "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=",
	} {
		if !strings.Contains(offline, expected) {
			t.Fatalf("offline environment missing %q:\n%s", expected, offline)
		}
	}
	if strings.Contains(offline, "SPICE_TEST_TOKEN") ||
		strings.Contains(offline, "SPICE_TEST_PASSWORD") {
		t.Fatalf("offline environment retained a secret:\n%s", offline)
	}
	network := strings.Join(commandEnvironment(true, nil), "\n")
	for _, expected := range []string{
		"GOAUTH=off", "GOPROXY=https://proxy.golang.org", "GOSUMDB=sum.golang.org",
	} {
		if !strings.Contains(network, expected) {
			t.Fatalf("bootstrap environment missing %q:\n%s", expected, network)
		}
	}
}

func TestSelectedGoExecutableName(t *testing.T) {
	t.Parallel()
	if goExecutableName("windows") != "go.exe" || goExecutableName("linux") != "go" {
		t.Fatal("selected Go executable names are not portable")
	}
}

func TestProcessFuzzContractsRemainInTheReleaseGate(t *testing.T) {
	t.Parallel()
	seen := make(map[fuzzTarget]int)
	for _, target := range fuzzTargets() {
		seen[target]++
	}
	for _, target := range []fuzzTarget{
		{"./process", "FuzzSpecValidation"},
		{"./process", "FuzzExitedOutcome"},
		{"./process", "FuzzLookupValidation"},
		{"./process", "FuzzSHA256"},
	} {
		if seen[target] != 1 {
			t.Fatalf("fuzz target %#v occurs %d times", target, seen[target])
		}
	}
}

func TestPluginFuzzContractRemainsInTheReleaseGate(t *testing.T) {
	t.Parallel()
	seen := make(map[fuzzTarget]int)
	for _, target := range fuzzTargets() {
		seen[target]++
	}
	for _, target := range []fuzzTarget{
		{"./plugin/v1", "FuzzPluginEnvelope"},
		{"./plugin/v1", "FuzzBootstrap"},
	} {
		if seen[target] != 1 {
			t.Fatalf("fuzz target %#v occurs %d times", target, seen[target])
		}
	}
}

func TestFuzzSmokeUsesDeterministicExecutionBudgetForEveryTarget(t *testing.T) {
	t.Parallel()
	expected := []fuzzTarget{
		{"./message", "FuzzNewID"},
		{"./tool", "FuzzToolCall"},
		{"./process", "FuzzSpecValidation"},
		{"./process", "FuzzExitedOutcome"},
		{"./process", "FuzzLookupValidation"},
		{"./process", "FuzzSHA256"},
		{"./agent", "FuzzParseSnapshot"},
		{"./agent", "FuzzDecodeToolStartedOccurrence"},
		{"./agent", "FuzzDecodeToolTerminalOccurrence"},
		{"./annotation/agent", "FuzzToolHandler"},
		{"./common/v1", "FuzzCommonEnvelope"},
		{"./engine/v1", "FuzzEngineEnvelope"},
		{"./plugin/v1", "FuzzPluginEnvelope"},
		{"./plugin/v1", "FuzzBootstrap"},
		{"./daemon/endpoint", "FuzzDecodeMetadata"},
	}
	if actual := fuzzTargets(); !slices.Equal(actual, expected) {
		t.Fatalf("fuzz targets = %#v", actual)
	}
	for _, target := range expected {
		want := []string{"test", "-run=^$", "-fuzz=^" + target.name + "$", "-fuzztime=100x", target.pkg}
		arguments := fuzzArguments(target)
		if !slices.Equal(arguments, want) {
			t.Fatalf("fuzz arguments for %#v = %q", target, arguments)
		}
		for _, argument := range arguments {
			if strings.HasPrefix(argument, "-fuzztime=") && argument != "-fuzztime=100x" {
				t.Fatalf("fuzz target %#v uses nondeterministic budget %q", target, argument)
			}
		}
	}
}

func TestPluginHostDependencyDirectionIsEnforced(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGateFile(t, root, "plugin/host/host.go", `package pluginhost

import "github.com/spice-framework/spice-agent/daemon/localipc"

var _ = localipc.ErrUnsafeEndpoint
`)
	if err := checkArchitecture(root); err == nil ||
		!strings.Contains(err.Error(), "plugin host file plugin/host/host.go imports forbidden package") {
		t.Fatalf("forbidden host dependency error = %v", err)
	}

	writeGateFile(t, root, "plugin/host/host.go", `package pluginhost

import pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"

var _ = pluginv1.ProtocolMajor
`)
	if err := checkArchitecture(root); err != nil {
		t.Fatalf("allowed host dependency failed: %v", err)
	}
}

func TestToolDispatchBoundaryFailsClosed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGateFile(t, root, "go.mod", "module github.com/spice-framework/spice-agent\n")
	for name, content := range map[string]string{
		"agent/agent.go": `package agent
// stage.NewToolDispatchScope(
// emitter.run.requester
// type runInteractionRequester struct
// return requester.run.Interact(ctx, request)
// NewToolStartedOccurrence(
// NewToolTerminalOccurrence(
// occurrence.Encode()
// dispatcher.Dispatch(ctx, scope, call, reporter)
`,
		"agent/prepared_execution.go": `package agent
// run.requester = &runInteractionRequester{run: run}
`,
		"agent/tool_started.go": `package agent
// ToolStartedOccurrenceVersion = "spice.agent.tool-started/v1alpha1"
// MaximumToolStartedOccurrenceBytes = 4096
// func DecodeToolStartedOccurrence(
`,
		"agent/tool_terminal.go": `package agent
// ToolTerminalOccurrenceVersion = "spice.agent.tool-terminal/v1alpha1"
// MaximumToolTerminalOccurrenceBytes = 1024
// func DecodeToolTerminalOccurrence(
// kind == event.ToolCompleted || kind == event.ToolFailed
`,
		"daemon/grpcserver/stream_events.go": `package grpcserver
// agent.DecodeToolStartedOccurrence(payload)
// agent.DecodeToolTerminalOccurrence(kind, payload)
// problem = "tool execution failed"
// CallID string ` + "`json:\"call_id\"`" + `
// Name   string ` + "`json:\"name\"`" + `
`,
		"plugin/host/host.go": `package pluginhost
// Processes    process.VerifiedLauncher
// stage.SnapshotToolDispatcher(config.Compiled)
// stage.ApplyToolDispatchPipeline(base, guards, decorators)
// stage.ApplyToolDispatchPipeline(merged, host.guards, host.decorators)
`,
		"process/verified_launcher.go": `package process
// type VerifiedLauncher interface
// StartVerified(context.Context, *ExecutableLease, Spec) (Process, error)
`,
		"process/verified_executable.go": `package process
// func VerifyExecutable(
// func (lease *ExecutableLease) ValidateSpec(
// func (lease *ExecutableLease) DuplicateForLaunch(
// func (lease *ExecutableLease) Recheck(
`,
		"process/materialized_executable.go": `package process
// func (lease *ExecutableLease) MaterializeForLaunch(
// os.MkdirTemp(parent, materializedExecutableDirectoryPattern)
// destination.Sync()
// VerifyExecutable(ctx, path, lease.Digest())
// cleanupMaterializedExecutable(path, directory)
`,
		"plugin/host/digest.go": `package pluginhost
// type SHA256 = process.SHA256
// process.VerifyExecutable(ctx, executable.Path(), executable.SHA256())
`,
		"plugin/host/launcher.go": `package pluginhost
// processes process.VerifiedLauncher
// launcher.processes.StartVerified(ctx, candidate.lease, spec)
// recheckVerifiedExecutable(ctx, candidate.lease)
`,
		"plugin/host/candidate.go": `package pluginhost
// lease      *process.ExecutableLease
// closeVerifiedExecutable(candidate.lease)
`,
		"plugin/host/autoconfigure/autoconfigure.go": `package autoconfigure
// launcher process.VerifiedLauncher
`,
		"stage/dispatch_guard.go": `package stage
// type ToolDispatchScope struct
// interactionRequester *toolInteractionCapability
// func (scope ToolDispatchScope) RequestInteraction(
// scope.interactionRequester == other.interactionRequester
// type ToolDispatchGuard interface
// tool dispatch continuation is closed or was already invoked
`,
		"internal/spicegen/compositionproof/spice_providers_gen.go": `package compositionproof
// []stage.ToolDispatchGuard{fixtureDispatchGuard}
`,
	} {
		writeGateFile(t, root, name, content)
	}
	if err := checkToolDispatchBoundary(root); err != nil {
		t.Fatal(err)
	}
	writeGateFile(t, root, "agent/tool_terminal.go", "package agent\n")
	if err := checkToolDispatchBoundary(root); err == nil || !strings.Contains(err.Error(), "ToolTerminalOccurrenceVersion") {
		t.Fatalf("missing typed tool terminal occurrence = %v", err)
	}
	writeGateFile(t, root, "agent/tool_terminal.go", `package agent
// ToolTerminalOccurrenceVersion = "spice.agent.tool-terminal/v1alpha1"
// MaximumToolTerminalOccurrenceBytes = 1024
// func DecodeToolTerminalOccurrence(
// kind == event.ToolCompleted || kind == event.ToolFailed
`)
	writeGateFile(t, root, "agent/prepared_execution.go", "package agent\n")
	if err := checkToolDispatchBoundary(root); err == nil || !strings.Contains(err.Error(), "runInteractionRequester") {
		t.Fatalf("missing run-owned interaction requester = %v", err)
	}
	writeGateFile(t, root, "agent/prepared_execution.go", `package agent
// run.requester = &runInteractionRequester{run: run}
`)
	writeGateFile(t, root, "plugin/host/host.go", "package pluginhost\n// Processes    process.VerifiedLauncher\n// stage.SnapshotToolDispatcher(config.Compiled)\n")
	if err := checkToolDispatchBoundary(root); err == nil || !strings.Contains(err.Error(), "ApplyToolDispatchPipeline") {
		t.Fatalf("missing host pipeline = %v", err)
	}
	writeGateFile(t, root, "plugin/host/host.go", `package pluginhost
// Processes    process.VerifiedLauncher
// stage.SnapshotToolDispatcher(config.Compiled)
// stage.ApplyToolDispatchPipeline(base, guards, decorators)
// stage.ApplyToolDispatchPipeline(merged, host.guards, host.decorators)
`)
	writeGateFile(t, root, "other/bypass.go", "package other\nfunc x() { ApplyToolDispatchDecorators(nil, nil) }\n")
	if err := checkArchitecture(root); err == nil || !strings.Contains(err.Error(), "bypasses terminal") {
		t.Fatalf("decorator-only composition = %v", err)
	}
	writeGateFile(t, root, "other/bypass.go", "package other\n")
	writeGateFile(t, root, "plugin/host/launcher.go", "package pluginhost\n// processes process.Launcher\n")
	if err := checkToolDispatchBoundary(root); err == nil || !strings.Contains(err.Error(), "VerifiedLauncher") {
		t.Fatalf("unsafe pathname launcher boundary = %v", err)
	}
	writeGateFile(t, root, "process/materialized_executable.go", "package process\n")
	if err := checkToolDispatchBoundary(root); err == nil || !strings.Contains(err.Error(), "MaterializeForLaunch") {
		t.Fatalf("missing Darwin private materialization boundary = %v", err)
	}
}

func TestFormattingBatchesPreserveEveryFileWithinWindowsCommandBounds(t *testing.T) {
	t.Parallel()
	files := make([]string, 0, 1000)
	for index := range 1000 {
		files = append(files, filepath.Join(
			strings.Repeat("long-directory-", 8),
			fmt.Sprintf("source-%04d.go", index),
		))
	}
	batches := formattingBatches(files)
	var flattened []string
	for _, batch := range batches {
		if len(batch) == 0 || len(batch) > maximumFormattingBatchFiles {
			t.Fatalf("formatting batch file count = %d", len(batch))
		}
		bytes := len("-l")
		for _, file := range batch {
			bytes += len(file) + 3
		}
		if bytes > maximumFormattingBatchBytes {
			t.Fatalf("formatting batch bytes = %d", bytes)
		}
		flattened = append(flattened, batch...)
	}
	if !slices.Equal(flattened, files) {
		t.Fatal("formatting batches omitted, duplicated, or reordered files")
	}
	if batches := formattingBatches(nil); len(batches) != 0 {
		t.Fatalf("empty formatting batches = %#v", batches)
	}
}

func writeGateFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

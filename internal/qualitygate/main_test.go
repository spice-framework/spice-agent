package main

import (
	"context"
	"errors"
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
	for _, mode := range []string{"fast", "check", "coverage", "verify"} {
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
	for _, mode := range []string{"tools-bootstrap", "proto", "fast", "check", "coverage", "unknown"} {
		if timeout := gateTimeout(mode); timeout != 15*time.Minute {
			t.Fatalf("%s timeout = %s", mode, timeout)
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
	if !slices.Equal(directories, []string{root, filepath.Join(root, "tools")}) {
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
		{"./agent", "FuzzParseSnapshot"},
		{"./agent", "FuzzDecodeToolStartedOccurrence"},
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
		"daemon/grpcserver/stream_events.go": `package grpcserver
// agent.DecodeToolStartedOccurrence(envelope.Data())
// CallID string ` + "`json:\"call_id\"`" + `
// Name   string ` + "`json:\"name\"`" + `
`,
		"plugin/host/host.go": `package pluginhost
// stage.SnapshotToolDispatcher(config.Compiled)
// stage.ApplyToolDispatchPipeline(base, guards, decorators)
// stage.ApplyToolDispatchPipeline(merged, host.guards, host.decorators)
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
	writeGateFile(t, root, "agent/prepared_execution.go", "package agent\n")
	if err := checkToolDispatchBoundary(root); err == nil || !strings.Contains(err.Error(), "runInteractionRequester") {
		t.Fatalf("missing run-owned interaction requester = %v", err)
	}
	writeGateFile(t, root, "agent/prepared_execution.go", `package agent
// run.requester = &runInteractionRequester{run: run}
`)
	writeGateFile(t, root, "plugin/host/host.go", "package pluginhost\n// stage.SnapshotToolDispatcher(config.Compiled)\n")
	if err := checkToolDispatchBoundary(root); err == nil || !strings.Contains(err.Error(), "ApplyToolDispatchPipeline") {
		t.Fatalf("missing host pipeline = %v", err)
	}
	writeGateFile(t, root, "other/bypass.go", "package other\nfunc x() { ApplyToolDispatchDecorators(nil, nil) }\n")
	if err := checkArchitecture(root); err == nil || !strings.Contains(err.Error(), "bypasses terminal") {
		t.Fatalf("decorator-only composition = %v", err)
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

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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

func TestRepositoryPortabilityRequiresLFAndExplicitToolBootstrap(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGateFile(t, root, ".gitattributes", "* text=auto eol=lf\n*.pb -text\n*.png -text\n")
	writeGateFile(t, root, ".github/workflows/ci.yml", `steps:
  - run: go run ./internal/qualitygate -mode=tools-bootstrap
  - run: go run ./internal/qualitygate -mode=verify
`)
	if err := checkRepositoryPortability(root); err != nil {
		t.Fatal(err)
	}

	writeGateFile(t, root, ".github/workflows/ci.yml", `steps:
  - run: go run ./internal/qualitygate -mode=verify
`)
	if err := checkRepositoryPortability(root); err == nil || !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("missing bootstrap error = %v", err)
	}
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

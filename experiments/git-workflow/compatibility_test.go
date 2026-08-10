package gitworkflow_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	gitworkflow "github.com/spice-framework/spice-agent/experiments/git-workflow"
)

type dependencyPin struct {
	Version  string `json:"version"`
	Sum      string `json:"sum"`
	GoModSum string `json:"go_mod_sum"`
}

type compatibilityManifest struct {
	Schema       int    `json:"schema"`
	Status       string `json:"status"`
	Module       string `json:"module"`
	Go           string `json:"go"`
	Toolchain    string `json:"toolchain"`
	Dependencies struct {
		Spice      dependencyPin `json:"spice"`
		SpiceAgent dependencyPin `json:"spice_agent"`
		XSys       dependencyPin `json:"x_sys"`
	} `json:"dependencies"`
	Contract struct {
		Version                   string   `json:"version"`
		Operations                []string `json:"operations"`
		Authority                 string   `json:"authority"`
		CommitScope               string   `json:"commit_scope"`
		TrustedExecutable         string   `json:"trusted_executable"`
		AtomicVerifiedChild       bool     `json:"atomic_verified_child"`
		NetworkOperations         bool     `json:"network_operations"`
		RepositoryMutationHelpers bool     `json:"repository_mutation_helpers"`
		Hooks                     bool     `json:"hooks"`
		Signing                   bool     `json:"signing"`
		Credentials               bool     `json:"credentials"`
		ArbitraryArguments        bool     `json:"arbitrary_arguments"`
		PostLaunchMutationFailure string   `json:"post_launch_mutation_failure"`
	} `json:"contract"`
	ReplaceDirectives bool   `json:"replace_directives"`
	Promotion         string `json:"promotion"`
	Deletion          string `json:"deletion"`
}

func TestCompatibilityManifestLocksReleasedExperimentalBoundary(t *testing.T) {
	encoded, err := os.ReadFile("compatibility.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var manifest compatibilityManifest
	if err = decoder.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("compatibility manifest has trailing content: %v", err)
	}
	if manifest.Schema != 1 || manifest.Status != "experimental" ||
		manifest.Module != "github.com/spice-framework/spice-agent/experiments/git-workflow" ||
		manifest.Go != "1.26.0" || manifest.Toolchain != "go1.26.5" {
		t.Fatalf("invalid experiment identity: %#v", manifest)
	}
	wantSpice := dependencyPin{
		Version: "v0.1.0-preview.2", Sum: "h1:5pYgTlUUzC/xZISetG/U6c1L/I3f8dUQSZhuo6YqxiA=",
		GoModSum: "h1:dBZV5UZcbY6pzhfGNtvAwQIJ8YsFna+jf1SAlmukJfk=",
	}
	wantAgent := dependencyPin{
		Version: "v0.1.0-preview.5", Sum: "h1:rGND9DYx3pssliD1tZQOvPDOZ5GVfQLDc7VJQI3HLOM=",
		GoModSum: "h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=",
	}
	wantXSys := dependencyPin{
		Version: "v0.47.0", Sum: "h1:o7XGOvZQCADBQQ4Y7VNq2dRWQR7JmOUW8Kxx4ZsNgWs=",
		GoModSum: "h1:4GL1E5IUh+htKOUEOaiffhrAeqysfVGipDYzABqnCmw=",
	}
	if manifest.Dependencies.Spice != wantSpice || manifest.Dependencies.SpiceAgent != wantAgent ||
		manifest.Dependencies.XSys != wantXSys {
		t.Fatalf("invalid dependency pins: %#v", manifest.Dependencies)
	}
	contract := manifest.Contract
	if contract.Version != gitworkflow.ContractVersion ||
		!slices.Equal(contract.Operations, []string{gitworkflow.InspectToolName, gitworkflow.CommitStagedToolName}) ||
		contract.Authority == "" || contract.CommitScope != "staged-index-only" ||
		contract.TrustedExecutable == "" || contract.AtomicVerifiedChild || contract.NetworkOperations ||
		contract.RepositoryMutationHelpers || contract.Hooks || contract.Signing || contract.Credentials ||
		contract.ArbitraryArguments || contract.PostLaunchMutationFailure != "execution-uncertain+retry-never" ||
		manifest.ReplaceDirectives || !strings.Contains(manifest.Promotion, "VerifiedLauncher") || manifest.Deletion == "" {
		t.Fatalf("invalid Git workflow boundary: %#v", manifest)
	}
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	text := string(goMod)
	if strings.Contains(text, "replace ") ||
		!strings.Contains(text, "github.com/spice-framework/spice v0.1.0-preview.2") ||
		!strings.Contains(text, "github.com/spice-framework/spice-agent v0.1.0-preview.5") ||
		!strings.Contains(text, "golang.org/x/sys v0.47.0") {
		t.Fatalf("go.mod violates released dependency boundary:\n%s", text)
	}
}

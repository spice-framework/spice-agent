package planning_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	planning "github.com/spice-framework/spice-agent/experiments/planning"
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
	} `json:"dependencies"`
	Contract struct {
		Version             string `json:"version"`
		Delivery            string `json:"delivery"`
		ReviewBoundary      string `json:"review_boundary"`
		MaximumSteps        int    `json:"maximum_steps"`
		MaximumDependencies int    `json:"maximum_dependencies"`
		MaximumTextBytes    int    `json:"maximum_text_bytes"`
		MaximumPlanBytes    int    `json:"maximum_plan_bytes"`
		CanonicalJSON       bool   `json:"canonical_json"`
		SHA256Identity      bool   `json:"sha256_identity"`
		SnapshotDurable     bool   `json:"snapshot_durable"`
		ResumeReplans       bool   `json:"resume_replans"`
		ToolAuthority       bool   `json:"tool_authority"`
		GuardAuthority      bool   `json:"guard_authority"`
		HiddenOperations    bool   `json:"hidden_operations"`
		ModelAssisted       bool   `json:"model_assisted"`
		DaemonIntegration   bool   `json:"daemon_integration"`
	} `json:"contract"`
	ReplaceDirectives bool   `json:"replace_directives"`
	Promotion         string `json:"promotion"`
	Deletion          string `json:"deletion"`
}

func TestCompatibilityManifestLocksReleasedPlanningBoundary(t *testing.T) {
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
		manifest.Module != "github.com/spice-framework/spice-agent/experiments/planning" ||
		manifest.Go != "1.26.0" || manifest.Toolchain != "go1.26.5" {
		t.Fatalf("invalid experiment identity: %#v", manifest)
	}
	if manifest.Dependencies.Spice != (dependencyPin{
		Version: "v0.1.0-preview.2", Sum: "h1:5pYgTlUUzC/xZISetG/U6c1L/I3f8dUQSZhuo6YqxiA=",
		GoModSum: "h1:dBZV5UZcbY6pzhfGNtvAwQIJ8YsFna+jf1SAlmukJfk=",
	}) || manifest.Dependencies.SpiceAgent != (dependencyPin{
		Version: "v0.1.0-preview.5", Sum: "h1:rGND9DYx3pssliD1tZQOvPDOZ5GVfQLDc7VJQI3HLOM=",
		GoModSum: "h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=",
	}) {
		t.Fatalf("invalid released dependency pins: %#v", manifest.Dependencies)
	}
	contract := manifest.Contract
	if contract.Version != planning.ContractVersion || contract.Delivery != "dedicated-initial-user-text-part" ||
		contract.ReviewBoundary != "prepare-then-start-prepared" || contract.MaximumSteps != planning.MaximumSteps ||
		contract.MaximumDependencies != planning.MaximumDependencies || contract.MaximumTextBytes != planning.MaximumTextBytes ||
		contract.MaximumPlanBytes != planning.MaximumPlanBytes || !contract.CanonicalJSON || !contract.SHA256Identity ||
		!contract.SnapshotDurable || contract.ResumeReplans || contract.ToolAuthority || contract.GuardAuthority ||
		contract.HiddenOperations || contract.ModelAssisted || contract.DaemonIntegration || manifest.ReplaceDirectives ||
		manifest.Promotion == "" || manifest.Deletion == "" {
		t.Fatalf("invalid planning boundary: %#v", manifest)
	}
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	text := string(goMod)
	if strings.Contains(text, "replace ") ||
		!strings.Contains(text, "github.com/spice-framework/spice v0.1.0-preview.2") ||
		!strings.Contains(text, "github.com/spice-framework/spice-agent v0.1.0-preview.5") {
		t.Fatalf("go.mod violates released dependency boundary:\n%s", text)
	}
}

func TestProductionSourceHasNoHiddenExecutionOrAuthority(t *testing.T) {
	forbidden := []string{
		"spice-agent/event", "spice-agent/interaction", "spice-agent/tool",
		"spice-agent/model", "spice-agent/process", "spice-agent/daemon",
		"spice-agent/plugin", "net/http", "os/exec", "RequestInteraction(", ".Dispatch(",
	}
	for _, path := range []string{"doc.go", "plan.go", "service.go"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range forbidden {
			if bytes.Contains(content, []byte(value)) {
				t.Fatalf("production source %s contains forbidden authority %q", path, value)
			}
		}
	}
}

func TestGeneratedSourceUsesDirectPlannerAndServiceCalls(t *testing.T) {
	providerSource, err := os.ReadFile(filepath.Join(
		"internal", "spicegen", "planningproof", "sources", "internal_", "composition", "proof_spice_gen.go",
	))
	if err != nil {
		t.Fatal(err)
	}
	text := string(providerSource)
	for _, required := range []string{
		"composition.NewProofPlanner()",
		"composition.NewProofEngine(",
		"composition.NewProofService(",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("generated source lacks %q", required)
		}
	}
	for _, forbidden := range []string{"reflect.", "map[string]any", "ServiceLocator", "RuntimeGraph"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("generated source contains %q", forbidden)
		}
	}
}

package permission_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type compatibilityManifest struct {
	Schema    int    `json:"schema"`
	Status    string `json:"status"`
	Module    string `json:"module"`
	Go        string `json:"go"`
	Toolchain string `json:"toolchain"`
	Core      struct {
		Module   string `json:"module"`
		Version  string `json:"version"`
		Sum      string `json:"sum"`
		GoModSum string `json:"go_mod_sum"`
	} `json:"core"`
	RequiredSeams     []string `json:"required_seams"`
	CompiledRegistry  bool     `json:"compiled_registry"`
	RuntimeNetwork    bool     `json:"runtime_network"`
	ReplaceDirectives bool     `json:"replace_directives"`
	Deletion          string   `json:"deletion"`
}

func TestCompatibilityManifestPinsReleasedCoreAndDeletionBoundary(t *testing.T) {
	t.Parallel()
	encoded, err := os.ReadFile("compatibility.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest compatibilityManifest
	if err = json.Unmarshal(encoded, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != 1 || manifest.Status != "experimental" ||
		manifest.Module != "github.com/spice-framework/spice-agent/experiments/permission" ||
		manifest.Go != "1.26.0" || manifest.Toolchain != "go1.26.5" {
		t.Fatalf("invalid experiment identity: %#v", manifest)
	}
	if manifest.Core.Module != "github.com/spice-framework/spice-agent" ||
		manifest.Core.Version != "v0.1.0-preview.5" ||
		manifest.Core.Sum != "h1:rGND9DYx3pssliD1tZQOvPDOZ5GVfQLDc7VJQI3HLOM=" ||
		manifest.Core.GoModSum != "h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=" {
		t.Fatalf("invalid released core pin: %#v", manifest.Core)
	}
	if len(manifest.RequiredSeams) != 2 || manifest.CompiledRegistry || manifest.RuntimeNetwork ||
		manifest.ReplaceDirectives || manifest.Deletion == "" {
		t.Fatalf("invalid experiment boundary: %#v", manifest)
	}
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(goMod), "replace ") || !strings.Contains(string(goMod), "github.com/spice-framework/spice-agent v0.1.0-preview.5") {
		t.Fatalf("go.mod violates released dependency boundary:\n%s", goMod)
	}
}

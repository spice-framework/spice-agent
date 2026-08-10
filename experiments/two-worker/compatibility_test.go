package twoworker_test

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
	Contracts struct {
		ClientSession  string `json:"client_session"`
		RunHost        string `json:"run_host"`
		EngineProtocol string `json:"engine_protocol"`
		LocalTransport string `json:"local_transport"`
	} `json:"contracts"`
	Tool struct {
		Name   string `json:"name"`
		Effect string `json:"effect"`
		Replay string `json:"replay"`
	} `json:"tool"`
	KernelHierarchy   bool   `json:"kernel_hierarchy"`
	Scheduler         bool   `json:"scheduler"`
	CompiledRegistry  bool   `json:"compiled_registry"`
	ProtocolChanges   bool   `json:"protocol_changes"`
	ReplaceDirectives bool   `json:"replace_directives"`
	Promotion         string `json:"promotion"`
	Deletion          string `json:"deletion"`
}

func TestCompatibilityManifestLocksReleasedPublicBoundary(t *testing.T) {
	encoded, err := os.ReadFile("compatibility.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest compatibilityManifest
	if err = json.Unmarshal(encoded, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != 1 || manifest.Status != "experimental" ||
		manifest.Module != "github.com/spice-framework/spice-agent/experiments/two-worker" ||
		manifest.Go != "1.26.0" || manifest.Toolchain != "go1.26.5" {
		t.Fatalf("invalid experiment identity: %#v", manifest)
	}
	if manifest.Core.Module != "github.com/spice-framework/spice-agent" ||
		manifest.Core.Version != "v0.1.0-preview.5" ||
		manifest.Core.Sum != "h1:rGND9DYx3pssliD1tZQOvPDOZ5GVfQLDc7VJQI3HLOM=" ||
		manifest.Core.GoModSum != "h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=" {
		t.Fatalf("invalid released core pin: %#v", manifest.Core)
	}
	if manifest.Contracts.ClientSession != "public preview.5" ||
		manifest.Contracts.RunHost != "public preview.5" ||
		manifest.Contracts.EngineProtocol != "1.3.0" || manifest.Contracts.LocalTransport == "" ||
		manifest.Tool.Name != "worker.delegate" || manifest.Tool.Effect != "mutating" ||
		manifest.Tool.Replay != "idempotent" || manifest.KernelHierarchy || manifest.Scheduler ||
		manifest.CompiledRegistry || manifest.ProtocolChanges || manifest.ReplaceDirectives ||
		manifest.Promotion == "" || manifest.Deletion == "" {
		t.Fatalf("invalid extension boundary: %#v", manifest)
	}
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(goMod), "replace ") ||
		!strings.Contains(string(goMod), "github.com/spice-framework/spice-agent v0.1.0-preview.5") ||
		!strings.Contains(string(goMod), "google.golang.org/grpc v1.83.0") {
		t.Fatalf("go.mod violates released dependency boundary:\n%s", goMod)
	}
}

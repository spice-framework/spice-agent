package sqliterecovery_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type compatibilityManifest struct {
	Schema        int    `json:"schema"`
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	Module        string `json:"module"`
	Go            string `json:"go"`
	Toolchain     string `json:"toolchain"`
	ApplicationID string `json:"application_id"`
	Core          struct {
		Module   string `json:"module"`
		Version  string `json:"version"`
		Sum      string `json:"sum"`
		GoModSum string `json:"go_mod_sum"`
	} `json:"core"`
	SQLite struct {
		Module   string `json:"module"`
		Version  string `json:"version"`
		Sum      string `json:"sum"`
		GoModSum string `json:"go_mod_sum"`
	} `json:"sqlite"`
	Contracts struct {
		Snapshot     string `json:"snapshot"`
		ToolStarted  string `json:"tool_started"`
		ToolTerminal string `json:"tool_terminal"`
	} `json:"contracts"`
	TransparentDaemonRestart bool   `json:"transparent_daemon_restart"`
	RuntimeNetwork           bool   `json:"runtime_network"`
	ReplaceDirectives        bool   `json:"replace_directives"`
	Deletion                 string `json:"deletion"`
}

func TestCompatibilityManifestPinsReleasedContractsAndDeletionBoundary(t *testing.T) {
	encoded, err := os.ReadFile("compatibility.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest compatibilityManifest
	if err = json.Unmarshal(encoded, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != 1 || manifest.SchemaVersion != 1 || manifest.Status != "experimental" ||
		manifest.Module != "github.com/spice-framework/spice-agent/experiments/sqlite-recovery" ||
		manifest.Go != "1.26.0" || manifest.Toolchain != "go1.26.5" || manifest.ApplicationID != "0x53504147" {
		t.Fatalf("invalid experiment identity: %#v", manifest)
	}
	if manifest.Core.Module != "github.com/spice-framework/spice-agent" || manifest.Core.Version != "v0.1.0-preview.5" ||
		manifest.Core.Sum != "h1:rGND9DYx3pssliD1tZQOvPDOZ5GVfQLDc7VJQI3HLOM=" || manifest.Core.GoModSum != "h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=" {
		t.Fatalf("invalid released core pin: %#v", manifest.Core)
	}
	if manifest.SQLite.Module != "github.com/ncruces/go-sqlite3" || manifest.SQLite.Version != "v0.35.3" ||
		manifest.SQLite.Sum != "h1:Ei07Zv1qfV/vyXzelhFsyS5Oh9TArBZHsmFk14Xv3GY=" || manifest.SQLite.GoModSum != "h1:i1rhym/NIiB5xeEfzbN+e24Y+i7NGUpf7C2xZ3Dpwks=" {
		t.Fatalf("invalid SQLite pin: %#v", manifest.SQLite)
	}
	if manifest.Contracts.Snapshot != "spice.agent.snapshot/v1alpha3" ||
		manifest.Contracts.ToolStarted != "spice.agent.tool-started/v1alpha1" ||
		manifest.Contracts.ToolTerminal != "spice.agent.tool-terminal/v1alpha1" ||
		manifest.TransparentDaemonRestart || manifest.RuntimeNetwork || manifest.ReplaceDirectives || manifest.Deletion == "" {
		t.Fatalf("invalid experiment boundary: %#v", manifest)
	}
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(goMod), "replace ") ||
		!strings.Contains(string(goMod), "github.com/spice-framework/spice-agent v0.1.0-preview.5") ||
		!strings.Contains(string(goMod), "github.com/ncruces/go-sqlite3 v0.35.3") {
		t.Fatalf("go.mod violates released dependency boundary:\n%s", goMod)
	}
}

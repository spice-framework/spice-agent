package compaction_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	compaction "github.com/spice-framework/spice-agent/experiments/compaction"
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
		Version                string `json:"version"`
		Mode                   string `json:"mode"`
		Selection              string `json:"selection"`
		ProviderWrapper        string `json:"provider_wrapper"`
		SemanticIdentity       string `json:"semantic_identity"`
		DurableHistoryModified bool   `json:"durable_history_modified"`
		EventsModified         bool   `json:"events_modified"`
		SnapshotsModified      bool   `json:"snapshots_modified"`
		HiddenModelCalls       bool   `json:"hidden_model_calls"`
		HiddenToolCalls        bool   `json:"hidden_tool_calls"`
		HiddenProcessCalls     bool   `json:"hidden_process_calls"`
		HiddenNetworkCalls     bool   `json:"hidden_network_calls"`
		HiddenInteractions     bool   `json:"hidden_interactions"`
	} `json:"contract"`
	ReplaceDirectives bool   `json:"replace_directives"`
	Promotion         string `json:"promotion"`
	Deletion          string `json:"deletion"`
}

func TestCompatibilityManifestLocksReleasedPublicBoundary(t *testing.T) {
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
		manifest.Module != "github.com/spice-framework/spice-agent/experiments/compaction" ||
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
	if contract.Version != compaction.ContractVersion || contract.Mode != "deterministic-local-extractive" ||
		contract.Selection != "contiguous-complete-tool-rounds" ||
		contract.ProviderWrapper != "application-owned-explicit" || contract.SemanticIdentity == "" ||
		contract.DurableHistoryModified || contract.EventsModified || contract.SnapshotsModified ||
		contract.HiddenModelCalls || contract.HiddenToolCalls || contract.HiddenProcessCalls ||
		contract.HiddenNetworkCalls || contract.HiddenInteractions || manifest.ReplaceDirectives ||
		manifest.Promotion == "" || manifest.Deletion == "" {
		t.Fatalf("invalid compaction boundary: %#v", manifest)
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

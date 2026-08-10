package telemetry_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/experiments/telemetry"
)

type dependencyPin struct {
	Version  string `json:"version"`
	Sum      string `json:"sum"`
	GoModSum string `json:"go_mod_sum"`
}

func TestRuntimeSourceHasNoNetworkOTelGlobalOrDurableAuthority(t *testing.T) {
	forbidden := []string{
		"go.opentelemetry.io/", "net/http", "net.Dial", "slog.SetDefault",
		"os.OpenFile", "event.Observer", "database/sql",
	}
	for _, path := range []string{"doc.go", "types.go", "processor.go", "health.go", filepath.Join("localjsonl", "jsonl.go")} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range forbidden {
			if bytes.Contains(content, []byte(value)) {
				t.Fatalf("runtime source %s contains forbidden authority %q", path, value)
			}
		}
	}
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
		Version                 string `json:"version"`
		Delivery                string `json:"delivery"`
		Queue                   string `json:"queue"`
		Consumers               int    `json:"consumers"`
		ExportRetry             bool   `json:"export_retry"`
		Authority               bool   `json:"authority"`
		Durable                 bool   `json:"durable"`
		RuntimeNetwork          bool   `json:"runtime_network"`
		RawEventData            bool   `json:"raw_event_data"`
		GlobalProvider          bool   `json:"global_provider"`
		OTelDependency          bool   `json:"otel_dependency"`
		TraceContextPropagation bool   `json:"trace_context_propagation"`
		TypedToolStarted        string `json:"typed_tool_started"`
		TypedToolTerminal       string `json:"typed_tool_terminal"`
		Correlation             string `json:"correlation"`
		ReadinessImpactDefault  bool   `json:"readiness_impact_default"`
	} `json:"contract"`
	ReplaceDirectives bool   `json:"replace_directives"`
	Promotion         string `json:"promotion"`
	Deletion          string `json:"deletion"`
}

func TestCompatibilityManifestLocksReleasedSafeProjection(t *testing.T) {
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
		manifest.Module != "github.com/spice-framework/spice-agent/experiments/telemetry" ||
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
	if contract.Version != telemetry.ContractVersion || contract.Delivery != "best-effort" ||
		contract.Queue != "agent-best-effort-mailbox" || contract.Consumers != 1 ||
		contract.ExportRetry || contract.Authority || contract.Durable || contract.RuntimeNetwork ||
		contract.RawEventData || contract.GlobalProvider || contract.OTelDependency ||
		contract.TraceContextPropagation || contract.ReadinessImpactDefault ||
		contract.TypedToolStarted != agent.ToolStartedOccurrenceVersion ||
		contract.TypedToolTerminal != agent.ToolTerminalOccurrenceVersion ||
		contract.Correlation != "process-local-hmac-sha256" || manifest.ReplaceDirectives ||
		manifest.Promotion == "" || manifest.Deletion == "" {
		t.Fatalf("invalid telemetry boundary: %#v", manifest)
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

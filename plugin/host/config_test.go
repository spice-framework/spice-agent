package pluginhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/process"
	"github.com/spice-framework/spice-agent/tool"
)

func TestParseSHA256Canonical(t *testing.T) {
	t.Parallel()
	const encoded = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	digest, err := ParseSHA256(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if digest.String() != encoded {
		t.Fatalf("digest = %q", digest.String())
	}

	for _, invalid := range []string{
		"", encoded[:63], encoded + "0", strings.ToUpper(encoded),
		strings.Repeat("z", 64),
	} {
		t.Run(fmt.Sprintf("length_%d", len(invalid)), func(t *testing.T) {
			t.Parallel()
			_, parseErr := ParseSHA256(invalid)
			if parseErr == nil {
				t.Fatal("expected rejection")
			}
			if strings.Contains(parseErr.Error(), invalid) && invalid != "" {
				t.Fatal("failure reflected rejected digest")
			}
		})
	}
}

func TestNewExecutableCopiesAndNormalizesInput(t *testing.T) {
	t.Parallel()
	config := validExecutableConfig(t)
	config.Environment = []string{"ZED=last", "ALPHA=first"}
	config.ApprovedCapabilities = []tool.Capability{
		tool.CapabilityNetworkAccess,
		tool.CapabilityFilesystemRead,
	}
	executable, err := NewExecutable(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Environment[0] = "SECRET=changed"
	config.ApprovedCapabilities[0] = tool.CapabilitySecretsRead
	config.RequestedLimits.MaxTools = 999

	if got := executable.Environment(); !slices.Equal(got, []string{"ALPHA=first", "ZED=last"}) {
		t.Fatalf("environment = %q", got)
	}
	if got := executable.ApprovedCapabilities(); !slices.Equal(got, []tool.Capability{
		tool.CapabilityFilesystemRead,
		tool.CapabilityNetworkAccess,
	}) {
		t.Fatalf("capabilities = %q", got)
	}
	if executable.RequestedLimits().GetMaxTools() != 8 {
		t.Fatal("limits alias caller input")
	}

	environment := executable.Environment()
	environment[0] = "SECRET=changed"
	capabilities := executable.ApprovedCapabilities()
	capabilities[0] = tool.CapabilitySecretsRead
	limits := executable.RequestedLimits()
	limits.MaxTools = 999
	clone := executable.Clone()
	cloneEnvironment := clone.Environment()
	cloneEnvironment[0] = "SECRET=changed"
	if executable.Environment()[0] != "ALPHA=first" ||
		executable.ApprovedCapabilities()[0] != tool.CapabilityFilesystemRead ||
		executable.RequestedLimits().GetMaxTools() != 8 ||
		clone.Environment()[0] != "ALPHA=first" {
		t.Fatal("immutable executable leaked mutable backing state")
	}
	if err := executable.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNewExecutablePreservesExplicitProcessCapability(t *testing.T) {
	t.Parallel()
	config := validExecutableConfig(t)
	config.ApprovedCapabilities = []tool.Capability{tool.CapabilityProcessExecute}
	executable, err := NewExecutable(config)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(executable.ApprovedCapabilities(), []tool.Capability{tool.CapabilityProcessExecute}) {
		t.Fatalf("capabilities = %q", executable.ApprovedCapabilities())
	}
}

func TestNewExecutableRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	valid := validExecutableConfig(t)
	tests := map[string]struct {
		change  func(*ExecutableConfig)
		field   string
		problem ConfigProblem
	}{
		"missing id":           {func(value *ExecutableConfig) { value.ID = "" }, "id", ProblemRequired},
		"uppercase id":         {func(value *ExecutableConfig) { value.ID = "Plugin" }, "id", ProblemMalformed},
		"long id":              {func(value *ExecutableConfig) { value.ID = "p" + strings.Repeat("x", maximumIdentityBytes) }, "id", ProblemTooLarge},
		"bad manifest name":    {func(value *ExecutableConfig) { value.ManifestName = " bad" }, "manifest_name", ProblemMalformed},
		"bad manifest version": {func(value *ExecutableConfig) { value.ManifestVersion = "bad\nvalue" }, "manifest_version", ProblemMalformed},
		"missing digest":       {func(value *ExecutableConfig) { value.SHA256 = SHA256{} }, "sha256", ProblemRequired},
		"relative executable":  {func(value *ExecutableConfig) { value.Path = "plugin" }, "executable", ProblemNotAbsolute},
		"noncanonical executable": {func(value *ExecutableConfig) {
			value.Path += string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(value.Path)
		}, "executable", ProblemNotCanonical},
		"relative workdir":      {func(value *ExecutableConfig) { value.WorkingDirectory = "." }, "working_directory", ProblemNotAbsolute},
		"malformed environment": {func(value *ExecutableConfig) { value.Environment = []string{"NO_SEPARATOR"} }, "environment", ProblemMalformed},
		"duplicate environment": {func(value *ExecutableConfig) { value.Environment = []string{"A=1", "a=2"} }, "environment", ProblemDuplicate},
		"unknown capability":    {func(value *ExecutableConfig) { value.ApprovedCapabilities = []tool.Capability{"private.secret"} }, "capabilities", ProblemMalformed},
		"duplicate capability": {func(value *ExecutableConfig) {
			value.ApprovedCapabilities = []tool.Capability{tool.CapabilityFilesystemRead, tool.CapabilityFilesystemRead}
		}, "capabilities", ProblemDuplicate},
		"missing limits":           {func(value *ExecutableConfig) { value.RequestedLimits = nil }, "requested_limits", ProblemRequired},
		"invalid limits":           {func(value *ExecutableConfig) { value.RequestedLimits.MaxTools = 0 }, "requested_limits", ProblemMalformed},
		"zero startup timeout":     {func(value *ExecutableConfig) { value.StartupTimeout = 0 }, "startup_timeout", ProblemOutOfRange},
		"negative call timeout":    {func(value *ExecutableConfig) { value.CallTimeout = -1 }, "call_timeout", ProblemOutOfRange},
		"oversized drain timeout":  {func(value *ExecutableConfig) { value.DrainTimeout = MaximumOperationTimeout + 1 }, "drain_timeout", ProblemOutOfRange},
		"zero shutdown timeout":    {func(value *ExecutableConfig) { value.ShutdownTimeout = 0 }, "shutdown_timeout", ProblemOutOfRange},
		"zero containment timeout": {func(value *ExecutableConfig) { value.ContainmentTimeout = 0 }, "containment_timeout", ProblemOutOfRange},
		"oversized aggregate process": {func(value *ExecutableConfig) {
			value.Environment = []string{"A=" + strings.Repeat("x", process.MaximumValueBytes+1)}
		}, "environment", ProblemTooLarge},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := cloneExecutableConfig(valid)
			test.change(&config)
			_, err := NewExecutable(config)
			var failure *ConfigError
			if !errors.As(err, &failure) {
				t.Fatalf("error = %T %v", err, err)
			}
			if failure.Field() != test.field || failure.Problem() != test.problem {
				t.Fatalf("failure = field %q, problem %q", failure.Field(), failure.Problem())
			}
		})
	}
}

func TestExecutableFormattingIsRedacted(t *testing.T) {
	t.Parallel()
	config := validExecutableConfig(t)
	config.Environment = []string{"VERY_PRIVATE_TOKEN=do-not-print"}
	executable, err := NewExecutable(config)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(executable)
	if err != nil {
		t.Fatal(err)
	}
	outputs := []string{
		executable.String(), executable.GoString(), string(encoded),
		fmt.Sprintf("%v", executable), fmt.Sprintf("%+v", executable),
		fmt.Sprintf("%#v", executable), fmt.Sprintf("%s", executable),
	}
	for _, output := range outputs {
		for _, private := range []string{
			config.Path, config.WorkingDirectory, config.Environment[0], config.SHA256.String(),
		} {
			if strings.Contains(output, private) {
				t.Fatalf("format %q contains private configuration", output)
			}
		}
	}
}

func validExecutableConfig(t *testing.T) ExecutableConfig {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "plugin")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	digest, err := ParseSHA256(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	return ExecutableConfig{
		ID: "fixture", ManifestName: "example.fixture", ManifestVersion: "v0.1.0",
		Path: path, SHA256: digest, WorkingDirectory: root,
		Environment:          []string{"SPICE_MODE=test"},
		ApprovedCapabilities: []tool.Capability{tool.CapabilityFilesystemRead},
		RequestedLimits:      validHostLimits(),
		StartupTimeout:       time.Second,
		CallTimeout:          2 * time.Second,
		DrainTimeout:         3 * time.Second,
		ShutdownTimeout:      4 * time.Second,
		ContainmentTimeout:   5 * time.Second,
	}
}

func cloneExecutableConfig(value ExecutableConfig) ExecutableConfig {
	value.Environment = slices.Clone(value.Environment)
	value.ApprovedCapabilities = slices.Clone(value.ApprovedCapabilities)
	value.RequestedLimits = cloneLimits(value.RequestedLimits)
	return value
}

func validHostLimits() *pluginv1.Limits {
	return &pluginv1.Limits{
		MaxMessageBytes: 1 << 20, MaxTools: 8, MaxSchemaBytes: 64 << 10,
		MaxCallArgumentBytes: 64 << 10, MaxResultBytes: 1 << 20,
		MaxProgressBytes: tool.MaximumProgressBytes, MaxConcurrentCalls: 4,
	}
}

func TestExecutableGetterInventory(t *testing.T) {
	t.Parallel()
	config := validExecutableConfig(t)
	executable, err := NewExecutable(config)
	if err != nil {
		t.Fatal(err)
	}
	got := []any{
		executable.ID(), executable.ManifestName(), executable.ManifestVersion(), executable.Path(),
		executable.SHA256(), executable.WorkingDirectory(), executable.StartupTimeout(),
		executable.CallTimeout(), executable.DrainTimeout(), executable.ShutdownTimeout(),
		executable.ContainmentTimeout(),
	}
	want := []any{
		config.ID, config.ManifestName, config.ManifestVersion, config.Path,
		config.SHA256, config.WorkingDirectory, config.StartupTimeout,
		config.CallTimeout, config.DrainTimeout, config.ShutdownTimeout,
		config.ContainmentTimeout,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("getters = %#v, want %#v", got, want)
	}
}

func TestExecutableConfigCannotSelectInterpreterArguments(t *testing.T) {
	t.Parallel()
	typeOfConfig := reflect.TypeFor[ExecutableConfig]()
	if _, present := typeOfConfig.FieldByName("Arguments"); present {
		t.Fatal("an executable digest must not authorize separate mutable arguments")
	}
}

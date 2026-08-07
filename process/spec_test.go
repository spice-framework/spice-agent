package process_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/process"
	"github.com/spice-framework/spice-agent/tool"
)

func TestSpecIsCanonicalImmutableAndRedacted(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	executable := filepath.Join(root, "secret-executable")
	arguments := []string{"--token", "secret-argument"}
	environment := []string{"TOKEN=secret-environment", "A=first"}
	capabilities := []tool.Capability{tool.CapabilityEnvironmentRead, tool.CapabilityProcessExecute}
	stdin := strings.NewReader("secret-input")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	spec, err := process.NewSpec(process.Config{
		Executable: executable, Arguments: arguments, WorkingDirectory: root,
		Environment: environment, Stdin: stdin, Stdout: stdout, Stderr: stderr,
		Capabilities: capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments[0] = "mutated"
	environment[0] = "MUTATED=yes"
	capabilities[0] = tool.CapabilityFilesystemWrite
	if err = spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if spec.Executable() != executable || spec.WorkingDirectory() != root ||
		!slices.Equal(spec.Arguments(), []string{"--token", "secret-argument"}) ||
		!slices.Equal(spec.Environment(), []string{"A=first", "TOKEN=secret-environment"}) ||
		!slices.Equal(spec.Capabilities(), []tool.Capability{
			tool.CapabilityEnvironmentRead, tool.CapabilityProcessExecute,
		}) || spec.Stdin() != stdin || spec.Stdout() != stdout || spec.Stderr() != stderr {
		t.Fatalf("unexpected specification: %#v", spec)
	}

	returned := spec.Arguments()
	returned[0] = "mutated-again"
	clone := spec.Clone()
	if !slices.Equal(spec.Arguments(), clone.Arguments()) {
		t.Fatal("specification or clone exposed backing storage")
	}
	serialized, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{
		fmt.Sprint(spec), fmt.Sprintf("%#v", spec), fmt.Sprintf("%+v", spec), string(serialized),
	} {
		for _, secret := range []string{"secret-executable", "secret-argument", "secret-environment", "secret-input"} {
			if strings.Contains(rendered, secret) {
				t.Fatalf("format leaked %q: %q", secret, rendered)
			}
		}
	}
}

func TestSpecRejectsInvalidInputsWithoutEchoingValues(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	valid := process.Config{
		Executable: filepath.Join(root, "program"), WorkingDirectory: root,
		Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
		Capabilities: []tool.Capability{tool.CapabilityProcessExecute},
	}
	tests := map[string]struct {
		mutate  func(*process.Config)
		field   string
		problem process.SpecProblem
	}{
		"missing executable":  {func(config *process.Config) { config.Executable = "" }, "executable", process.ProblemRequired},
		"relative executable": {func(config *process.Config) { config.Executable = "private-program" }, "executable", process.ProblemNotAbsolute},
		"unclean executable": {func(config *process.Config) {
			separator := string(os.PathSeparator)
			config.Executable = root + separator + "child" + separator + ".." + separator + "private-program"
		}, "executable", process.ProblemNotCanonical},
		"relative cwd":               {func(config *process.Config) { config.WorkingDirectory = "private-directory" }, "working_directory", process.ProblemNotAbsolute},
		"nil stdin":                  {func(config *process.Config) { config.Stdin = nil }, "stdin", process.ProblemRequired},
		"nil stdout":                 {func(config *process.Config) { config.Stdout = nil }, "stdout", process.ProblemRequired},
		"nil stderr":                 {func(config *process.Config) { config.Stderr = nil }, "stderr", process.ProblemRequired},
		"argument nul":               {func(config *process.Config) { config.Arguments = []string{"private\x00argument"} }, "arguments", process.ProblemContainsNUL},
		"argument invalid utf8":      {func(config *process.Config) { config.Arguments = []string{string([]byte{0xff})} }, "arguments", process.ProblemInvalidUTF8},
		"malformed environment":      {func(config *process.Config) { config.Environment = []string{"PRIVATE"} }, "environment", process.ProblemMalformed},
		"duplicate environment":      {func(config *process.Config) { config.Environment = []string{"Private=1", "PRIVATE=2"} }, "environment", process.ProblemDuplicate},
		"missing execute capability": {func(config *process.Config) { config.Capabilities = []tool.Capability{tool.CapabilityFilesystemRead} }, "capabilities", process.ProblemMissingCapability},
		"malformed capability": {func(config *process.Config) {
			config.Capabilities = []tool.Capability{tool.CapabilityProcessExecute, "Private.Secret"}
		}, "capabilities", process.ProblemMalformed},
		"duplicate capability": {func(config *process.Config) {
			config.Capabilities = []tool.Capability{tool.CapabilityProcessExecute, tool.CapabilityProcessExecute}
		}, "capabilities", process.ProblemDuplicate},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := valid
			test.mutate(&config)
			_, err := process.NewSpec(config)
			var specErr *process.SpecError
			if !errors.As(err, &specErr) || specErr.Field() != test.field || specErr.Problem() != test.problem {
				t.Fatalf("error = %T %v", err, err)
			}
			if strings.Contains(strings.ToLower(err.Error()), "private") {
				t.Fatalf("error leaked value: %v", err)
			}
		})
	}
}

func TestSpecBounds(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	base := process.Config{
		Executable: filepath.Join(root, "program"), WorkingDirectory: root,
		Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
		Capabilities: []tool.Capability{tool.CapabilityProcessExecute},
	}
	tests := []struct {
		name   string
		mutate func(*process.Config)
	}{
		{"arguments", func(config *process.Config) { config.Arguments = make([]string, process.MaximumArguments+1) }},
		{"environment", func(config *process.Config) { config.Environment = make([]string, process.MaximumEnvironment+1) }},
		{"capabilities", func(config *process.Config) {
			config.Capabilities = make([]tool.Capability, process.MaximumCapabilities+1)
		}},
		{"single value", func(config *process.Config) {
			config.Arguments = []string{strings.Repeat("x", process.MaximumValueBytes+1)}
		}},
		{"total", func(config *process.Config) {
			config.Arguments = []string{
				strings.Repeat("a", process.MaximumValueBytes), strings.Repeat("b", process.MaximumValueBytes),
				strings.Repeat("c", process.MaximumValueBytes), strings.Repeat("d", process.MaximumValueBytes),
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := base
			test.mutate(&config)
			if _, err := process.NewSpec(config); err == nil {
				t.Fatal("oversized specification succeeded")
			}
		})
	}
}

func TestZeroSpecIsInvalid(t *testing.T) {
	t.Parallel()
	if err := (process.Spec{}).Validate(); err == nil {
		t.Fatal("zero specification validated")
	}
}

func TestConfigDoesNotExposeMutableImplementationState(t *testing.T) {
	t.Parallel()
	configType := reflect.TypeFor[process.Config]()
	for _, name := range []string{"Arguments", "Environment", "Capabilities"} {
		field, found := configType.FieldByName(name)
		if !found || field.Type.Kind() != reflect.Slice {
			t.Fatalf("configuration field %s shape changed", name)
		}
	}
}

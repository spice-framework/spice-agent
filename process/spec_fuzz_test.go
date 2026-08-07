package process_test

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/spice-framework/spice-agent/process"
	"github.com/spice-framework/spice-agent/tool"
)

func FuzzSpecValidation(f *testing.F) {
	root := filepath.Clean(f.TempDir())
	f.Add(filepath.Join(root, "program"), root, "argument", "NAME=value", "process.execute")
	f.Add("relative", root, "\x00", "MALFORMED", "Private.Value")
	f.Fuzz(func(t *testing.T, executable, cwd, argument, environment, capability string) {
		spec, err := process.NewSpec(process.Config{
			Executable: executable, Arguments: []string{argument}, WorkingDirectory: cwd,
			Environment: []string{environment}, Stdin: emptyReader{}, Stdout: io.Discard, Stderr: io.Discard,
			Capabilities: []tool.Capability{tool.CapabilityProcessExecute, tool.Capability(capability)},
		})
		if err != nil {
			return
		}
		if err = spec.Validate(); err != nil {
			t.Fatalf("accepted specification failed validation: %v", err)
		}
	})
}

func FuzzExitedOutcome(f *testing.F) {
	f.Add(int64(0))
	f.Add(process.MaximumExitCode)
	f.Add(int64(-1))
	f.Fuzz(func(t *testing.T, code int64) {
		outcome, err := process.NewExitedOutcome(code)
		if err != nil {
			return
		}
		if err = outcome.Validate(); err != nil {
			t.Fatalf("accepted outcome failed validation: %v", err)
		}
		got, ok := outcome.ExitCode()
		if !ok || got != code || outcome.Successful() != (code == 0) {
			t.Fatalf("outcome round trip = %d, %t, %t", got, ok, outcome.Successful())
		}
	})
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

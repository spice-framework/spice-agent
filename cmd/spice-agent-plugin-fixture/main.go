// Command spice-agent-plugin-fixture is the independent Go implementation of
// the public plugin/v1 conformance profile. It is not a production plugin host.
package main

import (
	"fmt"
	"os"

	"github.com/spice-framework/spice-agent/internal/pluginfixture"
)

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr)) //nolint:forbidigo // This is the command process boundary.
}

func run(input *os.File, output, errorOutput *os.File) int {
	if err := pluginfixture.Serve(input, output); err != nil {
		if _, writeErr := fmt.Fprintln(errorOutput, "spice-agent-plugin-fixture:", err); writeErr != nil {
			return 2
		}
		return 1
	}
	return 0
}

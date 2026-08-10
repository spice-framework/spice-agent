package process_test

import (
	"strings"
	"testing"

	agentprocess "github.com/spice-framework/spice-agent/process"
)

func FuzzSHA256(fuzz *testing.F) {
	fuzz.Add(strings.Repeat("01", 32))
	fuzz.Add(strings.Repeat("0", 64))
	fuzz.Add("")
	fuzz.Add(strings.Repeat("A1", 32))
	fuzz.Fuzz(func(t *testing.T, value string) {
		digest, err := agentprocess.ParseSHA256(value)
		if err != nil {
			return
		}
		if len(value) != 64 || value != strings.ToLower(value) || digest.String() != value {
			t.Fatalf("accepted noncanonical digest %q", value)
		}
		if validateErr := digest.Validate(); validateErr != nil && value != strings.Repeat("0", 64) {
			t.Fatalf("nonzero parsed digest failed validation: %v", validateErr)
		}
	})
}

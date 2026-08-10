package process_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	agentprocess "github.com/spice-framework/spice-agent/process"
)

func TestSHA256CanonicalValidation(t *testing.T) {
	t.Parallel()
	encoded := strings.Repeat("01", 32)
	digest, err := agentprocess.ParseSHA256(encoded)
	if err != nil || digest.String() != encoded || digest.Validate() != nil {
		t.Fatalf("digest = %q, validation = %v, parse = %v", digest.String(), digest.Validate(), err)
	}
	for _, invalid := range []string{"", strings.Repeat("A1", 32), strings.Repeat("0", 63), strings.Repeat("z", 64)} {
		_, parseErr := agentprocess.ParseSHA256(invalid)
		var failure *agentprocess.DigestError
		if !errors.As(parseErr, &failure) || failure.Problem() != agentprocess.DigestProblemMalformed {
			t.Fatalf("ParseSHA256(%q) = %T %v", invalid, parseErr, parseErr)
		}
		encodedFailure, marshalErr := json.Marshal(failure)
		if marshalErr != nil || (invalid != "" && strings.Contains(string(encodedFailure), invalid)) {
			t.Fatalf("digest failure JSON = %q, %v", encodedFailure, marshalErr)
		}
	}
	zero, err := agentprocess.ParseSHA256(strings.Repeat("0", 64))
	var zeroFailure *agentprocess.DigestError
	if err != nil || !errors.As(zero.Validate(), &zeroFailure) ||
		zeroFailure.Problem() != agentprocess.DigestProblemZero {
		t.Fatalf("zero digest = %v, parse = %v", zeroFailure, err)
	}
	var absent *agentprocess.DigestError
	if absent.Problem() != "" || !strings.Contains(absent.Error(), "invalid") ||
		!strings.Contains(fmt.Sprintf("%+v", absent), "invalid") {
		t.Fatal("nil digest failure was not safe")
	}
}

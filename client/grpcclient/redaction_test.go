package grpcclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/daemon/endpoint"
)

func TestConnectorAndSessionFormattingRedactsEndpointCredential(t *testing.T) {
	t.Parallel()
	token, err := endpoint.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	canary, err := token.AuthorizationValue()
	if err != nil {
		t.Fatal(err)
	}
	values := []any{&Connector{token: token}, &session{token: token}}
	for _, value := range values {
		for _, formatted := range []string{
			fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value),
			fmt.Sprintf("%s", value), fmt.Sprintf("%q", value), fmt.Sprintf("%x", value),
		} {
			assertRedacted(t, canary, formatted)
		}
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		assertRedacted(t, canary, string(encoded))
		var output bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&output, nil))
		logger.Info("adapter", "value", value)
		assertRedacted(t, canary, output.String())
	}
}

func assertRedacted(t *testing.T, canary, value string) {
	t.Helper()
	if strings.Contains(value, canary) || strings.Contains(value, strings.TrimPrefix(canary, endpoint.BearerPrefix)) {
		t.Fatalf("credential leaked in %q", value)
	}
}

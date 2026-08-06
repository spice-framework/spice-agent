package interaction_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/interaction"
)

func TestRequestAndResponseAreImmutable(t *testing.T) {
	scope, err := interaction.NewScope("run-1")
	if err != nil || scope.RunID() != "run-1" || scope.Validate() != nil {
		t.Fatalf("scope = %#v, %v", scope, err)
	}
	schema := json.RawMessage(`{"type":"boolean"}`)
	request, err := interaction.NewRequest("i1", "confirm", "Continue?", schema)
	if err != nil {
		t.Fatal(err)
	}
	schema[2] = 'X'
	copySchema := request.Schema()
	copySchema[2] = 'X'
	if request.ID() != "i1" || request.Kind() != "confirm" || request.Prompt() != "Continue?" || !json.Valid(request.Schema()) || request.Clone().Validate() != nil {
		t.Fatal("request contract mismatch")
	}
	value := json.RawMessage(`true`)
	response, err := interaction.NewResponse(request.ID(), value)
	if err != nil {
		t.Fatal(err)
	}
	value[0] = 'X'
	copyValue := response.Value()
	copyValue[0] = 'X'
	if response.ID() != request.ID() || !json.Valid(response.Value()) || response.Clone().Validate() != nil {
		t.Fatal("response contract mismatch")
	}
}

func TestRequestAndResponseRejectInvalidValues(t *testing.T) {
	invalidRequests := []func() error{
		func() error {
			_, err := interaction.NewRequest("", "confirm", "Continue?", json.RawMessage(`{}`))
			return err
		},
		func() error {
			_, err := interaction.NewRequest("i", "", "Continue?", json.RawMessage(`{}`))
			return err
		},
		func() error { _, err := interaction.NewRequest("i", "confirm", " ", json.RawMessage(`{}`)); return err },
		func() error {
			_, err := interaction.NewRequest("i", "confirm", "Continue?", json.RawMessage(`{`))
			return err
		},
		func() error {
			_, err := interaction.NewRequest("i", "confirm", strings.Repeat("x", interaction.MaximumPayloadBytes+1), json.RawMessage(`{}`))
			return err
		},
	}
	for index, check := range invalidRequests {
		if err := check(); err == nil {
			t.Fatalf("invalid request %d succeeded", index)
		}
	}
	if _, err := interaction.NewResponse("bad ", json.RawMessage(`true`)); err == nil {
		t.Fatal("invalid response ID succeeded")
	}
	if _, err := interaction.NewResponse("i", json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid response JSON succeeded")
	}
	large := json.RawMessage(`"` + strings.Repeat("x", interaction.MaximumPayloadBytes) + `"`)
	if _, err := interaction.NewResponse("i", large); err == nil {
		t.Fatal("oversized response succeeded")
	}
	if (interaction.Request{}).Validate() == nil || (interaction.Response{}).Validate() == nil {
		t.Fatal("zero interaction value succeeded")
	}
	if _, err := interaction.NewScope(""); err == nil || (interaction.Scope{}).Validate() == nil {
		t.Fatal("invalid interaction scope succeeded")
	}
}

func TestUnavailableBrokerFailsClosed(t *testing.T) {
	request, _ := interaction.NewRequest("i", "confirm", "Continue?", json.RawMessage(`{}`))
	scope, _ := interaction.NewScope("run")
	if _, err := (interaction.UnavailableBroker{}).Request(t.Context(), scope, request); err == nil {
		t.Fatal("unavailable broker succeeded")
	}
}

package interaction_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/interaction"
)

func TestRequestAndResponseValidation(t *testing.T) {
	request := interaction.Request{ID: "i1", Kind: "confirm", Prompt: "Continue?", Schema: json.RawMessage(`{"type":"boolean"}`)}
	response := interaction.Response{ID: request.ID, Value: json.RawMessage(`true`)}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := response.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []interaction.Request{{}, {ID: "i", Kind: "k", Prompt: "p", Schema: json.RawMessage(`{`)}} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid request %+v succeeded", invalid)
		}
	}
	for _, invalid := range []interaction.Request{
		{ID: "i", Kind: "", Prompt: "p", Schema: json.RawMessage(`{}`)},
		{ID: "i", Kind: "k", Prompt: " ", Schema: json.RawMessage(`{}`)},
		{ID: "i", Kind: "k", Prompt: "p", Schema: json.RawMessage(`"` + strings.Repeat("x", 1<<20) + `"`)},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid request %+v succeeded", invalid)
		}
	}
	if err := (interaction.Response{ID: "i", Value: json.RawMessage(`{`)}).Validate(); err == nil {
		t.Fatal("invalid response succeeded")
	}
	if err := (interaction.Response{ID: " bad", Value: json.RawMessage(`true`)}).Validate(); err == nil {
		t.Fatal("invalid response ID succeeded")
	}
	large := json.RawMessage(`"` + strings.Repeat("x", 1<<20) + `"`)
	if err := (interaction.Response{ID: "i", Value: large}).Validate(); err == nil {
		t.Fatal("oversized response succeeded")
	}
}

package spiceagent_test

import (
	"strings"
	"testing"

	spiceagent "github.com/spice-framework/spice-agent"
	"github.com/spice-framework/spice/starter"
)

func TestManifestIsCanonicalAndCompatible(t *testing.T) {
	spec := spiceagent.Manifest.Spec()
	if spec.ID != "github.com/spice-framework/spice-agent" || spec.Activation.Mode != starter.ActivationExplicitConstructor {
		t.Fatalf("manifest = %+v", spec)
	}
	if err := spiceagent.Manifest.Compatible(starter.APIVersion, "go1.26.5"); err != nil {
		t.Fatal(err)
	}
	content, err := spiceagent.Manifest.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(content), "\n") {
		t.Fatal("manifest JSON lacks final newline")
	}
}

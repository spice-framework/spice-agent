package spiceagent_test

import (
	"os"
	"strings"
	"testing"
)

func TestModulePinsAndAuthorizesCompositionTools(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	moduleFile := string(content)
	for _, expected := range []string{
		"go 1.26.0\n",
		"toolchain go1.26.5\n",
		"github.com/spice-framework/spice-agent/cmd/spice-agent-annotations",
		"github.com/spice-framework/toolchain/cmd/spice\n",
		"github.com/spice-framework/toolchain/cmd/spice-annotation-core",
	} {
		if !strings.Contains(moduleFile, expected) {
			t.Fatalf("go.mod does not contain exact contract %q", expected)
		}
	}
	for modulePath, wantVersion := range map[string]string{
		"github.com/spice-framework/spice":     "v0.1.0-preview.1.0.20260806200749-524424a04df0",
		"github.com/spice-framework/toolchain": "v0.1.0-preview.1.0.20260806203056-d0b9ac086bd6",
		"google.golang.org/grpc":               "v1.83.0",
		"google.golang.org/protobuf":           "v1.36.11",
	} {
		if gotVersion := requiredVersion(moduleFile, modulePath); gotVersion != wantVersion {
			t.Fatalf("%s version = %q, want %q", modulePath, gotVersion, wantVersion)
		}
	}
	if strings.Contains(moduleFile, "\nreplace ") || strings.Contains(moduleFile, "\nreplace (") {
		t.Fatal("go.mod contains a local replacement")
	}
}

func TestProtocolToolsAreExactLocalToolDependencies(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("tools/go.mod")
	if err != nil {
		t.Fatal(err)
	}
	moduleFile := string(content)
	for modulePath, wantVersion := range map[string]string{
		"github.com/bufbuild/buf":                       "v1.72.0",
		"google.golang.org/grpc/cmd/protoc-gen-go-grpc": "v1.6.2",
		"google.golang.org/protobuf":                    "v1.36.11",
	} {
		if gotVersion := requiredVersion(moduleFile, modulePath); gotVersion != wantVersion {
			t.Fatalf("tools %s version = %q, want %q", modulePath, gotVersion, wantVersion)
		}
	}
	for _, toolPath := range []string{
		"github.com/bufbuild/buf/cmd/buf",
		"google.golang.org/grpc/cmd/protoc-gen-go-grpc",
		"google.golang.org/protobuf/cmd/protoc-gen-go",
	} {
		if !strings.Contains(moduleFile, "\t"+toolPath+"\n") {
			t.Fatalf("tools/go.mod does not authorize %s", toolPath)
		}
	}
}

func requiredVersion(moduleFile, modulePath string) string {
	for line := range strings.Lines(moduleFile) {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == modulePath && strings.HasPrefix(fields[1], "v") {
			return fields[1]
		}
	}
	return ""
}

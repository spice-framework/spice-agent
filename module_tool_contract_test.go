package spiceagent_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	publicSpiceVersion = "v0.1.0-preview.4"
	staleSpiceVersion  = "v0.1.0-preview.2.0.20260811041952-0e79bc4f3b29"
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
		"github.com/spice-framework/spice":     publicSpiceVersion,
		"github.com/spice-framework/toolchain": "v0.1.0-preview.2",
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
	if strings.Contains(moduleFile, staleSpiceVersion) {
		t.Fatalf("go.mod retains superseded Spice pseudo-version %q", staleSpiceVersion)
	}
}

func TestPublicSpiceSelectionIsExactAndVendoredBytesStayFrozen(t *testing.T) {
	t.Parallel()
	for _, contract := range []struct {
		path     string
		required []string
	}{
		{
			path: "go.sum",
			required: []string{
				"github.com/spice-framework/spice " + publicSpiceVersion + " h1:jfUSUquq9rQN/FMI6zvBEJZYX12ZVeJAQmusvjy/3T8=\n",
				"github.com/spice-framework/spice " + publicSpiceVersion + "/go.mod h1:dBZV5UZcbY6pzhfGNtvAwQIJ8YsFna+jf1SAlmukJfk=\n",
			},
		},
		{
			path: "vendor/modules.txt",
			required: []string{
				"# github.com/spice-framework/spice " + publicSpiceVersion + "\n",
				"github.com/spice-framework/spice/logging\n",
			},
		},
	} {
		content, err := os.ReadFile(contract.path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)
		for _, required := range contract.required {
			if !strings.Contains(text, required) {
				t.Errorf("%s does not contain exact public Spice contract %q", contract.path, required)
			}
		}
		if strings.Contains(text, staleSpiceVersion) {
			t.Errorf("%s retains superseded Spice pseudo-version %q", contract.path, staleSpiceVersion)
		}
	}

	const wantVendorDigest = "da0b7caf67a22cdca7d149d5048d5ac2a389d6d4115599baead724344aa3bb39"
	if got := treeDigest(t, "vendor/github.com/spice-framework/spice"); got != wantVendorDigest {
		t.Fatalf("vendored Spice bytes digest = %s, want %s", got, wantVendorDigest)
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

func treeDigest(t *testing.T, root string) string {
	t.Helper()
	digest := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, _ = digest.Write([]byte(filepath.ToSlash(relative)))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(content)
		_, _ = digest.Write([]byte{0})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}

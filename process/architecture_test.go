package process_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProcessContractDependencyDirection(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Clean(entry.Name())
		content, readErr := os.ReadFile(path) // #nosec G304 -- path is a package-local directory entry.
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, forbidden := range [][]byte{
			[]byte("os/exec"), []byte("reflect"), []byte("*exec.Cmd"),
			[]byte("Runtime" + "Graph"), []byte("Service" + "Locator"), []byte("Registry"),
		} {
			if bytes.Contains(content, forbidden) {
				t.Fatalf("%s contains forbidden process mechanism %q", path, forbidden)
			}
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, content, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, imported := range file.Imports {
			name, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				t.Fatal(unquoteErr)
			}
			if strings.HasPrefix(name, "github.com/spice-framework/spice-agent/") &&
				name != "github.com/spice-framework/spice-agent/tool" {
				t.Fatalf("%s imports forbidden core package %q", path, name)
			}
		}
	}
}

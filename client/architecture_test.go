package client_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPublicClientSourceImportsOnlyTheStandardLibrary(t *testing.T) {
	t.Parallel()
	standard := standardLibraryPackages(t)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		for _, imported := range parsed.Imports {
			assertStandardLibraryImport(t, standard, path, imported)
		}
	}
}

func standardLibraryPackages(t *testing.T) map[string]struct{} {
	t.Helper()
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("locate current Go executable: %v", err)
	}
	command := exec.Command(goExecutable, "list", "std")
	command.Env = append(os.Environ(), "GOPROXY=off", "GOWORK=off", "GOTOOLCHAIN=local")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("list exact standard-library packages: %v: %s", err, output)
	}
	result := make(map[string]struct{})
	for packagePath := range strings.FieldsSeq(string(output)) {
		result[packagePath] = struct{}{}
	}
	if len(result) == 0 {
		t.Fatal("go list std returned no packages")
	}
	return result
}

func assertStandardLibraryImport(
	t *testing.T,
	standard map[string]struct{},
	path string,
	imported *ast.ImportSpec,
) {
	t.Helper()
	value, err := strconv.Unquote(imported.Path.Value)
	if err != nil {
		t.Fatalf("unquote import in %s: %v", path, err)
	}
	if _, found := standard[value]; !found {
		t.Errorf("%s imports non-standard public-client dependency %q", path, value)
	}
}

package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestEachDescriptorAndRealHandlerShareOneDocumentedFile(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		file       string
		descriptor string
		handler    string
	}{
		{"model_provider.go", "ModelProvider", "ModelProviderHandler"},
		{"stage.go", "Stage", "StageHandler"},
		{"tool.go", "Tool", "ToolHandler"},
	} {
		t.Run(test.descriptor, func(t *testing.T) {
			t.Parallel()
			content, err := os.ReadFile(test.file) // #nosec G304 -- fixed repository-owned test path.
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), test.file, content, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			functions := make(map[string]*ast.FuncDecl)
			for _, declaration := range parsed.Decls {
				if function, ok := declaration.(*ast.FuncDecl); ok {
					functions[function.Name.Name] = function
				}
			}
			for _, name := range []string{test.descriptor, test.handler} {
				function := functions[name]
				if function == nil || function.Doc == nil || len(strings.Fields(function.Doc.Text())) < 12 {
					t.Fatalf("%s is missing rich adjacent GoDoc", name)
				}
			}
			text := string(content)
			for _, required := range []string{"direct", "Spice", "provider", "metadata"} {
				if !strings.Contains(strings.ToLower(text), strings.ToLower(required)) {
					t.Fatalf("%s documentation omits %q", test.file, required)
				}
			}
		})
	}
}

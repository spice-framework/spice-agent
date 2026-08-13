package main

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

func checkDocumentationContract(root string, publicPackages []string) error {
	required := map[string][]string{
		"README.md": {
			"docs/authoring/extensions.md", "docs/authoring/protocols.md",
			"docs/examples.md", "docs/threat-model.md",
		},
		"docs/authoring/extensions.md": {
			"compiled-tool-autoconfigure/v1alpha1-preview6", "spice-agent-tool-text",
			"spice-agent-tool-json", "spice-agent-tool-integer", "install", "delete",
		},
		"docs/authoring/protocols.md": {
			"client/conformance", "plugin/conformance", "compatibility/released-generation.json",
			"proto/spice/agent", "schema-baseline", "docs/migrations",
		},
		"docs/examples.md": {
			"spice-agent-tool-text", "spice-agent-tool-json", "spice-agent-tool-integer",
			"client/conformance", "plugin/conformance",
		},
		"docs/threat-model.md": {
			"Assets and actors", "Trust boundaries and abuse cases", "Residual risk",
			"compatibility/security-process.json", "compatibility/security-exceptions.json",
		},
		"SECURITY.md": {
			"Supported versions", "private", "security advisory", "three calendar days",
			"seven", "coordinated disclosure", "Dependency updates",
		},
		"docs/dependencies.md": {
			"Update process", "within one day", "every 30 days", "govulncheck",
			"vendor reproducibility", "compatibility/security-process.json",
		},
	}
	for relative, fragments := range required {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative))) // #nosec G304 -- closed repository path set above.
		if err != nil {
			return fmt.Errorf("read documentation contract %s: %w", relative, err)
		}
		if err = validateRequiredDocument(relative, string(content), fragments); err != nil {
			return err
		}
	}
	for _, relative := range []string{
		"proto/spice/agent/common/v1/common.proto",
		"proto/spice/agent/engine/v1/engine.proto",
		"proto/spice/agent/plugin/v1/plugin.proto",
		"schema-baseline/buf.yaml",
		"docs/migrations/v0.1.0-preview.4-to-v0.1.0-preview.5.md",
		"docs/migrations/v0.1.0-preview.5-to-v0.1.0-preview.6.md",
		"client/conformance/doc.go",
		"plugin/conformance/doc.go",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			return fmt.Errorf("documentation contract requires %s: %w", relative, err)
		}
	}
	return checkPublicPackageDocumentation(root, publicPackages)
}

func validateRequiredDocument(relative, content string, required []string) error {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	for _, fragment := range required {
		if strings.Count(content, fragment) == 0 {
			return fmt.Errorf("%s must contain %q", relative, fragment)
		}
	}
	return nil
}

func checkPublicPackageDocumentation(root string, publicPackages []string) error {
	for _, importPath := range publicPackages {
		relative := strings.TrimPrefix(importPath, modulePath)
		relative = strings.TrimPrefix(relative, "/")
		directory := filepath.Join(root, filepath.FromSlash(relative))
		entries, err := os.ReadDir(directory)
		if err != nil {
			return fmt.Errorf("read public package %s: %w", importPath, err)
		}
		found := false
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			file, parseErr := parser.ParseFile(
				token.NewFileSet(), filepath.Join(directory, entry.Name()), nil, parser.PackageClauseOnly|parser.ParseComments,
			)
			if parseErr != nil {
				return fmt.Errorf("parse public package documentation %s: %w", importPath, parseErr)
			}
			if file.Doc != nil && strings.HasPrefix(file.Doc.Text(), "Package "+file.Name.Name+" ") {
				found = true
				break
			}
		}
		if !found {
			return errors.New("public package " + importPath + " lacks a canonical Package comment")
		}
	}
	return nil
}

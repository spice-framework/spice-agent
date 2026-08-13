package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const goAPICompatibilityPath = "compatibility/go-api.json"

type goAPICompatibility struct {
	Schema         string               `json:"schema"`
	Module         string               `json:"module"`
	Status         string               `json:"status"`
	Baseline       goAPIRelease         `json:"baseline"`
	PublicPackages []string             `json:"packages"`
	Platforms      []goAPIPlatform      `json:"platforms"`
	ApprovedBreaks []goAPIApprovedBreak `json:"approved_breaks"`
	V1Stable       bool                 `json:"v1_stable"`
}

type goAPIRelease struct {
	Release string `json:"release"`
	Commit  string `json:"commit"`
	Go      string `json:"go_version"`
}

type goAPIPlatform struct {
	GOOS                      string `json:"goos"`
	GOARCH                    string `json:"goarch"`
	BaselinePackageSHA256     string `json:"baseline_package_sha256"`
	BaselineDeclarationSHA256 string `json:"baseline_declaration_sha256"`
	CurrentPackageSHA256      string `json:"current_package_sha256"`
	CurrentDeclarationSHA256  string `json:"current_declaration_sha256"`
}

type goAPIApprovedBreak struct {
	ID         string `json:"id"`
	Transition string `json:"transition"`
	Package    string `json:"package"`
	Symbol     string `json:"symbol"`
	Kind       string `json:"kind"`
	Migration  string `json:"migration"`
}

type goAPIDigest struct {
	GOOS              string `json:"goos"`
	GOARCH            string `json:"goarch"`
	PackageSHA256     string `json:"package_sha256"`
	DeclarationSHA256 string `json:"declaration_sha256"`
}

type listedPackage struct {
	Dir        string
	ImportPath string
	GoFiles    []string
	CgoFiles   []string
}

func renderCurrentAPIBaseline(ctx context.Context, root string) error {
	digests, packages, err := currentAPIDigests(ctx, root)
	if err != nil {
		return err
	}
	value := struct {
		Packages  []string      `json:"packages"`
		Platforms []goAPIDigest `json:"platforms"`
	}{Packages: packages, Platforms: digests}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Go API baseline: %w", err)
	}
	_, err = fmt.Fprintf(os.Stdout, "%s\n", content)
	return err
}

func checkGoAPISurface(ctx context.Context, root string) error {
	manifest, _, err := readCanonicalJSON[goAPICompatibility](root, goAPICompatibilityPath)
	if err != nil {
		return err
	}
	if validationErr := validateGoAPICompatibility(manifest, root); validationErr != nil {
		return validationErr
	}
	digests, packages, err := currentAPIDigests(ctx, root)
	if err != nil {
		return err
	}
	if !slices.Equal(packages, manifest.PublicPackages) {
		return fmt.Errorf("public Go package inventory differs from %s", goAPICompatibilityPath)
	}
	for index, platform := range manifest.Platforms {
		actual := digests[index]
		if platform.GOOS != actual.GOOS || platform.GOARCH != actual.GOARCH ||
			platform.CurrentPackageSHA256 != actual.PackageSHA256 ||
			platform.CurrentDeclarationSHA256 != actual.DeclarationSHA256 {
			return fmt.Errorf("public Go API digest differs for %s/%s; review the change and record an approved migration", platform.GOOS, platform.GOARCH)
		}
	}
	return nil
}

func currentAPIDigests(ctx context.Context, root string) ([]goAPIDigest, []string, error) {
	targets := [][2]string{{"darwin", "arm64"}, {"linux", "amd64"}, {"windows", "amd64"}}
	var inventory []string
	digests := make([]goAPIDigest, 0, len(targets))
	for _, target := range targets {
		packages, err := listPlatformPackages(ctx, root, target[0], target[1])
		if err != nil {
			return nil, nil, err
		}
		paths := make([]string, 0, len(packages))
		for _, pkg := range packages {
			paths = append(paths, pkg.ImportPath)
		}
		if inventory == nil {
			inventory = paths
		} else if !slices.Equal(inventory, paths) {
			return nil, nil, fmt.Errorf("public package inventory differs between platforms")
		}
		packageDigest := framedSHA256(paths)
		declarations, err := canonicalExportedDeclarations(packages)
		if err != nil {
			return nil, nil, fmt.Errorf("canonicalize %s/%s public Go API: %w", target[0], target[1], err)
		}
		digests = append(digests, goAPIDigest{
			GOOS: target[0], GOARCH: target[1], PackageSHA256: packageDigest,
			DeclarationSHA256: framedSHA256(declarations),
		})
	}
	return digests, inventory, nil
}

func listPlatformPackages(ctx context.Context, root, goos, goarch string) ([]listedPackage, error) {
	environment := map[string]string{
		"CGO_ENABLED": "0", "GOARCH": goarch, "GOFLAGS": "-mod=vendor", "GOOS": goos,
		"GOPROXY": "off", "GOTOOLCHAIN": "local", "GOWORK": "off",
	}
	output, err := capture(ctx, root, environment, "go", "list", "-json", "./...")
	if err != nil {
		return nil, fmt.Errorf("list %s/%s packages: %w", goos, goarch, err)
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	var packages []listedPackage
	for {
		var pkg listedPackage
		err := decoder.Decode(&pkg)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode %s/%s package inventory: %w", goos, goarch, err)
		}
		if !isPublicModulePackage(pkg.ImportPath) {
			continue
		}
		packages = append(packages, pkg)
	}
	slices.SortFunc(packages, func(left, right listedPackage) int { return strings.Compare(left.ImportPath, right.ImportPath) })
	return packages, nil
}

func isPublicModulePackage(path string) bool {
	return (path == modulePath || strings.HasPrefix(path, modulePath+"/")) &&
		!strings.Contains(path, "/internal/") && !strings.Contains(path, "/cmd/") &&
		!strings.HasSuffix(path, "/internal") && !strings.HasSuffix(path, "/cmd")
}

func canonicalExportedDeclarations(packages []listedPackage) ([]string, error) {
	var records []string
	for _, pkg := range packages {
		files := append(slices.Clone(pkg.GoFiles), pkg.CgoFiles...)
		slices.Sort(files)
		for _, name := range files {
			path := filepath.Join(pkg.Dir, name)
			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
			imports := canonicalImports(file)
			for _, declaration := range file.Decls {
				for _, exported := range exportedDeclarationCopies(declaration) {
					var formatted bytes.Buffer
					if err := format.Node(&formatted, fileSet, exported); err != nil {
						return nil, fmt.Errorf("format exported declaration in %s: %w", path, err)
					}
					records = append(records, pkg.ImportPath+"\n"+imports+"\n"+formatted.String())
				}
			}
		}
	}
	slices.Sort(records)
	return records, nil
}

func canonicalImports(file *ast.File) string {
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		name := ""
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports = append(imports, name+"="+spec.Path.Value)
	}
	slices.Sort(imports)
	return strings.Join(imports, ",")
}

func exportedDeclarationCopies(declaration ast.Decl) []ast.Decl {
	switch value := declaration.(type) {
	case *ast.FuncDecl:
		if value.Name == nil || !value.Name.IsExported() {
			return nil
		}
		cloned := *value
		cloned.Doc = nil
		cloned.Body = nil
		return []ast.Decl{&cloned}
	case *ast.GenDecl:
		var result []ast.Decl
		for _, specification := range value.Specs {
			if !specificationExportsName(specification) {
				continue
			}
			cloned := *value
			cloned.Doc = nil
			cloned.Lparen = token.NoPos
			cloned.Rparen = token.NoPos
			cloned.Specs = []ast.Spec{specification}
			result = append(result, &cloned)
		}
		return result
	default:
		return nil
	}
}

func specificationExportsName(specification ast.Spec) bool {
	switch value := specification.(type) {
	case *ast.TypeSpec:
		return value.Name.IsExported()
	case *ast.ValueSpec:
		return slices.ContainsFunc(value.Names, func(name *ast.Ident) bool { return name.IsExported() })
	default:
		return false
	}
}

func framedSHA256(values []string) string {
	hash := sha256.New()
	for _, value := range values {
		_, _ = fmt.Fprintf(hash, "%d:%s\n", len(value), value)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func validateGoAPICompatibility(value goAPICompatibility, root string) error {
	if value.Schema != "spice.agent.go-api.compatibility/v1alpha1" || value.Module != modulePath ||
		value.Status != "pre-v1-reviewed-not-stable" || value.V1Stable {
		return errors.New("go API compatibility manifest status differs from the reviewed pre-v1 contract")
	}
	if value.Baseline != (goAPIRelease{Release: "v0.1.0-preview.5", Commit: "3e8fe6406171a7e7f1765311a4fa7fc3b878e425", Go: requiredGoVersion}) {
		return errors.New("go API compatibility baseline differs from preview5")
	}
	if len(value.PublicPackages) != 29 || !sortedUnique(value.PublicPackages) ||
		!slices.Contains(value.PublicPackages, modulePath+"/client/conformance") {
		return errors.New("go API compatibility manifest must contain the sorted exact 29-package inventory")
	}
	if err := validateGoAPIPlatforms(value.Platforms); err != nil {
		return err
	}
	return validateGoAPIBreaks(value.ApprovedBreaks, root)
}

func validateGoAPIPlatforms(platforms []goAPIPlatform) error {
	wantTargets := [][2]string{{"darwin", "arm64"}, {"linux", "amd64"}, {"windows", "amd64"}}
	wantBaselinePackages := "4911815649911c1ef13cc4a6f00b5cbbef03cdd93388824c66b1646d10abe9c0"
	wantBaselineDeclarations := []string{
		"e70ab391059d657839a3722ac9d700853d6e432c3776f17231ee04de36e712e8",
		"e70ab391059d657839a3722ac9d700853d6e432c3776f17231ee04de36e712e8",
		"a2b46e29ad086be6e6d48c235b0c6fab27a9480b64ac33de8a3f91fe122a4a72",
	}
	if len(platforms) != len(wantTargets) {
		return errors.New("go API compatibility manifest must contain all reviewed platforms")
	}
	for index, target := range wantTargets {
		platform := platforms[index]
		if platform.GOOS != target[0] || platform.GOARCH != target[1] {
			return errors.New("go API compatibility platforms are not canonical")
		}
		if platform.BaselinePackageSHA256 != wantBaselinePackages || platform.BaselineDeclarationSHA256 != wantBaselineDeclarations[index] {
			return fmt.Errorf("go API preview5 baseline digest differs for %s/%s", platform.GOOS, platform.GOARCH)
		}
		for _, digest := range []string{platform.BaselinePackageSHA256, platform.BaselineDeclarationSHA256, platform.CurrentPackageSHA256, platform.CurrentDeclarationSHA256} {
			decoded, err := hex.DecodeString(digest)
			if err != nil || len(decoded) != sha256.Size || strings.Trim(digest, "0") == "" {
				return fmt.Errorf("invalid Go API digest for %s/%s", platform.GOOS, platform.GOARCH)
			}
		}
	}
	return nil
}

func validateGoAPIBreaks(approvedBreaks []goAPIApprovedBreak, root string) error {
	wantBreaks := []goAPIApprovedBreak{
		{ID: "SPICE-AGENT-GO-0001", Transition: "v0.1.0-preview.4-to-v0.1.0-preview.5", Package: modulePath + "/stage", Symbol: "ToolDispatcher.Dispatch", Kind: "interface-signature", Migration: "docs/migrations/v0.1.0-preview.4-to-v0.1.0-preview.5.md"},
		{ID: "SPICE-AGENT-GO-0002", Transition: "v0.1.0-preview.4-to-v0.1.0-preview.5", Package: modulePath + "/agent", Symbol: "NewPlanIdentity", Kind: "function-signature", Migration: "docs/migrations/v0.1.0-preview.4-to-v0.1.0-preview.5.md"},
		{ID: "SPICE-AGENT-GO-0003", Transition: "v0.1.0-preview.4-to-v0.1.0-preview.5", Package: modulePath + "/plugin/host/autoconfigure", Symbol: "DefaultHost", Kind: "function-signature", Migration: "docs/migrations/v0.1.0-preview.4-to-v0.1.0-preview.5.md"},
		{ID: "SPICE-AGENT-GO-0004", Transition: "v0.1.0-preview.4-to-v0.1.0-preview.5", Package: modulePath + "/agent", Symbol: "EngineOptions", Kind: "exported-struct-shape", Migration: "docs/migrations/v0.1.0-preview.4-to-v0.1.0-preview.5.md"},
		{ID: "SPICE-AGENT-GO-0005", Transition: "v0.1.0-preview.5-to-v0.1.0-preview.6", Package: modulePath + "/plugin/host", Symbol: "HostConfig.Processes", Kind: "exported-field-type", Migration: "docs/migrations/v0.1.0-preview.5-to-v0.1.0-preview.6.md"},
		{ID: "SPICE-AGENT-GO-0006", Transition: "v0.1.0-preview.5-to-v0.1.0-preview.6", Package: modulePath + "/plugin/host/autoconfigure", Symbol: "DefaultHost", Kind: "function-signature", Migration: "docs/migrations/v0.1.0-preview.5-to-v0.1.0-preview.6.md"},
	}
	if len(approvedBreaks) < len(wantBreaks) || !slices.Equal(approvedBreaks[:len(wantBreaks)], wantBreaks) {
		return errors.New("go API compatibility manifest must retain all reviewed pre-v1 breaks")
	}
	for index, approved := range approvedBreaks {
		if approved.ID != fmt.Sprintf("SPICE-AGENT-GO-%04d", index+1) || approved.Transition == "" ||
			approved.Package == "" || approved.Symbol == "" || approved.Kind == "" || approved.Migration == "" {
			return errors.New("go API approved breaks must be complete and append-only")
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(approved.Migration))); err != nil {
			return fmt.Errorf("go API migration %s: %w", approved.Migration, err)
		}
	}
	return nil
}

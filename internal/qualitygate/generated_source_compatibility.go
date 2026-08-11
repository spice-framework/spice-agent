package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	generatorModulePath = "github.com/spice-framework/toolchain"
	generatorVersion    = "v0.1.0-preview.2"
	generatorSum        = "h1:Hv/Ur+Uc3cG00jVCo/R5zINZ1w33jH0O6/ekeNOrFyk="
	generatorGoModSum   = "h1:LZ04RGO793x7rSetV5T8xZnGvXjbI8u6WCyzdwN2wOI="
)

type generatedSourceCompatibility struct {
	Schema    string                   `json:"schema"`
	Module    string                   `json:"module"`
	Status    string                   `json:"status"`
	Generator generatedSourceGenerator `json:"generator"`
	Contract  generatedSourceContract  `json:"contract"`
	Targets   []generatedSourceTarget  `json:"targets"`
	CleanRoom generatedSourceCleanRoom `json:"clean_room"`
	Proven    bool                     `json:"proven"`
}

type generatedSourceGenerator struct {
	Module       string `json:"module"`
	Version      string `json:"version"`
	Sum          string `json:"sum"`
	GoModSum     string `json:"go_mod_sum"`
	SourceCommit string `json:"source_commit"`
}

type generatedSourceContract struct {
	ManifestSchema       int    `json:"manifest_schema"`
	GoFormatLine         string `json:"go_format_line"`
	AnalysisBuildTag     string `json:"analysis_build_tag"`
	AcceptedInputSchemas []int  `json:"accepted_input_schemas"`
	GeneratedOwnership   string `json:"generated_ownership"`
	ManualEditPolicy     string `json:"manual_edit_policy"`
	StaleFilePolicy      string `json:"stale_file_policy"`
	PathPolicy           string `json:"path_policy"`
	Determinism          string `json:"determinism"`
}

type generatedSourceTarget struct {
	Module     string `json:"module"`
	ModuleRoot string `json:"module_root"`
	Target     string `json:"target"`
	Manifest   string `json:"manifest"`
}

type generatedSourceCleanRoom struct {
	Manifest            string `json:"manifest"`
	RequiredExtensions  int    `json:"required_extensions"`
	ExercisedExtensions int    `json:"exercised_extensions"`
	Exercised           bool   `json:"exercised"`
}

type generatedOwnershipIdentity struct {
	Schema int `json:"schema"`
	Target struct {
		ID           string `json:"id"`
		Module       string `json:"module"`
		ManifestPath string `json:"manifest_path"`
	} `json:"target"`
	GeneratorVersion string `json:"generator_version"`
	GoFormatLine     string `json:"go_format_line"`
}

func checkGeneratedSourceCompatibility(root string) error {
	value, _, err := readCanonicalJSON[generatedSourceCompatibility](root, generatedSourceCompatibilityPath)
	if err != nil {
		return err
	}
	return validateGeneratedSourceCompatibility(root, value)
}

func validateGeneratedSourceCompatibility(root string, value generatedSourceCompatibility) error {
	want := expectedGeneratedSourceCompatibility()
	if value.Schema != want.Schema || value.Module != want.Module || value.Status != want.Status ||
		value.Generator != want.Generator || !equalGeneratedSourceContract(value.Contract, want.Contract) ||
		!slices.Equal(value.Targets, want.Targets) || value.CleanRoom != want.CleanRoom || !value.Proven {
		return errors.New("generated source compatibility manifest differs from the reviewed immutable contract")
	}
	for _, target := range value.Targets {
		if err := validateGeneratedSourceTarget(root, target); err != nil {
			return err
		}
	}
	return nil
}

func equalGeneratedSourceContract(left, right generatedSourceContract) bool {
	return left.ManifestSchema == right.ManifestSchema && left.GoFormatLine == right.GoFormatLine &&
		left.AnalysisBuildTag == right.AnalysisBuildTag && slices.Equal(left.AcceptedInputSchemas, right.AcceptedInputSchemas) &&
		left.GeneratedOwnership == right.GeneratedOwnership && left.ManualEditPolicy == right.ManualEditPolicy &&
		left.StaleFilePolicy == right.StaleFilePolicy && left.PathPolicy == right.PathPolicy && left.Determinism == right.Determinism
}

func validateGeneratedSourceTarget(root string, target generatedSourceTarget) error {
	if target.ModuleRoot == "" || target.Manifest == "" || strings.Contains(target.ModuleRoot, "\\") ||
		strings.Contains(target.Manifest, "\\") || filepath.Clean(filepath.FromSlash(target.ModuleRoot)) != filepath.FromSlash(target.ModuleRoot) ||
		filepath.Clean(filepath.FromSlash(target.Manifest)) != filepath.FromSlash(target.Manifest) {
		return fmt.Errorf("generated source target %q has an unsafe path", target.Target)
	}
	if err := validateGeneratedModuleSelection(root, target.ModuleRoot); err != nil {
		return fmt.Errorf("generated source target %s module selection: %w", target.Target, err)
	}
	identity, err := readGeneratedOwnershipIdentity(root, target.Manifest)
	if err != nil {
		return err
	}
	wantManifest := target.Manifest
	if target.ModuleRoot != "." {
		wantManifest = strings.TrimPrefix(target.Manifest, target.ModuleRoot+"/")
	}
	if identity.Schema != 6 || identity.GeneratorVersion != generatorVersion || identity.GoFormatLine != "1.26" ||
		identity.Target.ID != target.Target || identity.Target.Module != target.Module || identity.Target.ManifestPath != wantManifest {
		return fmt.Errorf("generated source target %s ownership identity differs from the frozen contract", target.Target)
	}
	return nil
}

func validateGeneratedModuleSelection(root, moduleRoot string) error {
	goMod, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(moduleRoot), "go.mod")) // #nosec G304 -- moduleRoot is a reviewed manifest value.
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	wantRequirement := generatorModulePath + " " + generatorVersion
	if bytes.Count(goMod, []byte(wantRequirement)) != 1 {
		return errors.New("go.mod does not select the frozen generator exactly once")
	}
	goSum, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(moduleRoot), "go.sum")) // #nosec G304 -- moduleRoot is a reviewed manifest value.
	if err != nil {
		return fmt.Errorf("read go.sum: %w", err)
	}
	wantSum := generatorModulePath + " " + generatorVersion + " " + generatorSum
	wantModSum := generatorModulePath + " " + generatorVersion + "/go.mod " + generatorGoModSum
	if bytes.Count(goSum, []byte(wantSum)) != 1 || bytes.Count(goSum, []byte(wantModSum)) != 1 {
		return errors.New("go.sum does not contain the exact frozen generator sums")
	}
	modules, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(moduleRoot), "vendor", "modules.txt")) // #nosec G304 -- moduleRoot is a reviewed manifest value.
	if err != nil {
		return fmt.Errorf("read vendor/modules.txt: %w", err)
	}
	if bytes.Count(modules, []byte("# "+wantRequirement)) != 1 {
		return errors.New("vendor/modules.txt does not select the frozen generator exactly once")
	}
	return nil
}

func readGeneratedOwnershipIdentity(root, relative string) (generatedOwnershipIdentity, error) {
	var value generatedOwnershipIdentity
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative))) // #nosec G304 -- relative is a reviewed manifest value.
	if err != nil {
		return value, fmt.Errorf("read %s: %w", relative, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err = decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode %s: %w", relative, err)
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return value, fmt.Errorf("decode %s trailing data", relative)
	}
	return value, nil
}

func expectedGeneratedSourceCompatibility() generatedSourceCompatibility {
	return generatedSourceCompatibility{
		Schema: "spice.agent.generated-source.compatibility/v1alpha1", Module: modulePath,
		Status: "immutable-released-migrated-clean-room-proven",
		Generator: generatedSourceGenerator{
			Module: generatorModulePath, Version: generatorVersion, Sum: generatorSum, GoModSum: generatorGoModSum,
			SourceCommit: "bab8bcaf7d0c6311237b34812c681c3ee6a6593b",
		},
		Contract: generatedSourceContract{
			ManifestSchema: 6, GoFormatLine: "1.26", AnalysisBuildTag: "spice_generate",
			AcceptedInputSchemas: []int{1, 2, 3, 4, 5, 6}, GeneratedOwnership: "manifest-only",
			ManualEditPolicy: "reject", StaleFilePolicy: "remove-only-when-owned-hash-matches",
			PathPolicy: "module-relative-forward-slash-case-fold-unique", Determinism: "same-input-same-bytes",
		},
		Targets: []generatedSourceTarget{
			{Module: modulePath, ModuleRoot: ".", Target: "compositionproof", Manifest: ".spice/compositionproof.manifest.json"},
			{Module: modulePath + "/experiments/compaction", ModuleRoot: "experiments/compaction", Target: "compactionproof", Manifest: "experiments/compaction/.spice/compactionproof.manifest.json"},
			{Module: modulePath + "/experiments/git-workflow", ModuleRoot: "experiments/git-workflow", Target: "gitworkflowproof", Manifest: "experiments/git-workflow/.spice/gitworkflowproof.manifest.json"},
			{Module: modulePath + "/experiments/permission", ModuleRoot: "experiments/permission", Target: "permissionproof", Manifest: "experiments/permission/.spice/permissionproof.manifest.json"},
			{Module: modulePath + "/experiments/planning", ModuleRoot: "experiments/planning", Target: "planningproof", Manifest: "experiments/planning/.spice/planningproof.manifest.json"},
			{Module: modulePath + "/experiments/sqlite-recovery", ModuleRoot: "experiments/sqlite-recovery", Target: "sqliterecoveryproof", Manifest: "experiments/sqlite-recovery/.spice/sqliterecoveryproof.manifest.json"},
			{Module: modulePath + "/experiments/two-worker", ModuleRoot: "experiments/two-worker", Target: "twoworkerproof", Manifest: "experiments/two-worker/.spice/twoworkerproof.manifest.json"},
		},
		CleanRoom: generatedSourceCleanRoom{
			Manifest: publicAuthoringCompatibilityPath, RequiredExtensions: 3, ExercisedExtensions: 3, Exercised: true,
		},
		Proven: true,
	}
}

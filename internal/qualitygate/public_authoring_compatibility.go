package main

import (
	"errors"
	"slices"
)

type publicAuthoringCompatibility struct {
	Schema                     string                    `json:"schema"`
	Module                     string                    `json:"module"`
	Status                     string                    `json:"status"`
	ProofModel                 string                    `json:"proof_model"`
	RequiredExtensions         int                       `json:"required_extensions"`
	SeparatelyVersionedModules bool                      `json:"separately_versioned_modules"`
	GeneratedCompositionProof  bool                      `json:"generated_composition_proof"`
	GeneratedSource            publicAuthoringGenerator  `json:"generated_source"`
	Isolation                  publicAuthoringIsolation  `json:"isolation"`
	Platforms                  []string                  `json:"platforms"`
	VendorOffline              bool                      `json:"vendor_offline"`
	RequiredOperations         []string                  `json:"required_operations"`
	Evidence                   []publicAuthoringEvidence `json:"evidence"`
	Proven                     bool                      `json:"proven"`
}

type publicAuthoringGenerator struct {
	Module         string `json:"module"`
	Version        string `json:"version"`
	ManifestSchema int    `json:"manifest_schema"`
}

type publicAuthoringIsolation struct {
	ReleasedArtifactsOnly   bool   `json:"released_artifacts_only"`
	PublicDocumentationOnly bool   `json:"public_documentation_only"`
	FreshModuleCache        bool   `json:"fresh_module_cache"`
	FreshBuildCache         bool   `json:"fresh_build_cache"`
	GOWork                  string `json:"gowork"`
	ReplaceDirectives       bool   `json:"replace_directives"`
	ExcludeDirectives       bool   `json:"exclude_directives"`
	RetractDirectives       bool   `json:"retract_directives"`
	PrivateImports          bool   `json:"private_imports"`
	WorkspacePaths          bool   `json:"workspace_paths"`
}

type publicAuthoringEvidence struct {
	Module          string   `json:"module"`
	Version         string   `json:"version"`
	Commit          string   `json:"commit"`
	Profile         string   `json:"profile"`
	Platforms       []string `json:"platforms"`
	VerificationRun string   `json:"verification_run"`
}

func checkPublicAuthoringCompatibility(root string) error {
	value, _, err := readCanonicalJSON[publicAuthoringCompatibility](root, publicAuthoringCompatibilityPath)
	if err != nil {
		return err
	}
	return validatePublicAuthoringCompatibility(value)
}

func validatePublicAuthoringCompatibility(value publicAuthoringCompatibility) error {
	wantIsolation := publicAuthoringIsolation{
		ReleasedArtifactsOnly: true, PublicDocumentationOnly: true, FreshModuleCache: true,
		FreshBuildCache: true, GOWork: "off",
	}
	wantGenerator := publicAuthoringGenerator{Module: generatorModulePath, Version: generatorVersion, ManifestSchema: 6}
	if value.Schema != "spice.agent.public-authoring.compatibility/v1alpha1" || value.Module != modulePath ||
		value.Status != "required-not-proven" || value.ProofModel != "clean-room-released-artifacts-only" ||
		value.RequiredExtensions != 3 || !value.SeparatelyVersionedModules || !value.GeneratedCompositionProof ||
		value.GeneratedSource != wantGenerator ||
		value.Isolation != wantIsolation || !slices.Equal(value.Platforms, []string{"linux/amd64", "windows/amd64"}) ||
		!value.VendorOffline || !slices.Equal(value.RequiredOperations, []string{"install", "configure", "debug", "test", "package", "delete"}) ||
		len(value.Evidence) != 0 || value.Proven {
		return errors.New("public authoring compatibility manifest differs from the reviewed clean-room contract")
	}
	return nil
}

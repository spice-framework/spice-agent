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
	Module                  string   `json:"module"`
	Version                 string   `json:"version"`
	Commit                  string   `json:"commit"`
	TagObject               string   `json:"tag_object"`
	Profile                 string   `json:"profile"`
	ModuleSum               string   `json:"module_sum"`
	GoModSum                string   `json:"go_mod_sum"`
	Proxy                   string   `json:"proxy"`
	SumDB                   string   `json:"sumdb"`
	GeneratedManifestSchema int      `json:"generated_manifest_schema"`
	Platforms               []string `json:"platforms"`
	VendorOffline           bool     `json:"vendor_offline"`
	Operations              []string `json:"operations"`
	VerificationRun         string   `json:"verification_run"`
	Release                 string   `json:"release"`
}

func checkPublicAuthoringCompatibility(root string) error {
	value, _, err := readCanonicalJSON[publicAuthoringCompatibility](root, publicAuthoringCompatibilityPath)
	if err != nil {
		return err
	}
	return validatePublicAuthoringCompatibility(value)
}

func validatePublicAuthoringCompatibility(value publicAuthoringCompatibility) error {
	wantOperations := []string{"install", "configure", "debug", "test", "package", "delete"}
	wantIsolation := publicAuthoringIsolation{
		ReleasedArtifactsOnly: true, PublicDocumentationOnly: true, FreshModuleCache: true,
		FreshBuildCache: true, GOWork: "off",
	}
	wantGenerator := publicAuthoringGenerator{Module: generatorModulePath, Version: generatorVersion, ManifestSchema: 6}
	wantEvidence := []publicAuthoringEvidence{
		{
			Module: "github.com/spice-framework/spice-agent-tool-text", Version: "v0.1.0-preview.1",
			Commit: "cbc738067e9f67efd273509481488ba5eadfe1bd", TagObject: "36539996097937196711433a1e501d299b8fbe9f",
			Profile:   "compiled-tool-autoconfigure/v1alpha1-preview6",
			ModuleSum: "h1:e9qhtkySbuL/47k/dx9S9U0Y17MhI42p0pcEBrruPF4=", GoModSum: "h1:OEMtMFjM4RDQ7dbPqjTdIEGuvJIQsWPvq2bug66tT6M=",
			Proxy: "https://proxy.golang.org", SumDB: "sum.golang.org", GeneratedManifestSchema: 6,
			Platforms: slices.Clone(value.Platforms), VendorOffline: true, Operations: slices.Clone(wantOperations),
			VerificationRun: "https://github.com/spice-framework/spice-agent-tool-text/actions/runs/31442525658",
			Release:         "https://github.com/spice-framework/spice-agent-tool-text/releases/tag/v0.1.0-preview.1",
		},
		{
			Module: "github.com/spice-framework/spice-agent-tool-json", Version: "v0.1.0-preview.1",
			Commit: "78bd51b100ca697d369f594c7f78c0d0b8c2b817", TagObject: "639fb4fd1253f21c69ecbde00c2d9d12803ceab2",
			Profile:   "compiled-tool-autoconfigure/v1alpha1-preview6",
			ModuleSum: "h1:F5ngOkwjx2AhGAbPQGxGCh+OxZy6YveU3q3Ec7q2gw4=", GoModSum: "h1:Li2OFEzHQY46FVWJU3dXqdxHD12yRnlsp2UBrHgBqKE=",
			Proxy: "https://proxy.golang.org", SumDB: "sum.golang.org", GeneratedManifestSchema: 6,
			Platforms: slices.Clone(value.Platforms), VendorOffline: true, Operations: slices.Clone(wantOperations),
			VerificationRun: "https://github.com/spice-framework/spice-agent-tool-json/actions/runs/31446318737",
			Release:         "https://github.com/spice-framework/spice-agent-tool-json/releases/tag/v0.1.0-preview.1",
		},
		{
			Module: "github.com/spice-framework/spice-agent-tool-integer", Version: "v0.1.0-preview.1",
			Commit: "64c17aaea6cfd22cdb054d9faa0457f1181cfdae", TagObject: "d5e31ede0c8229437862760b30cdeb1421272c04",
			Profile:   "compiled-tool-autoconfigure/v1alpha1-preview6",
			ModuleSum: "h1:4Rj8Q4ZE/lgTj1SRpj5RP0YmzDsE4VoLv2zdQshRa8Y=", GoModSum: "h1:zvQ0tmoLuDyRI0uqQlr0iuJrbP/bDBKI49YMOLODsJ0=",
			Proxy: "https://proxy.golang.org", SumDB: "sum.golang.org", GeneratedManifestSchema: 6,
			Platforms: slices.Clone(value.Platforms), VendorOffline: true, Operations: slices.Clone(wantOperations),
			VerificationRun: "https://github.com/spice-framework/spice-agent-tool-integer/actions/runs/31448284962",
			Release:         "https://github.com/spice-framework/spice-agent-tool-integer/releases/tag/v0.1.0-preview.1",
		},
	}
	if value.Schema != "spice.agent.public-authoring.compatibility/v1alpha1" || value.Module != modulePath ||
		value.Status != "sdk-beta-and-phase8-proven" || value.ProofModel != "clean-room-released-artifacts-only" ||
		value.RequiredExtensions != 3 || !value.SeparatelyVersionedModules || !value.GeneratedCompositionProof ||
		value.GeneratedSource != wantGenerator ||
		value.Isolation != wantIsolation || !slices.Equal(value.Platforms, []string{"linux/amd64", "windows/amd64"}) ||
		!value.VendorOffline || !slices.Equal(value.RequiredOperations, wantOperations) ||
		!slices.EqualFunc(value.Evidence, wantEvidence, equalPublicAuthoringEvidence) || !value.Proven {
		return errors.New("public authoring compatibility manifest differs from the reviewed clean-room contract")
	}
	return nil
}

func equalPublicAuthoringEvidence(left, right publicAuthoringEvidence) bool {
	return left.Module == right.Module && left.Version == right.Version && left.Commit == right.Commit &&
		left.TagObject == right.TagObject && left.Profile == right.Profile && left.ModuleSum == right.ModuleSum &&
		left.GoModSum == right.GoModSum && left.Proxy == right.Proxy && left.SumDB == right.SumDB &&
		left.GeneratedManifestSchema == right.GeneratedManifestSchema && slices.Equal(left.Platforms, right.Platforms) &&
		left.VendorOffline == right.VendorOffline && slices.Equal(left.Operations, right.Operations) &&
		left.VerificationRun == right.VerificationRun && left.Release == right.Release
}

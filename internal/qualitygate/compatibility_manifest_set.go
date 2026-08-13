package main

import "errors"

type compatibilityManifestSet struct {
	policy             compatibilityPolicy
	engine             engineProtocolCompatibility
	plugin             pluginProtocolCompatibility
	durable            durableCompatibility
	security           securityExceptions
	publicAuthoring    publicAuthoringCompatibility
	releasedGeneration releasedGenerationCompatibility
	generatedSource    generatedSourceCompatibility
	goAPI              goAPICompatibility
	apiUsage           apiUsageManifest
	securityProcess    securityProcessManifest
	kernelConcepts     kernelConceptManifest
}

func newCompatibilityManifestSet(root string) (compatibilityManifestSet, error) {
	var manifests compatibilityManifestSet
	var err error
	manifests.policy, _, err = readCanonicalJSON[compatibilityPolicy](root, compatibilityPolicyPath)
	if err != nil {
		return compatibilityManifestSet{}, err
	}
	manifests.engine, _, err = readCanonicalJSON[engineProtocolCompatibility](root, engineProtocolCompatibilityPath)
	if err != nil {
		return compatibilityManifestSet{}, err
	}
	manifests.plugin, _, err = readCanonicalJSON[pluginProtocolCompatibility](root, pluginProtocolCompatibilityPath)
	if err != nil {
		return compatibilityManifestSet{}, err
	}
	manifests.durable, _, err = readCanonicalJSON[durableCompatibility](root, durableCompatibilityPath)
	if err != nil {
		return compatibilityManifestSet{}, err
	}
	manifests.security, _, err = readCanonicalJSON[securityExceptions](root, securityExceptionsPath)
	if err != nil {
		return compatibilityManifestSet{}, err
	}
	manifests.publicAuthoring, _, err = readCanonicalJSON[publicAuthoringCompatibility](root, publicAuthoringCompatibilityPath)
	if err != nil {
		return compatibilityManifestSet{}, err
	}
	manifests.releasedGeneration, _, err = readCanonicalJSON[releasedGenerationCompatibility](root, releasedGenerationCompatibilityPath)
	if err != nil {
		return compatibilityManifestSet{}, err
	}
	manifests.generatedSource, _, err = readCanonicalJSON[generatedSourceCompatibility](root, generatedSourceCompatibilityPath)
	if err != nil {
		return compatibilityManifestSet{}, err
	}
	manifests.goAPI, _, err = readCanonicalJSON[goAPICompatibility](root, goAPICompatibilityPath)
	if err != nil {
		return compatibilityManifestSet{}, err
	}
	manifests.apiUsage, _, err = readCanonicalJSON[apiUsageManifest](root, apiUsagePath)
	if err != nil {
		return compatibilityManifestSet{}, err
	}
	manifests.securityProcess, _, err = readCanonicalJSON[securityProcessManifest](root, securityProcessPath)
	if err != nil {
		return compatibilityManifestSet{}, err
	}
	manifests.kernelConcepts, _, err = readCanonicalJSON[kernelConceptManifest](root, kernelConceptsPath)
	if err != nil {
		return compatibilityManifestSet{}, err
	}
	return manifests, nil
}

func (manifests compatibilityManifestSet) Validate(root string) error {
	validators := []func() error{
		func() error { return validateCompatibilityPolicy(manifests.policy) },
		func() error { return validateEngineProtocolCompatibility(manifests.engine) },
		func() error { return validatePluginProtocolCompatibility(manifests.plugin) },
		func() error { return validateDurableCompatibility(manifests.durable) },
		func() error { return validateSecurityExceptions(manifests.security) },
		func() error { return validatePublicAuthoringCompatibility(manifests.publicAuthoring) },
		manifests.releasedGeneration.ValidateProven,
		func() error { return manifests.releasedGeneration.ValidateSource(root) },
		func() error { return validateGeneratedSourceCompatibility(root, manifests.generatedSource) },
		func() error { return validateGoAPICompatibility(manifests.goAPI, root) },
		func() error { return validateAPIUsage(manifests.apiUsage, manifests.goAPI) },
		func() error { return validateSecurityProcess(manifests.securityProcess) },
		func() error { return validateKernelConcepts(manifests.kernelConcepts) },
		func() error {
			_, err := loadKernelRuntimeBenchmarkBudgets(root)
			return err
		},
	}
	for _, validate := range validators {
		if err := validate(); err != nil {
			return err
		}
	}
	if !compatibilityReferencesAreCanonical(manifests.policy, manifests.engine, manifests.plugin) {
		return errors.New("compatibility manifests do not cross-reference the canonical contracts")
	}
	if err := validateCleanRoomEvidenceProgress(
		manifests.policy,
		manifests.publicAuthoring,
		manifests.generatedSource,
	); err != nil {
		return err
	}
	return validateReleasedGenerationEvidenceProgress(
		manifests.policy,
		manifests.releasedGeneration,
		manifests.engine,
		manifests.plugin,
	)
}

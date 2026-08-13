package main

import (
	"errors"
	"fmt"
	"slices"
)

const (
	apiUsagePath        = "compatibility/api-usage.json"
	securityProcessPath = "compatibility/security-process.json"
	kernelConceptsPath  = "compatibility/kernel-concepts.json"
)

type apiUsageManifest struct {
	Schema            string            `json:"schema"`
	Module            string            `json:"module"`
	Surface           string            `json:"surface"`
	Status            string            `json:"status"`
	DispositionPolicy string            `json:"disposition_policy"`
	Packages          []apiUsagePackage `json:"packages"`
	Unresolved        []string          `json:"unresolved"`
}

type apiUsagePackage struct {
	Path        string   `json:"path"`
	Disposition string   `json:"disposition"`
	Evidence    []string `json:"evidence"`
}

type securityProcessManifest struct {
	Schema            string                  `json:"schema"`
	Module            string                  `json:"module"`
	Status            string                  `json:"status"`
	SupportedVersions supportedVersionProcess `json:"supported_versions"`
	Reporting         reportingProcess        `json:"reporting"`
	Response          responseProcess         `json:"response"`
	DependencyUpdates dependencyUpdateProcess `json:"dependency_updates"`
	GitHubControls    []string                `json:"github_controls"`
}

type supportedVersionProcess struct {
	Policy                      string `json:"policy"`
	Current                     string `json:"current"`
	Previous                    string `json:"previous"`
	WithdrawnVersionsFailClosed bool   `json:"withdrawn_versions_must_fail_closed"`
}

type reportingProcess struct {
	Channel                   string `json:"channel"`
	PublicIssueForbidden      bool   `json:"public_issue_forbidden"`
	AcknowledgementTargetDays int    `json:"acknowledgement_target_days"`
	TriageTargetDays          int    `json:"triage_target_days"`
}

type responseProcess struct {
	CoordinatedDisclosure             bool   `json:"coordinated_disclosure"`
	FixedReleaseOrWithdrawalRequired  bool   `json:"fixed_release_or_withdrawal_required"`
	BackportRequiresIdenticalContract bool   `json:"backport_requires_identical_security_contract"`
	SecurityExceptionManifest         string `json:"security_exception_manifest"`
}

type dependencyUpdateProcess struct {
	Owner               string   `json:"owner"`
	RoutineReviewDays   int      `json:"routine_review_days"`
	CriticalReviewDays  int      `json:"critical_review_days"`
	RequiredGates       []string `json:"required_gates"`
	HiddenNetworkAccess string   `json:"hidden_network_access"`
}

type kernelConceptManifest struct {
	Schema                  string          `json:"schema"`
	Module                  string          `json:"module"`
	Status                  string          `json:"status"`
	Concepts                []kernelConcept `json:"concepts"`
	OptionalNoKernelSurface []string        `json:"optional_no_kernel_surface"`
	Unresolved              []string        `json:"unresolved"`
}

type kernelConcept struct {
	Name        string   `json:"name"`
	Disposition string   `json:"disposition"`
	Consumers   []string `json:"consumers"`
}

func validateAPIUsage(value apiUsageManifest, goAPI goAPICompatibility) error {
	if value.Schema != "spice.agent.api-usage/v1alpha1" || value.Module != modulePath ||
		value.Surface != "public-package-inventory" || value.Status != "reviewed-pre-v1" ||
		value.DispositionPolicy != "retain-requires-concrete-consumer-or-contract-evidence" ||
		value.Unresolved == nil || len(value.Unresolved) != 0 {
		return errors.New("API usage manifest differs from the reviewed pre-v1 contract")
	}
	paths := make([]string, len(value.Packages))
	for index, record := range value.Packages {
		paths[index] = record.Path
		if record.Disposition != "retain" || len(record.Evidence) < 2 || !sortedUnique(record.Evidence) {
			return fmt.Errorf("API usage package %q lacks a reviewed retain disposition and two sorted evidence identities", record.Path)
		}
	}
	if !slices.Equal(paths, goAPI.PublicPackages) {
		return errors.New("API usage package inventory differs from the public Go API inventory")
	}
	return nil
}

func validateSecurityProcess(value securityProcessManifest) error {
	if value.Schema != "spice.agent.security-process/v1alpha1" || value.Module != modulePath ||
		value.Status != "supported-pre-v1" {
		return errors.New("security response and dependency update process differs from the reviewed contract")
	}
	if !validSupportedSecurityVersions(value.SupportedVersions) ||
		!validSecurityReporting(value.Reporting) || !validSecurityResponse(value.Response) ||
		!validDependencyUpdates(value.DependencyUpdates) ||
		!slices.Equal(value.GitHubControls, []string{"dependabot-security-updates", "private-vulnerability-reporting"}) {
		return errors.New("security response and dependency update process differs from the reviewed contract")
	}
	return nil
}

func validSupportedSecurityVersions(value supportedVersionProcess) bool {
	return value.Policy == "current-and-previous-preview-when-fix-is-technically-safe" &&
		value.Current == "v0.1.0-preview.7" && value.Previous == "v0.1.0-preview.6" &&
		value.WithdrawnVersionsFailClosed
}

func validSecurityReporting(value reportingProcess) bool {
	return value.Channel == "github-private-security-advisory" && value.PublicIssueForbidden &&
		value.AcknowledgementTargetDays == 3 && value.TriageTargetDays == 7
}

func validSecurityResponse(value responseProcess) bool {
	return value.CoordinatedDisclosure && value.FixedReleaseOrWithdrawalRequired &&
		value.BackportRequiresIdenticalContract && value.SecurityExceptionManifest == securityExceptionsPath
}

func validDependencyUpdates(value dependencyUpdateProcess) bool {
	wantGates := []string{"go-mod-tidy-diff", "gosec", "govulncheck", "license-review", "vendor-reproducibility"}
	return value.Owner == "spice-framework-maintainers" && value.RoutineReviewDays == 30 &&
		value.CriticalReviewDays == 1 && slices.Equal(value.RequiredGates, wantGates) &&
		value.HiddenNetworkAccess == "forbidden"
}

func validateKernelConcepts(value kernelConceptManifest) error {
	wantOptional := []string{"compaction", "git-workflow", "mcp", "planning", "sandbox", "subagents"}
	if value.Schema != "spice.agent.kernel-concepts/v1alpha1" || value.Module != modulePath ||
		value.Status != "reviewed-one-pending-independent-consumer" ||
		!slices.Equal(value.OptionalNoKernelSurface, wantOptional) ||
		!slices.Equal(value.Unresolved, []string{"semantic-client-session-second-conformance-consumer"}) ||
		len(value.Concepts) != 6 {
		return errors.New("kernel concept manifest differs from the reviewed contract")
	}
	names := make([]string, len(value.Concepts))
	for index, concept := range value.Concepts {
		names[index] = concept.Name
		if concept.Disposition != "multi-consumer-proven" || len(concept.Consumers) < 2 || !sortedUnique(concept.Consumers) {
			return fmt.Errorf("kernel concept %q lacks two sorted independent consumers", concept.Name)
		}
	}
	if !sortedUnique(names) {
		return errors.New("kernel concept inventory must be sorted and unique")
	}
	return nil
}

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
	"time"
)

const (
	compatibilityPolicyPath         = "compatibility/policy.json"
	durableCompatibilityPath        = "compatibility/durable.json"
	engineProtocolCompatibilityPath = "engine/v1/compatibility.json"
	pluginProtocolCompatibilityPath = "plugin/v1/compatibility.json"
	securityExceptionsPath          = "compatibility/security-exceptions.json"
)

type compatibilityPolicy struct {
	Schema             string                `json:"schema"`
	Module             string                `json:"module"`
	Status             string                `json:"status"`
	GoAPI              goAPIPolicy           `json:"go_api"`
	Protocols          protocolPolicy        `json:"protocols"`
	DurableState       durablePolicy         `json:"durable_state"`
	GeneratedSource    generatedSourcePolicy `json:"generated_source"`
	SecurityExceptions string                `json:"security_exceptions"`
	V1Blockers         []string              `json:"v1_blockers"`
}

type goAPIPolicy struct {
	Manifest                   string `json:"manifest"`
	PreV1DeprecationReleases   int    `json:"pre_v1_deprecation_releases"`
	PreV1DeprecationDays       int    `json:"pre_v1_deprecation_days"`
	V1DeprecationMinorReleases int    `json:"v1_deprecation_minor_releases"`
	V1DeprecationDays          int    `json:"v1_deprecation_days"`
	V1Removal                  string `json:"v1_removal"`
}

type protocolPolicy struct {
	Engine                               string `json:"engine"`
	Plugin                               string `json:"plugin"`
	SupportedReleasedGenerationsRequired int    `json:"supported_released_generations_required"`
	SupportMinorReleases                 int    `json:"support_minor_releases"`
	SupportDays                          int    `json:"support_days"`
}

type durablePolicy struct {
	Manifest                   string `json:"manifest"`
	AutomaticMigration         bool   `json:"automatic_migration"`
	ReinterpretExistingVersion bool   `json:"reinterpret_existing_version"`
}

type generatedSourcePolicy struct {
	Status           string `json:"status"`
	RequiredContract string `json:"required_contract"`
}

type engineProtocolCompatibility struct {
	Schema                      string                           `json:"schema"`
	Protocol                    string                           `json:"protocol"`
	ProductionRange             protocolCompatibilityRange       `json:"production_range"`
	InitializationModes         []protocolInitializationMode     `json:"initialization_modes"`
	SourceBuiltMatrix           []protocolSourceBuiltMatrixEntry `json:"source_built_matrix"`
	RequiredCases               []string                         `json:"required_cases"`
	ReleasedBinaryMatrix        releasedBinaryMatrix             `json:"released_binary_matrix"`
	History                     []engineProtocolHistory          `json:"history"`
	PluginCompatibilityManifest string                           `json:"plugin_compatibility_manifest"`
	V1Stable                    bool                             `json:"v1_stable"`
}

type protocolCompatibilityRange struct {
	Minimum string `json:"minimum"`
	Maximum string `json:"maximum"`
}

type protocolInitializationMode struct {
	Name                        string `json:"name"`
	Minimum                     string `json:"minimum"`
	Maximum                     string `json:"maximum"`
	AttemptID                   string `json:"attempt_id"`
	AutomaticUnavailableRetries int    `json:"automatic_unavailable_retries"`
	AmbiguousOutcome            string `json:"ambiguous_outcome"`
}

type protocolSourceBuiltMatrixEntry struct {
	Peer        string   `json:"peer"`
	ServerRange string   `json:"server_range"`
	ClientMode  string   `json:"client_mode"`
	Platforms   []string `json:"platforms"`
}

type releasedBinaryMatrix struct {
	Proven bool   `json:"proven"`
	Claim  string `json:"claim"`
}

type engineProtocolHistory struct {
	Version        string `json:"version"`
	Profile        string `json:"profile"`
	Evidence       string `json:"evidence"`
	ReleasedBinary bool   `json:"released_binary"`
}

type pluginProtocolCompatibility struct {
	Schema               string                     `json:"schema"`
	Protocol             string                     `json:"protocol"`
	ProductionRange      protocolCompatibilityRange `json:"production_range"`
	Versioning           string                     `json:"versioning"`
	Transcripts          []pluginTranscript         `json:"transcripts"`
	SourceBuiltMatrix    pluginSourceBuiltMatrix    `json:"source_built_matrix"`
	History              []pluginProtocolHistory    `json:"history"`
	ProductionHostLaunch pluginProductionHostLaunch `json:"production_host_launch"`
	ReleasedBinaryMatrix releasedBinaryMatrix       `json:"released_binary_matrix"`
	V1Stable             bool                       `json:"v1_stable"`
}

type pluginTranscript struct {
	Version  int    `json:"version"`
	Domain   string `json:"domain"`
	Protocol string `json:"protocol"`
}

type pluginSourceBuiltMatrix struct {
	Bridge        string   `json:"bridge"`
	EngineModes   []string `json:"engine_modes"`
	Languages     []string `json:"languages"`
	RequiredCases []string `json:"required_cases"`
}

type pluginProtocolHistory struct {
	Version        string `json:"version"`
	Transcript     int    `json:"transcript"`
	Evidence       string `json:"evidence"`
	ReleasedBinary bool   `json:"released_binary"`
}

type pluginProductionHostLaunch struct {
	Go     string `json:"go"`
	Python string `json:"python"`
}

type durableCompatibility struct {
	Schema                      string                `json:"schema"`
	Status                      string                `json:"status"`
	Formats                     []durableFormat       `json:"formats"`
	EventMappings               []durableEventMapping `json:"event_mappings"`
	History                     []durableHistory      `json:"history"`
	CurrentAndPreviousSupported bool                  `json:"current_and_previous_supported"`
	MigrationToolAvailable      bool                  `json:"migration_tool_available"`
	V1Stable                    bool                  `json:"v1_stable"`
}

type durableFormat struct {
	Name    string `json:"name"`
	Current string `json:"current"`
	Package string `json:"package"`
	Decode  string `json:"decode"`
}

type durableEventMapping struct {
	Event  string `json:"event"`
	Format string `json:"format"`
}

type durableHistory struct {
	Format             string `json:"format"`
	Status             string `json:"status"`
	Replacement        string `json:"replacement"`
	AutomaticMigration bool   `json:"automatic_migration"`
}

type securityExceptions struct {
	Schema                      string              `json:"schema"`
	Module                      string              `json:"module"`
	DowngradeToWithdrawnVersion string              `json:"downgrade_to_withdrawn_version"`
	Active                      []securityException `json:"active"`
	History                     []securityException `json:"history"`
}

type securityException struct {
	ID          string   `json:"id"`
	Advisory    string   `json:"advisory"`
	Affected    []string `json:"affected"`
	FixedIn     string   `json:"fixed_in"`
	Effect      string   `json:"effect"`
	EffectiveAt string   `json:"effective_at"`
	ReviewBy    string   `json:"review_by"`
	Migration   string   `json:"migration"`
	Status      string   `json:"status"`
	Downgrade   bool     `json:"downgrade"`
}

func checkCompatibilityManifests(root string) error {
	policy, _, err := readCanonicalJSON[compatibilityPolicy](root, compatibilityPolicyPath)
	if err != nil {
		return err
	}
	engine, _, err := readCanonicalJSON[engineProtocolCompatibility](root, engineProtocolCompatibilityPath)
	if err != nil {
		return err
	}
	plugin, _, err := readCanonicalJSON[pluginProtocolCompatibility](root, pluginProtocolCompatibilityPath)
	if err != nil {
		return err
	}
	durable, _, err := readCanonicalJSON[durableCompatibility](root, durableCompatibilityPath)
	if err != nil {
		return err
	}
	security, _, err := readCanonicalJSON[securityExceptions](root, securityExceptionsPath)
	if err != nil {
		return err
	}
	goAPI, _, err := readCanonicalJSON[goAPICompatibility](root, goAPICompatibilityPath)
	if err != nil {
		return err
	}
	if err := validateCompatibilityPolicy(policy); err != nil {
		return err
	}
	if err := validateEngineProtocolCompatibility(engine); err != nil {
		return err
	}
	if err := validatePluginProtocolCompatibility(plugin); err != nil {
		return err
	}
	if err := validateDurableCompatibility(durable); err != nil {
		return err
	}
	if err := validateSecurityExceptions(security); err != nil {
		return err
	}
	if err := validateGoAPICompatibility(goAPI, root); err != nil {
		return err
	}
	if policy.GoAPI.Manifest != goAPICompatibilityPath || policy.Protocols.Engine != engineProtocolCompatibilityPath ||
		policy.Protocols.Plugin != pluginProtocolCompatibilityPath || policy.DurableState.Manifest != durableCompatibilityPath ||
		policy.SecurityExceptions != securityExceptionsPath || engine.PluginCompatibilityManifest != pluginProtocolCompatibilityPath {
		return errors.New("compatibility manifests do not cross-reference the canonical contracts")
	}
	return nil
}

func checkEngineProtocolCompatibility(root string) error {
	value, _, err := readCanonicalJSON[engineProtocolCompatibility](root, engineProtocolCompatibilityPath)
	if err != nil {
		return err
	}
	return validateEngineProtocolCompatibility(value)
}

func validateCompatibilityPolicy(value compatibilityPolicy) error {
	wantBlockers := []string{"external-author-proof", "frozen-generated-source-contract", "released-engine-n-minus-one", "released-plugin-n-minus-one", "stable-benchmark-regression-policy"}
	if value.Schema != "spice.agent.compatibility.policy/v1alpha1" || value.Module != modulePath || value.Status != "pre-v1-enforced-not-stable" ||
		value.GoAPI.PreV1DeprecationReleases != 1 || value.GoAPI.PreV1DeprecationDays != 30 ||
		value.GoAPI.V1DeprecationMinorReleases != 2 || value.GoAPI.V1DeprecationDays != 180 || value.GoAPI.V1Removal != "next-module-major" ||
		value.Protocols.SupportedReleasedGenerationsRequired != 2 || value.Protocols.SupportMinorReleases != 2 || value.Protocols.SupportDays != 180 ||
		value.DurableState.AutomaticMigration || value.DurableState.ReinterpretExistingVersion || value.GeneratedSource.Status != "blocked" ||
		value.GeneratedSource.RequiredContract != "immutable-non-development-generator-and-frozen-ownership-schema" || !slices.Equal(value.V1Blockers, wantBlockers) {
		return errors.New("compatibility policy differs from the reviewed pre-v1 contract")
	}
	return nil
}

func validateEngineProtocolCompatibility(value engineProtocolCompatibility) error {
	want := expectedEngineProtocolCompatibility()
	if value.Schema != want.Schema || value.Protocol != want.Protocol || value.ProductionRange != want.ProductionRange ||
		!slices.Equal(value.InitializationModes, want.InitializationModes) || !slices.EqualFunc(value.SourceBuiltMatrix, want.SourceBuiltMatrix, equalSourceBuiltMatrix) ||
		!slices.Equal(value.RequiredCases, want.RequiredCases) || value.ReleasedBinaryMatrix != want.ReleasedBinaryMatrix ||
		value.PluginCompatibilityManifest != want.PluginCompatibilityManifest || value.V1Stable || len(value.History) < len(want.History) ||
		!slices.Equal(value.History[:len(want.History)], want.History) {
		return errors.New("engine protocol compatibility manifest differs from the reviewed contract")
	}
	return nil
}

func equalSourceBuiltMatrix(left, right protocolSourceBuiltMatrixEntry) bool {
	return left.Peer == right.Peer && left.ServerRange == right.ServerRange && left.ClientMode == right.ClientMode && slices.Equal(left.Platforms, right.Platforms)
}

func expectedEngineProtocolCompatibility() engineProtocolCompatibility {
	platforms := []string{"linux/amd64", "windows/amd64"}
	return engineProtocolCompatibility{
		Schema: "spice.agent.engine.compatibility/v1alpha2", Protocol: "spice.agent.engine.v1",
		ProductionRange: protocolCompatibilityRange{Minimum: "1.0.0", Maximum: "1.3.0"},
		InitializationModes: []protocolInitializationMode{
			{Name: "legacy", Minimum: "1.0.0", Maximum: "1.2.0", AttemptID: "forbidden", AmbiguousOutcome: "non-retryable"},
			{Name: "exact-replay", Minimum: "1.3.0", Maximum: "1.3.0", AttemptID: "required", AutomaticUnavailableRetries: 1, AmbiguousOutcome: "same-request-and-attempt-only"},
		},
		SourceBuiltMatrix: []protocolSourceBuiltMatrixEntry{
			{Peer: "previous-semantics", ServerRange: "1.0.0-1.2.0", ClientMode: "legacy", Platforms: slices.Clone(platforms)},
			{Peer: "current", ServerRange: "1.0.0-1.3.0", ClientMode: "exact-replay", Platforms: slices.Clone(platforms)},
		},
		RequiredCases:        []string{"exact-legacy-1.2", "adaptive-current-1.3", "explicit-proven-downgrade", "authentication-definitive", "current-exact-replay-after-response-loss", "legacy-ambiguity-never-retries", "cancellation-conflict-exact-recovery", "process-cleanup"},
		ReleasedBinaryMatrix: releasedBinaryMatrix{Claim: "not-claimed"},
		History: []engineProtocolHistory{
			{Version: "1.2.0", Profile: "legacy", Evidence: "source-built-only"},
			{Version: "1.3.0", Profile: "exact-replay", Evidence: "source-built-current"},
		},
		PluginCompatibilityManifest: pluginProtocolCompatibilityPath,
	}
}

func validatePluginProtocolCompatibility(value pluginProtocolCompatibility) error {
	if value.Schema != "spice.agent.plugin.compatibility/v1alpha1" || value.Protocol != "spice.agent.plugin.v1" ||
		value.ProductionRange != (protocolCompatibilityRange{Minimum: "1.0.0", Maximum: "1.0.0"}) || value.Versioning != "independent-from-engine" ||
		!slices.Equal(value.Transcripts, []pluginTranscript{{Version: 1, Domain: "spice-agent/plugin/v1/initialize", Protocol: "1.0.0"}}) ||
		value.SourceBuiltMatrix.Bridge != "real-process-plugin-v1-to-immutable-run-leased-tool-plan" || !slices.Equal(value.SourceBuiltMatrix.EngineModes, []string{"1.2.0", "1.3.0"}) ||
		!slices.Equal(value.SourceBuiltMatrix.Languages, []string{"go", "python"}) || !sortedUnique(value.SourceBuiltMatrix.RequiredCases) ||
		len(value.History) < 1 || value.History[0] != (pluginProtocolHistory{Version: "1.0.0", Transcript: 1, Evidence: "source-built-go-and-python"}) ||
		value.ProductionHostLaunch != (pluginProductionHostLaunch{Go: "separately-proven", Python: "future-pinned-native-artifact-required"}) ||
		value.ReleasedBinaryMatrix != (releasedBinaryMatrix{Claim: "not-claimed"}) || value.V1Stable {
		return errors.New("plugin protocol compatibility manifest differs from the reviewed independent contract")
	}
	return nil
}

func validateDurableCompatibility(value durableCompatibility) error {
	wantFormats := []durableFormat{
		{Name: "event-tool-started", Current: "spice.agent.tool-started/v1alpha1", Package: "agent", Decode: "DecodeToolStartedOccurrence"},
		{Name: "event-tool-terminal", Current: "spice.agent.tool-terminal/v1alpha1", Package: "agent", Decode: "DecodeToolTerminalOccurrence"},
		{Name: "plan-identity", Current: "spice-agent-plan/v3", Package: "agent", Decode: "NewPlanIdentity"},
		{Name: "snapshot", Current: "spice.agent.snapshot/v1alpha3", Package: "agent", Decode: "ParseSnapshot"},
		{Name: "snapshot-envelope", Current: "spice.agent.snapshot/v1alpha3", Package: "engine/v1", Decode: "ValidateSnapshotEnvelope"},
	}
	wantMappings := []durableEventMapping{{Event: "tool_started", Format: "spice.agent.tool-started/v1alpha1"}, {Event: "tool_completed", Format: "spice.agent.tool-terminal/v1alpha1"}, {Event: "tool_failed", Format: "spice.agent.tool-terminal/v1alpha1"}}
	wantHistory := []durableHistory{
		{Format: "spice-agent-plan/v2", Status: "rejected", Replacement: "spice-agent-plan/v3"},
		{Format: "spice.agent.snapshot/v1alpha1", Status: "rejected", Replacement: "spice.agent.snapshot/v1alpha3"},
		{Format: "spice.agent.snapshot/v1alpha2", Status: "rejected-missing-workspace-authority", Replacement: "spice.agent.snapshot/v1alpha3"},
		{Format: "spice.agent.snapshot/v1alpha3", Status: "current"},
		{Format: "spice.agent.tool-started/v1alpha1", Status: "current"},
		{Format: "spice.agent.tool-terminal/v1alpha1", Status: "current"},
	}
	if value.Schema != "spice.agent.durable.compatibility/v1alpha1" || value.Status != "pre-v1-hard-cuts-recorded" ||
		!slices.Equal(value.Formats, wantFormats) || !slices.Equal(value.EventMappings, wantMappings) || len(value.History) < len(wantHistory) ||
		!slices.Equal(value.History[:len(wantHistory)], wantHistory) || value.CurrentAndPreviousSupported || value.MigrationToolAvailable || value.V1Stable {
		return errors.New("durable compatibility manifest differs from the reviewed hard-cut history")
	}
	return nil
}

func validateSecurityExceptions(value securityExceptions) error {
	if value.Schema != "spice.agent.security-exceptions/v1alpha1" || value.Module != modulePath || value.DowngradeToWithdrawnVersion != "forbidden" {
		return errors.New("security exception manifest differs from the fail-closed contract")
	}
	seen := make(map[string]struct{}, len(value.Active)+len(value.History))
	for _, exception := range append(slices.Clone(value.Active), value.History...) {
		if exception.ID == "" || exception.Advisory == "" || len(exception.Affected) == 0 || !sortedUnique(exception.Affected) ||
			exception.FixedIn == "" || exception.Effect == "" || exception.Migration == "" || exception.Status == "" || exception.Downgrade {
			return errors.New("security exceptions must be complete, sorted, and may never authorize downgrade")
		}
		if _, exists := seen[exception.ID]; exists {
			return errors.New("security exception identifiers must be unique and append-only")
		}
		seen[exception.ID] = struct{}{}
		effective, err := time.Parse(time.RFC3339, exception.EffectiveAt)
		if err != nil {
			return fmt.Errorf("security exception %s effective_at: %w", exception.ID, err)
		}
		review, err := time.Parse(time.RFC3339, exception.ReviewBy)
		if err != nil || !review.After(effective) {
			return fmt.Errorf("security exception %s must have a later valid review_by", exception.ID)
		}
	}
	return nil
}

func readCanonicalJSON[T any](root, relative string) (T, []byte, error) {
	var zero T
	path := filepath.Join(root, filepath.FromSlash(relative))
	content, readErr := os.ReadFile(path) // #nosec G304 -- caller supplies a fixed repository manifest path.
	if readErr != nil {
		return zero, nil, fmt.Errorf("read %s: %w", relative, readErr)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var value T
	if decodeErr := decoder.Decode(&value); decodeErr != nil {
		return zero, nil, fmt.Errorf("decode %s: %w", relative, decodeErr)
	}
	var trailing any
	if trailingErr := decoder.Decode(&trailing); !errors.Is(trailingErr, io.EOF) {
		if trailingErr == nil {
			return zero, nil, fmt.Errorf("%s contains multiple JSON values", relative)
		}
		return zero, nil, fmt.Errorf("decode %s trailing data: %w", relative, trailingErr)
	}
	canonical, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return zero, nil, fmt.Errorf("encode %s: %w", relative, err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(content, canonical) {
		return zero, nil, fmt.Errorf("%s is not canonical JSON", relative)
	}
	return value, content, nil
}

func sortedUnique(values []string) bool {
	return slices.IsSorted(values) && len(slices.Compact(slices.Clone(values))) == len(values) &&
		!slices.Contains(values, "")
}

func compatibilityManifestPaths() []string {
	return []string{compatibilityPolicyPath, durableCompatibilityPath, goAPICompatibilityPath, securityExceptionsPath, engineProtocolCompatibilityPath, pluginProtocolCompatibilityPath}
}

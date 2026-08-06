package compositionfixture

import (
	"context"
	"maps"
	"slices"
)

// @import { Bean, Qualifier } from "github.com/spice-framework/spice/annotation/core"

// Proof is the generated graph's immutable, typed composition evidence.
type Proof struct {
	provider      ProviderAlias
	stages        []TextStage
	fallbackStage FallbackStage
	replacedStage ReplaceableStage
	tools         map[string]ToolAlias
	aliasSelected ToolAlias
	cleanup       *CleanupLog
}

// NewProof captures only constructor-injected values from the generated graph.
//
// @Bean(name="proof")
func NewProof(
	provider ProviderAlias,
	stages []TextStage,
	fallbackStage FallbackStage,
	replacedStage ReplaceableStage,
	tools map[string]ToolAlias,
	// @Qualifier("inspect")
	aliasSelected ToolAlias,
	cleanup *CleanupLog,
) *Proof {
	return &Proof{
		provider:      provider,
		stages:        append([]TextStage(nil), stages...),
		fallbackStage: fallbackStage,
		replacedStage: replacedStage,
		tools:         cloneTools(tools),
		aliasSelected: aliasSelected,
		cleanup:       cleanup,
	}
}

// StageSelections reports the fallback-only and normal-replaced stage names.
func (proof *Proof) StageSelections() (string, string) {
	return proof.fallbackStage.Name(), proof.replacedStage.Name()
}

func cloneTools(tools map[string]ToolAlias) map[string]ToolAlias {
	result := make(map[string]ToolAlias, len(tools))
	maps.Copy(result, tools)
	return result
}

// ProviderName reports the selected normal primary model provider.
func (proof *Proof) ProviderName() string {
	return ProviderName(proof.provider)
}

// Process applies the injected stage collection in its generated order.
func (proof *Proof) Process(ctx context.Context, input string) (string, error) {
	current := input
	for _, currentStage := range proof.stages {
		output, err := currentStage.Process(ctx, current)
		if err != nil {
			return "", err
		}
		current = output
	}
	return current, nil
}

// ToolNames returns the canonical generated map keys.
func (proof *Proof) ToolNames() []string {
	return slices.Sorted(maps.Keys(proof.tools))
}

// AliasSelectedName proves that alias selection does not add an alias map key.
func (proof *Proof) AliasSelectedName() string {
	if proof.aliasSelected == nil {
		return ""
	}
	return proof.aliasSelected.Definition().Name()
}

// CleanupEvents returns the owning application's reverse-cleanup evidence.
func (proof *Proof) CleanupEvents() []string {
	return proof.cleanup.Snapshot()
}

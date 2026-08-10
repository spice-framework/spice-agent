package composition

import (
	"context"
	"errors"

	compaction "github.com/spice-framework/spice-agent/experiments/compaction"
	"github.com/spice-framework/spice-agent/model"
)

// @import { Bean } from "github.com/spice-framework/spice/annotation/core"

// Proof captures exact generated model.Provider interface injection.
type Proof struct{ provider model.Provider }

type ProofBaseProvider struct{}

func (*ProofBaseProvider) Stream(context.Context, model.Request) (model.Stream, error) {
	return nil, errors.New("compile-only base provider")
}

// NewProofBaseProvider contributes the application-owned base implementation.
//
// @Bean(name="compactionBaseProvider")
func NewProofBaseProvider() *ProofBaseProvider { return &ProofBaseProvider{} }

// NewProofCompactionOptions contributes explicit semantic configuration.
//
// @Bean(name="compactionOptions")
func NewProofCompactionOptions() compaction.Options { return compaction.DefaultOptions() }

// NewProofCompactedProvider explicitly wraps the exact base provider.
//
// @Bean(name="compactedModelProvider")
func NewProofCompactedProvider(base *ProofBaseProvider, options compaction.Options) (model.Provider, error) {
	return compaction.NewProvider(base, options)
}

// NewProof proves ordinary generated interface injection without a registry.
//
// @Bean(name="proof")
func NewProof(provider model.Provider) *Proof { return &Proof{provider: provider} }

// ProviderPresent reports that generated construction selected the wrapper.
func (proof *Proof) ProviderPresent() bool { return proof != nil && proof.provider != nil }

var _ model.Provider = (*ProofBaseProvider)(nil)

package composition

import (
	"context"

	recovery "github.com/spice-framework/spice-agent/experiments/sqlite-recovery"
)

// @import { Bean } from "github.com/spice-framework/spice/annotation/core"

// StoreFactory is an explicitly injected embedded-store constructor.
type StoreFactory func(context.Context, string) (*recovery.Store, error)

// Proof captures generated static composition without opening storage.
type Proof struct{ factory StoreFactory }

// NewRecoveryOptions contributes bounded embedded defaults.
//
// @Bean(name="sqliteRecoveryOptions")
func NewRecoveryOptions() recovery.Options { return recovery.Options{} }

// NewStoreFactory contributes the store constructor without a registry.
//
// @Bean(name="sqliteRecoveryStoreFactory")
func NewStoreFactory(options recovery.Options) StoreFactory {
	return func(ctx context.Context, path string) (*recovery.Store, error) {
		return recovery.Open(ctx, path, options)
	}
}

// NewProof proves generated constructor injection.
//
// @Bean(name="sqliteRecoveryProof")
func NewProof(factory StoreFactory) *Proof { return &Proof{factory: factory} }

// HasFactory reports whether generated construction supplied the factory.
func (proof *Proof) HasFactory() bool { return proof != nil && proof.factory != nil }
